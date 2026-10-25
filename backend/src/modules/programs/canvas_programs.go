package programs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	log "github.com/sirupsen/logrus"
)

type CachedCanvasProgram struct {
	Program        ProgramsOverviewTable
	LastUpdated    time.Time
	CompletionRate *float64
	Loading        bool
}

// liveCourse holds one provider course normalized into the shape the live
// program path uses, regardless of which provider it came from.
type liveCourse struct {
	encodedID      uint
	rawID          uint
	name           string
	description    string
	startDt        time.Time
	endDt          *time.Time
	status         ClassStatus
	enrolleeIDs    []string
	weeklySchedule map[time.Weekday][]meetingTime
}

type meetingTime struct {
	startMin int
	endMin   int
}

// isCanvasProvider returns true when the provider is a Canvas (OSS or Cloud) instance.
func isCanvasProvider(p *ppapi.Provider) bool {
	return p.Type == ppapi.CanvasOSS || p.Type == ppapi.CanvasCloud
}

// encodeCanvasClassID packs (providerID, rawCourseID) into the synthetic class ID.
// decodeCanvasClassID is the reverse.
func encodeCanvasClassID(providerID, rawCourseID uint) uint {
	return CanvasClassIDOffset + providerID*1_000_000 + rawCourseID
}

// parseCanvasCourse extracts metadata from a raw Canvas API course map.
// Returns (entry, true) on success, or the zero value and false when the map
// has no valid numeric id field.
func parseCanvasCourse(course map[string]interface{}, providerID uint, now time.Time) (liveCourse, bool) {
	idFloat, ok := course["id"].(float64)
	if !ok {
		return liveCourse{}, false
	}
	rawID := uint(idFloat)
	if !courseIDFitsEncoding(rawID) {
		log.Warnf("canvas course %d exceeds the synthetic class ID range, skipping", rawID)
		return liveCourse{}, false
	}
	name, _ := course["name"].(string)
	description, _ := course["course_code"].(string)
	var startDt time.Time
	if s, ok := course["start_at"].(string); ok && s != "" {
		startDt, _ = time.Parse("2006-01-02T15:04:05Z", s)
	}
	var endDt *time.Time
	status := Active
	if s, ok := course["end_at"].(string); ok && s != "" {
		if t, err := time.Parse("2006-01-02T15:04:05Z", s); err == nil {
			endDt = &t
			if !t.After(now) {
				status = Completed
			}
		}
	}
	return liveCourse{
		encodedID:   encodeCanvasClassID(providerID, rawID),
		rawID:       rawID,
		name:        name,
		description: description,
		startDt:     startDt,
		endDt:       endDt,
		status:      status,
	}, true
}

// buildCourseTimezoneMap extracts the "time_zone" field from each Canvas API
// course map and returns a mapping of raw course ID → IANA timezone string.
func buildCourseTimezoneMap(courses []map[string]interface{}) map[uint]string {
	courseTimezones := make(map[uint]string, len(courses))
	for _, course := range courses {
		if idFloat, ok := course["id"].(float64); ok {
			if tz, ok := course["time_zone"].(string); ok && tz != "" {
				courseTimezones[uint(idFloat)] = tz
			}
		}
	}
	return courseTimezones
}

// canvasMappedUser holds the local user record for a Canvas external_user_id.
type canvasMappedUser struct {
	UserID    uint
	NameFirst string
	NameLast  string
	DocID     string
}

// fetchCanvasMappedUsers resolves a slice of Canvas external user ID strings to
// their local user records via provider_user_mappings. When facilityID is non-zero
// only users in that facility are returned. Returns a map keyed by external_user_id.
func (srv *server) fetchCanvasMappedUsers(providerID uint, canvasUserIDs []string, facilityID uint) (map[string]canvasMappedUser, error) {
	if len(canvasUserIDs) == 0 {
		return make(map[string]canvasMappedUser), nil
	}
	rows, err := srv.providerPlatforms.MappedUsers(context.Background(), providerID, canvasUserIDs, facilityID)
	if err != nil {
		return nil, newDBError(err, "error getting canvas_mapped_users")
	}
	result := make(map[string]canvasMappedUser, len(rows))
	for _, r := range rows {
		result[r.ExternalUserID] = canvasMappedUser{
			UserID:    r.UserID,
			NameFirst: r.NameFirst,
			NameLast:  r.NameLast,
			DocID:     r.DocID,
		}
	}
	return result, nil
}

// fetchCanvasCourseEnrolleeIDs fetches active student enrollments for a Canvas
// course and returns the Canvas user IDs as strings, ready to query
// provider_user_mappings.external_user_id.
func (srv *server) fetchCanvasCourseEnrolleeIDs(provider *ppapi.Provider, rawCourseID uint) ([]string, error) {
	ids, _, err := srv.fetchCanvasCourseEnrollmentData(provider, rawCourseID)
	return ids, err
}

// fetchCanvasCourseEnrollmentData fetches active student enrollments for a Canvas
// course and returns both the Canvas user IDs and a map of userID → enrollment created_at.
func (srv *server) fetchCanvasCourseEnrollmentData(provider *ppapi.Provider, rawCourseID uint) ([]string, map[string]*time.Time, error) {
	enrollURL := fmt.Sprintf(
		"%s/api/v1/courses/%d/enrollments?type[]=StudentEnrollment&state[]=active&per_page=100",
		provider.BaseURL, rawCourseID,
	)
	enrollments, err := srv.fetchAllCanvasPages(context.Background(), provider, enrollURL, 0)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0, len(enrollments))
	dates := make(map[string]*time.Time, len(enrollments))
	for _, e := range enrollments {
		if u, ok := e["user"].(map[string]interface{}); ok {
			if id, ok := u["id"].(float64); ok {
				userIDStr := fmt.Sprintf("%d", int(id))
				ids = append(ids, userIDStr)
				if createdAtStr, ok := e["created_at"].(string); ok && createdAtStr != "" {
					if t, err := time.Parse(time.RFC3339, createdAtStr); err == nil {
						dates[userIDStr] = &t
					}
				}
			}
		}
	}
	return ids, dates, nil
}

// canvasProgramCacheTTL is how long a cached program entry is served before it
// is refreshed.
const canvasProgramCacheTTL = 5 * time.Minute

// canvasCacheRetryDelay is how long a failed refresh is left alone before the
// next request retries it.
const canvasCacheRetryDelay = 30 * time.Second

// retryableCacheTimestamp dates an entry so it reads as stale once
// canvasCacheRetryDelay has passed. A refresh that failed writes this rather
// than time.Now(), so the entry is neither a loading marker that never resolves
// nor an empty result served as fresh for a whole TTL.
func retryableCacheTimestamp(ttl time.Duration) time.Time {
	return time.Now().Add(canvasCacheRetryDelay - ttl)
}

// carryOverFailedProviders re-appends the rows a previous cache entry held for
// the providers that failed this refresh. Without it a provider being briefly
// unreachable drops its rows from the page and caches that absence as a
// complete result.
func carryOverFailedProviders[T any](fresh, previous []T, failedProviders []uint, programID func(T) uint) []T {
	if len(failedProviders) == 0 || len(previous) == 0 {
		return fresh
	}
	failed := make(map[uint]bool, len(failedProviders))
	for _, id := range failedProviders {
		failed[CanvasProgramIDOffset+id] = true
	}
	for _, item := range previous {
		if failed[programID(item)] {
			fresh = append(fresh, item)
		}
	}
	return fresh
}

// warmCanvasProgramCache fires a background goroutine that fetches program data
// from Canvas and writes the result to NATS KV at cacheKey. An in-flight dedup
// guard (canvasInflight) prevents concurrent fetches for the same provider.
// previous is the stale entry being replaced, if there was one; a failed fetch
// restores it so the caller's loading marker does not outlive the attempt.
func (srv *server) warmCanvasProgramCache(provider *ppapi.Provider, facilityID uint, cacheKey string, previous *ProgramsOverviewTable) {
	key := provider.ID
	if _, loaded := srv.canvasInflight.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	kv := srv.buckets[CanvasPrograms]
	providerCopy := *provider
	go func() {
		defer srv.canvasInflight.Delete(key)
		result, err := srv.fetchCanvasProviderProgram(&providerCopy, facilityID)
		if err != nil {
			log.WithError(err).Warnf("warmCanvasProgramCache: failed to fetch canvas program for provider %d", providerCopy.ID)
			// The caller wrote a loading marker before firing this off. Replace it,
			// or the reads below return that marker ahead of any freshness check and
			// the view stays in the loading state until the entry expires.
			srv.restoreCanvasProgram(cacheKey, previous)
			return
		}
		if kv == nil {
			return
		}
		cached := CachedCanvasProgram{Program: result, LastUpdated: time.Now(), Loading: false}
		data, err := json.Marshal(cached)
		if err != nil {
			return
		}
		if _, err := kv.Put(cacheKey, data); err != nil {
			log.WithError(err).Warnf("warmCanvasProgramCache: failed to store canvas program for provider %d", providerCopy.ID)
			return
		}
		// Kick off completion-rate computation in the background.
		// Uses the global (facility=0) key so all callers share the result.
		globalKey := fmt.Sprintf("canvas_program_%d_0", providerCopy.ID)
		go func(p ppapi.Provider, gKey string) {
			rateCtx, rateCancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer rateCancel()
			log.Debugf("background: starting completion rate computation for provider %d", p.ID)
			rate := srv.computeCanvasCompletionRate(rateCtx, &p)
			if rate != nil {
				log.Debugf("background: completion rate for provider %d = %.2f%%", p.ID, *rate)
			} else {
				log.Debugf("background: no completion rate data for provider %d", p.ID)
			}
			entry, err := kv.Get(gKey)
			if err != nil {
				return
			}
			var c CachedCanvasProgram
			if json.Unmarshal(entry.Value(), &c) != nil {
				return
			}
			c.CompletionRate = rate
			if d, err := json.Marshal(c); err == nil {
				if _, err := kv.Put(gKey, d); err != nil {
					log.WithError(err).Warnf("background: failed to update completion rate for provider %d", p.ID)
				}
			}
			log.Debugf("background: completion rate cached for provider %d", p.ID)
		}(providerCopy, globalKey)
	}()
}

// restoreCanvasProgram puts a failed refresh back into a retryable state: the
// stale entry it was replacing, dated so the next request refreshes it, or no
// entry at all when there is nothing worth serving.
func (srv *server) restoreCanvasProgram(cacheKey string, previous *ProgramsOverviewTable) {
	kv := srv.buckets[CanvasPrograms]
	if kv == nil {
		return
	}
	if previous == nil {
		if err := kv.Delete(cacheKey); err != nil {
			log.WithError(err).Warnf("restoreCanvasProgram: failed to clear loading marker at %s", cacheKey)
		}
		return
	}
	cached := CachedCanvasProgram{
		Program:     *previous,
		LastUpdated: retryableCacheTimestamp(canvasProgramCacheTTL),
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return
	}
	if _, err := kv.Put(cacheKey, data); err != nil {
		log.WithError(err).Warnf("restoreCanvasProgram: failed to restore entry at %s", cacheKey)
	}
}

// canvasPageSlice returns the Canvas rows that belong on the requested page.
// Canvas rows are synthetic (one per provider platform, not a `programs` row)
// and have no position in the SQL sort order, so the combined list puts them
// after every database row: a Canvas row at index i occupies combined-list
// position dbTotal+i. A page keeps only the rows whose positions land inside
// its own window, which is what makes meta.total and per_page agree on every
// page.
func canvasPageSlice(canvas []ProgramsOverviewTable, dbTotal int64, args *QueryContext) []ProgramsOverviewTable {
	if args.All {
		return canvas
	}
	count := int64(len(canvas))
	start := min(max(int64(args.CalcOffset())-dbTotal, 0), count)
	end := min(max(int64(args.CalcOffset()+args.PerPage)-dbTotal, 0), count)
	if start >= end {
		return nil
	}
	return canvas[start:end]
}

// filterCanvasPrograms applies the same search/filter predicates that
// GetProgramsOverviewTable applies in SQL, in memory, to the synthetic
// per-provider rows getCanvasProviderPrograms returns. Canvas rows have no
// backing `programs` row, so the database-layer WHERE clauses never see them
// — without this they bypass every filter and are always counted in
// meta.total regardless of what actually matched.
//
// A filter with no meaningful counterpart on a Canvas row (credit type,
// funding type, capacity utilization, completion/attendance rate, the
// dept-admin facility_id filter) excludes the row: a row that cannot satisfy
// a predicate should not appear to match it.
func filterCanvasPrograms(programs []ProgramsOverviewTable, args *QueryContext, filters map[string]string) []ProgramsOverviewTable {
	kept := make([]ProgramsOverviewTable, 0, len(programs))
programLoop:
	for _, p := range programs {
		if args.Search != "" {
			q := strings.ToLower(args.Search)
			if !strings.Contains(strings.ToLower(p.ProgramName), q) &&
				!strings.Contains(strings.ToLower(p.Description), q) {
				continue
			}
		}
		for col, val := range filters {
			switch col {
			case "programs.is_active":
				if !canvasStatusMatches(val) {
					continue programLoop
				}
			case "pt.program_types":
				if !canvasTypesMatch(p.Types, val) {
					continue programLoop
				}
			case "mr.total_active_classes":
				if !canvasNumericMatches(p.TotalActiveClasses, val) {
					continue programLoop
				}
			case "mr.total_active_enrollments":
				if !canvasNumericMatches(p.TotalActiveEnrollments, val) {
					continue programLoop
				}
			case "mr.total_classes":
				if !canvasNumericMatches(p.TotalClasses, val) {
					continue programLoop
				}
			case "mr.total_enrollments":
				if !canvasNumericMatches(p.TotalEnrollments, val) {
					continue programLoop
				}
			case "pct.credit_types", "programs.funding_type", "capacity_utilization",
				"completion_rate", "attendance_rate", "mr.total_active_facilities", "facility_id":
				continue programLoop
			}
		}
		kept = append(kept, p)
	}
	return kept
}

// canvasStatusMatches reports whether a Canvas provider row satisfies a
// `programs.is_active` filter. Canvas rows always represent a live,
// unarchived connection, so they only ever match "Available".
func canvasStatusMatches(val string) bool {
	return slices.Contains(strings.Split(val, "|"), "Available")
}

// canvasTypesMatch mirrors the SQL side's `pt.program_types ~* val` regex
// match against the Canvas row's single hardcoded type ("College").
func canvasTypesMatch(types, val string) bool {
	re, err := regexp.Compile("(?i)" + val)
	if err != nil {
		return false
	}
	return re.MatchString(types)
}

// canvasNumericMatches evaluates a `filter_mr.total_*`-style operator/value
// pair against a Canvas row's metric. A malformed value parses to no
// operator, same as the SQL-side applyNumericFilter, and is treated the same
// way: the filter doesn't apply, so the row is not excluded by it. A nil
// metric (the row is still loading and has no value yet) cannot satisfy an
// otherwise well-formed filter, so it is excluded.
func canvasNumericMatches(value *int64, val string) bool {
	op, num := ParseOperatorAndValue(val)
	if op == "" {
		return true
	}
	if value == nil {
		return false
	}
	v := float64(*value)
	switch op {
	case "=":
		return v == num
	case "!=":
		return v != num
	case ">=":
		return v >= num
	case "<=":
		return v <= num
	case ">":
		return v > num
	case "<":
		return v < num
	default:
		return false
	}
}

// getCanvasProviderPrograms returns one synthetic ProgramsOverviewTable entry per
// enabled Canvas provider platform. Results are served from NATS KV when fresh
// (< 5 min old). On cache miss or stale entry a loading placeholder is written
// to KV and warmCanvasProgramCache is fired in the background, returning a
// Loading=true entry so the frontend can poll until data is ready.
// When adminRole is FacilityAdmin, enrollment counts are scoped to facilityID.
func (srv *server) getCanvasProviderPrograms(facilityID uint, adminRole users.Role) ([]ProgramsOverviewTable, error) {
	providers, err := srv.activeProviders(context.Background())
	if err != nil {
		return nil, err
	}
	kv := srv.buckets[CanvasPrograms]
	if kv == nil {
		log.Warn("canvas_programs NATS bucket is nil, skipping cache")
	}
	var result []ProgramsOverviewTable

	scopedFacilityID := uint(0)
	if adminRole == users.FacilityAdmin {
		scopedFacilityID = facilityID
	}

	for _, provider := range providers {
		if !isLiveProgramProvider(&provider) {
			continue
		}
		cacheKey := fmt.Sprintf("canvas_program_%d_%d", provider.ID, scopedFacilityID)

		var previous *ProgramsOverviewTable
		if kv != nil {
			if entry, err := kv.Get(cacheKey); err == nil {
				var cached CachedCanvasProgram
				if json.Unmarshal(entry.Value(), &cached) == nil {
					if cached.Loading {
						// Computation already in progress — return placeholder.
						placeholder := cached.Program
						placeholder.Loading = true
						result = append(result, placeholder)
						continue
					}
					if cached.LastUpdated.Add(canvasProgramCacheTTL).After(time.Now()) {
						result = append(result, cached.Program)
						continue
					}
					stale := cached.Program
					previous = &stale
				}
			}
		}

		// Cache miss or stale — write a loading placeholder and kick off background fetch.
		if kv != nil {
			placeholder := ProgramsOverviewTable{
				ProgramID:   CanvasProgramIDOffset + provider.ID,
				ProgramName: provider.Name,
				Source:      providerSourceLabel(&provider),
				Status:      true,
				Loading:     true,
			}
			if data, err := json.Marshal(CachedCanvasProgram{Program: placeholder, LastUpdated: time.Now(), Loading: true}); err == nil {
				if _, err := kv.Put(cacheKey, data); err != nil {
					log.WithError(err).Warn("getCanvasProviderPrograms: failed to write loading placeholder")
				}
			}
		}
		providerCopy := provider
		srv.warmCanvasProgramCache(&providerCopy, scopedFacilityID, cacheKey, previous)
		result = append(result, ProgramsOverviewTable{
			ProgramID:   CanvasProgramIDOffset + provider.ID,
			ProgramName: provider.Name,
			Source:      providerSourceLabel(&provider),
			Status:      true,
			Loading:     true,
		})
	}
	return result, nil
}

// fetchCanvasProviderProgram calls:
//
//	GET /api/v1/accounts/{accountID}/courses?per_page=100
//
// and builds the synthetic program entry. When facilityID is non-zero, enrollment
// counts are scoped to users belonging to that facility.
// active enrollment is the sum of per-course mapped enrollees (matching the detail
// page calculation), while total enrollment is the all-time provider_user_mappings count.
func (srv *server) fetchCanvasProviderProgram(provider *ppapi.Provider, facilityID uint) (ProgramsOverviewTable, error) {
	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		return ProgramsOverviewTable{}, err
	}
	ctx := context.Background()
	listing, err := reader.ListCourses(ctx)
	if err != nil {
		return ProgramsOverviewTable{}, err
	}

	// Class counts are reported against everything the provider returned, so a
	// record we could not parse still counts toward the total.
	totalClasses := int64(listing.Total)
	var activeClasses int64
	for _, course := range listing.Courses {
		if course.status == Active {
			activeClasses++
		}
	}

	// Sum per-course mapped enrollee counts concurrently, scoped to facility when set.
	// This matches the calculation used on the program detail page.
	var activeEnrollments int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, course := range listing.Courses {
		if course.status != Active {
			continue
		}
		wg.Add(1)
		go func(c liveCourse) {
			defer wg.Done()
			var n int64
			if facilityID != 0 {
				n = srv.countMappedCanvasEnrolleesForFacility(ctx, reader, provider, c, facilityID)
			} else {
				n = srv.countMappedCanvasEnrollees(ctx, reader, provider, c)
			}
			mu.Lock()
			activeEnrollments += n
			mu.Unlock()
		}(course)
	}
	wg.Wait()

	programID := CanvasProgramIDOffset + provider.ID
	return ProgramsOverviewTable{
		ProgramID:              programID,
		ProgramName:            provider.Name,
		Description:            "Courses pulled live from " + providerDisplayName(provider) + " connection: " + provider.Name,
		TotalEnrollments:       &activeEnrollments,
		TotalActiveEnrollments: &activeEnrollments,
		TotalClasses:           &totalClasses,
		TotalActiveClasses:     &activeClasses,
		Types:                  "College",
		Status:                 true,
		Source:                 providerSourceLabel(provider),
	}, nil
}

// fetchAllCanvasPages fetches pages from a Canvas API endpoint and returns the combined results.
// max limits the total number of items returned; 0 means no limit (fetch all pages).
func (srv *server) fetchAllCanvasPages(ctx context.Context, provider *ppapi.Provider, startURL string, max int) ([]map[string]interface{}, error) {
	var all []map[string]interface{}
	for pageURL := startURL; pageURL != ""; {
		// Checked every iteration, not just on startURL: the next page comes from
		// the provider's own Link header.
		if err := requireHTTPS(pageURL); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Add("Authorization", "Bearer "+provider.AccessKey)
		req.Header.Add("Accept", "application/json")
		resp, err := srv.Client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("canvas API returned %d", resp.StatusCode)
		}
		var page []map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		pageURL = NextPageURL(resp.Header.Get("Link"))
		_ = resp.Body.Close()
		all = append(all, page...)
		if max > 0 && len(all) >= max {
			break
		}
	}
	return all, nil
}

// NextPageURL parses an RFC 5988 Link header and returns the URL for rel="next",
// or an empty string if there is no next page.
// It handles quoted strings, varied whitespace, arbitrary attribute order,
// case-insensitive rel tokens, and URIs that contain commas or semicolons.
func NextPageURL(header string) string {
	for _, entry := range splitLinkEntries(header) {
		entry = strings.TrimSpace(entry)
		if !strings.HasPrefix(entry, "<") {
			continue
		}
		uriEnd := strings.IndexByte(entry, '>')
		if uriEnd < 0 {
			continue
		}
		uri := entry[1:uriEnd]
		if linkEntryHasRelNext(entry[uriEnd+1:]) {
			return uri
		}
	}
	return ""
}

// splitLinkEntries splits a Link header value by top-level commas (i.e. commas
// not inside angle brackets or double-quoted strings).
func splitLinkEntries(header string) []string {
	var entries []string
	depth, start := 0, 0
	inQuote := false
	for i := 0; i < len(header); i++ {
		switch header[i] {
		case '<':
			if !inQuote {
				depth++
			}
		case '>':
			if !inQuote && depth > 0 {
				depth--
			}
		case '"':
			inQuote = !inQuote
		case '\\':
			if inQuote && i+1 < len(header) {
				i++ // skip escaped character inside quoted string
			}
		case ',':
			if depth == 0 && !inQuote {
				entries = append(entries, header[start:i])
				start = i + 1
			}
		}
	}
	return append(entries, header[start:])
}

// linkEntryHasRelNext reports whether the parameter portion of a Link entry
// contains a rel attribute whose value includes the token "next"
// (case-insensitive; handles quoted values and multiple space-separated tokens).
func linkEntryHasRelNext(params string) bool {
	inQuote, start := false, 0
	checkParam := func(p string) bool {
		p = strings.TrimSpace(p)
		eq := strings.IndexByte(p, '=')
		if eq < 0 || !strings.EqualFold(strings.TrimSpace(p[:eq]), "rel") {
			return false
		}
		val := strings.TrimSpace(p[eq+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		for _, tok := range strings.Fields(val) {
			if strings.EqualFold(tok, "next") {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(params); i++ {
		switch params[i] {
		case '"':
			inQuote = !inQuote
		case '\\':
			if inQuote && i+1 < len(params) {
				i++
			}
		case ';':
			if !inQuote {
				if checkParam(params[start:i]) {
					return true
				}
				start = i + 1
			}
		}
	}
	return checkParam(params[start:])
}

// fetchCanvasCalendarEvents fetches individual Canvas calendar events for all
// courses in the given provider for the specified date range.
func (srv *server) fetchCanvasCalendarEvents(
	provider *ppapi.Provider,
	start, end time.Time,
	loc *time.Location,
) ([]FacilityProgramClassEvent, error) {
	// Step 1: fetch all courses for this provider (paginated)
	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		return nil, err
	}
	listing, err := reader.ListCourses(context.Background())
	if err != nil {
		return nil, fmt.Errorf("fetching courses for provider %d: %w", provider.ID, err)
	}

	if listing.Total == 0 {
		return []FacilityProgramClassEvent{}, nil
	}

	courseTimezones := listing.Timezones

	// Providers that describe a weekly meeting pattern rather than dated calendar
	// events have no calendar endpoint to call, so their pattern is expanded onto
	// the same facility calendar.
	if !isCanvasProvider(provider) {
		return weeklyScheduleFacilityEvents(provider, listing.Courses, start, end, loc), nil
	}

	// Step 2: build context_codes[] query params using url.Values so brackets are encoded
	params := url.Values{}
	params.Set("start_date", start.Format("2006-01-02"))
	params.Set("end_date", end.Format("2006-01-02"))
	params.Set("per_page", "100")
	for _, course := range listing.Courses {
		params.Add("context_codes[]", fmt.Sprintf("course_%d", course.rawID))
	}
	rawEvents, err := srv.fetchAllCanvasPages(context.Background(), provider, provider.BaseURL+"/api/v1/calendar_events?"+params.Encode(), 0)
	if err != nil {
		return nil, fmt.Errorf("canvas API error fetching calendar events for provider %d: %w", provider.ID, err)
	}

	var events []FacilityProgramClassEvent
	for _, event := range rawEvents {
		startAtStr, _ := event["start_at"].(string)
		endAtStr, _ := event["end_at"].(string)
		if startAtStr == "" || endAtStr == "" {
			continue
		}
		startAt, err := time.Parse(time.RFC3339, startAtStr)
		if err != nil {
			continue
		}
		endAt, err := time.Parse(time.RFC3339, endAtStr)
		if err != nil {
			continue
		}

		title, _ := event["title"].(string)
		isCancelled := event["workflow_state"] == "deleted"

		var id uint
		if idFloat, ok := event["id"].(float64); ok {
			id = encodeCanvasClassID(provider.ID, uint(idFloat))
		}

		var classID uint
		var canvasTimezone string
		if contextCode, ok := event["context_code"].(string); ok && strings.HasPrefix(contextCode, "course_") {
			if rawCourseID, err := strconv.ParseUint(strings.TrimPrefix(contextCode, "course_"), 10, 64); err == nil {
				classID = encodeCanvasClassID(provider.ID, uint(rawCourseID))
				canvasTimezone = courseTimezones[uint(rawCourseID)]
			}
		}

		ev := FacilityProgramClassEvent{
			IsCanvasEvent:  true,
			Source:         providerSourceLabel(provider),
			IsCancelled:    isCancelled,
			ProgramID:      CanvasProgramIDOffset + provider.ID,
			ProgramName:    provider.Name,
			ClassName:      title,
			StartTime:      &startAt,
			EndTime:        &endAt,
			ClassStatus:    Active,
			CanvasTimezone: canvasTimezone,
		}
		ev.ID = id
		ev.CohortID = classID
		events = append(events, ev)
	}
	return events, nil
}

// weeklyScheduleFacilityEvents expands every course's weekly meeting pattern into
// facility calendar events across [start, end]. Occurrences of a class share the
// class's synthetic ID -- the calendar renders from the event objects themselves
// and never looks one up by ID.
func weeklyScheduleFacilityEvents(
	provider *ppapi.Provider,
	courses []liveCourse,
	start, end time.Time,
	loc *time.Location,
) []FacilityProgramClassEvent {
	var events []FacilityProgramClassEvent
	for _, course := range courses {
		// A weekly pattern has no end of its own, so a completed class would keep
		// emitting meetings forever -- including ones dated after it finished. Any
		// other status is emitted and carried through on the event itself.
		if course.status == Completed {
			continue
		}
		classID := encodeCanvasClassID(provider.ID, course.rawID)
		for _, occurrence := range expandWeeklySchedule(course, start, end, loc) {
			ev := FacilityProgramClassEvent{
				IsCanvasEvent: true,
				Source:        providerSourceLabel(provider),
				ProgramID:     CanvasProgramIDOffset + provider.ID,
				ProgramName:   provider.Name,
				ClassName:     course.name,
				StartTime:     &occurrence.StartAt,
				EndTime:       &occurrence.EndAt,
				ClassStatus:   course.status,
				// Without this the calendar would re-interpret the wall-clock time
				// in the viewer's own zone and shift every meeting.
				CanvasTimezone: occurrence.Timezone,
			}
			ev.ID = classID
			ev.CohortID = classID
			events = append(events, ev)
		}
	}
	return events
}

// fetchCanvasClassesAllProviders returns a ProgramClassCohort for every Canvas course
// across all active Canvas provider platforms. Failures per-provider are logged
// and skipped so a single unreachable provider doesn't break the response; the
// IDs of those providers are returned so the caller can tell a complete listing
// from a partial one.
// facilityID must be non-nil and non-zero; for the statewide (all-facilities) case
// use fetchCanvasClassesAllProvidersAllFacilities instead.
func (srv *server) fetchCanvasClassesAllProviders(facilityID *uint) ([]ProgramClassCohort, []uint, error) {
	providers, err := srv.activeProviders(context.Background())
	if err != nil {
		return nil, nil, err
	}
	var facility *Facility
	if facilityID != nil && *facilityID != 0 {
		if f, err := srv.store.GetFacilityByID(int(*facilityID)); err == nil {
			facility = f
		}
	}
	var result []ProgramClassCohort
	var failed []uint
	for _, provider := range providers {
		if !isLiveProgramProvider(&provider) {
			continue
		}
		programID := CanvasProgramIDOffset + provider.ID
		reader, err := srv.newLiveProgramProvider(&provider)
		if err != nil {
			log.WithError(err).Warnf("no live reader for provider %d, skipping", provider.ID)
			failed = append(failed, provider.ID)
			continue
		}
		ctx := context.Background()
		listing, err := reader.ListCourses(ctx)
		if err != nil {
			log.WithError(err).Warnf("failed to fetch canvas courses for provider %d, skipping", provider.ID)
			failed = append(failed, provider.ID)
			continue
		}
		entries := listing.Courses
		if facilityID == nil || *facilityID == 0 {
			continue
		}
		counts := make([]int64, len(entries))
		var wg sync.WaitGroup
		var mu sync.Mutex
		sem := make(chan struct{}, 10)
		for i := range entries {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int) {
				defer wg.Done()
				defer func() { <-sem }()
				n := srv.countMappedCanvasEnrolleesForFacility(ctx, reader, &provider, entries[idx], *facilityID)
				mu.Lock()
				counts[idx] = n
				mu.Unlock()
			}(i)
		}
		wg.Wait()
		for i, entry := range entries {
			result = append(result, ProgramClassCohort{
				DatabaseFields: DatabaseFields{ID: encodeFacilityCanvasClassID(*facilityID, provider.ID, entry.rawID)},
				ProgramID:      programID,
				FacilityID:     *facilityID,
				Facility:       facility,
				// Canvas courses have no row in the class tier, so nothing joins
				// class_name for them. Set it from the course title so the field is
				// populated for every cohort the API returns, Canvas or not.
				ClassName:   entry.name,
				Description: entry.description,
				StartDt:     entry.startDt,
				EndDt:       entry.endDt,
				Status:      entry.status,
				Enrolled:    counts[i],
				IsCanvas:    true,
				Source:      providerSourceLabel(&provider),
				Program: &Program{
					DatabaseFields: DatabaseFields{ID: programID},
					Name:           "College - " + provider.Name,
				},
			})
		}
	}
	return result, failed, nil
}

// fetchCanvasClassesAllProvidersAllFacilities returns one ProgramClassCohort per
// (facility × Canvas course) with facility-scoped IDs. Used by the statewide
// classes index when facility=all so every returned class has a navigable,
// facility-scoped ID.
func (srv *server) fetchCanvasClassesAllProvidersAllFacilities() ([]ProgramClassCohort, []uint, error) {
	providers, err := srv.activeProviders(context.Background())
	if err != nil {
		return nil, nil, err
	}
	facilities, err := srv.store.GetAllFacilitiesOrdered()
	if err != nil {
		return nil, nil, err
	}
	var result []ProgramClassCohort
	var failed []uint
	for _, provider := range providers {
		if !isLiveProgramProvider(&provider) {
			continue
		}
		programID := CanvasProgramIDOffset + provider.ID
		reader, err := srv.newLiveProgramProvider(&provider)
		if err != nil {
			log.WithError(err).Warnf("no live reader for provider %d, skipping", provider.ID)
			failed = append(failed, provider.ID)
			continue
		}
		ctx := context.Background()
		listing, err := reader.ListCourses(ctx)
		if err != nil {
			log.WithError(err).Warnf("failed to fetch canvas courses for provider %d, skipping", provider.ID)
			failed = append(failed, provider.ID)
			continue
		}
		entries := listing.Courses
		// Fetch per-facility enrollment counts for each course concurrently.
		// countMappedCanvasEnrolleesPerFacility returns counts for all facilities at
		// once, so this is O(courses) rather than O(courses × facilities).
		facilityCounts := make([]map[uint]int64, len(entries))
		var wg sync.WaitGroup
		var mu sync.Mutex
		sem := make(chan struct{}, 10)
		for i := range entries {
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int) {
				defer wg.Done()
				defer func() { <-sem }()
				counts := srv.countMappedCanvasEnrolleesPerFacility(ctx, reader, &provider, entries[idx])
				mu.Lock()
				facilityCounts[idx] = counts
				mu.Unlock()
			}(i)
		}
		wg.Wait()
		for _, facility := range facilities {
			facilityPtr := facility
			for i, entry := range entries {
				enrolled := int64(0)
				if facilityCounts[i] != nil {
					enrolled = facilityCounts[i][facility.ID]
				}
				result = append(result, ProgramClassCohort{
					DatabaseFields: DatabaseFields{ID: encodeFacilityCanvasClassID(facility.ID, provider.ID, entry.rawID)},
					ProgramID:      programID,
					FacilityID:     facility.ID,
					Facility:       &facilityPtr,
					ClassName:      entry.name, // see fetchCanvasClassesAllProviders
					Description:    entry.description,
					StartDt:        entry.startDt,
					EndDt:          entry.endDt,
					Status:         entry.status,
					Enrolled:       enrolled,
					IsCanvas:       true,
					Source:         providerSourceLabel(&provider),
					Program: &Program{
						DatabaseFields: DatabaseFields{ID: programID},
						Name:           "College - " + provider.Name,
					},
				})
			}
		}
	}
	return result, failed, nil
}

// appendCanvasEventsForFacility iterates all active Canvas provider platforms
// and collects calendar events for the given date range. Providers that could
// not be read are logged, skipped, and returned so the caller can tell a
// complete listing from a partial one.
func (srv *server) appendCanvasEventsForFacility(dtRng *DateRange, facilityID uint) ([]FacilityProgramClassEvent, []uint, error) {
	loc := srv.facilityLocation(facilityID)
	providers, err := srv.activeProviders(context.Background())
	if err != nil {
		return nil, nil, err
	}
	var result []FacilityProgramClassEvent
	var failed []uint
	for _, provider := range providers {
		if !isLiveProgramProvider(&provider) {
			continue
		}
		evs, err := srv.fetchCanvasCalendarEvents(&provider, dtRng.Start, dtRng.End, loc)
		if err != nil {
			log.WithError(err).Warnf("failed to fetch canvas calendar events for provider %d, skipping", provider.ID)
			failed = append(failed, provider.ID)
			continue
		}
		result = append(result, evs...)
	}
	return result, failed, nil
}

func (srv *server) handleShowCanvasProgram(w http.ResponseWriter, r *http.Request, log sLog, programID uint) error {
	connectionID := programID - CanvasProgramIDOffset
	provider, err := srv.providerByID(r.Context(), connectionID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if !isLiveProgramProvider(provider) {
		return newInvalidIdServiceError(fmt.Errorf("provider %d is not a live program provider", connectionID), "program ID")
	}
	claims := claimsFrom(r)
	if !claims.HasFeature(features.ProviderPlatforms) {
		return newInvalidIdServiceError(fmt.Errorf("provider access disabled for provider %d", connectionID), "program ID")
	}

	facilityID := getQueryContext(r).FacilityID
	cacheKey := fmt.Sprintf("canvas_program_%d_%d", connectionID, facilityID)
	globalKey := fmt.Sprintf("canvas_program_%d_0", connectionID)
	kv := srv.buckets[CanvasPrograms]
	var previous *ProgramsOverviewTable

	// Try facility-scoped key first, then global key.
	if kv != nil {
		for _, key := range []string{cacheKey, globalKey} {
			entry, err := kv.Get(key)
			if err != nil {
				continue
			}
			var cached CachedCanvasProgram
			if json.Unmarshal(entry.Value(), &cached) != nil {
				continue
			}
			if cached.Loading {
				return writeJsonResponse(w, http.StatusOK, ProgramOverviewResponse{
					Program: Program{
						DatabaseFields:     DatabaseFields{ID: programID},
						Name:               "College - " + provider.Name,
						IsActive:           true,
						ProgramTypes:       []ProgramType{},
						ProgramCreditTypes: []ProgramCreditType{},
						Facilities:         []Facility{},
					},
					ActiveClassFacilityIDs: []int{},
					Source:                 providerSourceLabel(provider),
					Loading:                true,
				})
			}
			if cached.LastUpdated.Add(canvasProgramCacheTTL).After(time.Now()) {
				prog := cached.Program
				totalEnrollments := 0
				if prog.TotalEnrollments != nil {
					totalEnrollments = int(*prog.TotalEnrollments)
				}
				activeEnrollments := 0
				if prog.TotalActiveEnrollments != nil {
					activeEnrollments = int(*prog.TotalActiveEnrollments)
				}
				return writeJsonResponse(w, http.StatusOK, ProgramOverviewResponse{
					Program: Program{
						DatabaseFields:     DatabaseFields{ID: programID},
						Name:               "College - " + provider.Name,
						Description:        prog.Description,
						IsActive:           true,
						ProgramTypes:       []ProgramType{},
						ProgramCreditTypes: []ProgramCreditType{},
						Facilities:         []Facility{},
					},
					ActiveResidents:        activeEnrollments,
					ActiveEnrollments:      activeEnrollments,
					TotalEnrollments:       totalEnrollments,
					CompletionRate:         cached.CompletionRate,
					ActiveClassFacilityIDs: []int{},
					Source:                 providerSourceLabel(provider),
				})
			}
			stale := cached.Program
			previous = &stale
			break
		}
	}

	// Cache miss — write a loading placeholder, kick off warm, return loading response.
	if kv != nil {
		placeholder := ProgramsOverviewTable{
			ProgramID:   programID,
			ProgramName: "College - " + provider.Name,
			Source:      providerSourceLabel(provider),
			Status:      true,
			Loading:     true,
		}
		if data, err := json.Marshal(CachedCanvasProgram{Program: placeholder, LastUpdated: time.Now(), Loading: true}); err == nil {
			if _, putErr := kv.Put(cacheKey, data); putErr != nil {
				_ = putErr
			}
		}
	}
	srv.warmCanvasProgramCache(provider, facilityID, cacheKey, previous)
	return writeJsonResponse(w, http.StatusOK, ProgramOverviewResponse{
		Program: Program{
			DatabaseFields:     DatabaseFields{ID: programID},
			Name:               "College - " + provider.Name,
			IsActive:           true,
			ProgramTypes:       []ProgramType{},
			ProgramCreditTypes: []ProgramCreditType{},
			Facilities:         []Facility{},
		},
		ActiveClassFacilityIDs: []int{},
		Source:                 providerSourceLabel(provider),
		Loading:                true,
	})
}

func (srv *server) handleGetCanvasClasses(w http.ResponseWriter, r *http.Request, log sLog, programID uint) error {
	connectionID := programID - CanvasProgramIDOffset
	provider, err := srv.providerByID(r.Context(), connectionID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if !isLiveProgramProvider(provider) {
		return newInvalidIdServiceError(fmt.Errorf("provider %d is not a live program provider", connectionID), "program ID")
	}
	claims := claimsFrom(r)
	if !claims.HasFeature(features.ProviderPlatforms) {
		return newInvalidIdServiceError(fmt.Errorf("provider access disabled for provider %d", connectionID), "program ID")
	}

	facilityID := getQueryContext(r).FacilityID
	if r.URL.Query().Get("all") == "true" || facilityID == 0 {
		return srv.handleGetCanvasClassesByFacility(w, r, provider, programID, connectionID)
	}

	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	listing, err := reader.ListCourses(r.Context())
	if err != nil {
		return newInternalServerServiceError(err, "failed to fetch canvas courses")
	}

	courseTimezones := listing.Timezones

	classes := make([]ProgramClassDetail, 0, len(listing.Courses))
	for _, entry := range listing.Courses {
		classes = append(classes, ProgramClassDetail{
			ProgramClassCohort: ProgramClassCohort{
				DatabaseFields: DatabaseFields{ID: entry.encodedID},
				ProgramID:      programID,
				FacilityID:     0,
				ClassName:      entry.name, // see fetchCanvasClassesAllProviders
				Description:    entry.description,
				StartDt:        entry.startDt,
				EndDt:          entry.endDt,
				Status:         entry.status,
			},
			FacilityName: provider.Name,
		})
	}

	// Concurrently count mapped enrollees for each course, scoped to facility when set.
	counts := make(map[uint]int64, len(classes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range classes {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var n int64
			if facilityID != 0 {
				n = srv.countMappedCanvasEnrolleesForFacility(r.Context(), reader, provider, listing.Courses[idx], facilityID)
			} else {
				n = srv.countMappedCanvasEnrollees(r.Context(), reader, provider, listing.Courses[idx])
			}
			mu.Lock()
			counts[classes[idx].ID] = n
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for i := range classes {
		n := counts[classes[i].ID]
		classes[i].ProgramClassCohort.Enrolled = n
		classes[i].Enrolled = int(n)
	}

	// Batch-fetch upcoming calendar events to populate schedule info.
	scheduleEvents := srv.fetchCanvasCoursesScheduleEvents(provider, listing.Courses, srv.facilityLocation(facilityID))
	facilityTimezone := getQueryContext(r).Timezone
	for i, cls := range classes {
		_, rawID := decodeCanvasClassID(cls.ID)
		if evs := scheduleEvents[rawID]; len(evs) > 0 {
			classes[i].Events = evs
			tz := courseTimezones[rawID]
			if tz == "" {
				tz = facilityTimezone
			}
			sched, _ := FormatClassScheduleAndRoom(evs, tz)
			classes[i].Schedule = sched
		}
	}

	// Swap non-scoped IDs for facility-scoped IDs now that all processing is done.
	// This must happen last because enrollment counting and schedule lookups use
	// decodeCanvasClassID on the old-style IDs.
	if facilityID != 0 {
		for i := range classes {
			_, rawID := decodeCanvasClassID(classes[i].ID)
			newID := encodeFacilityCanvasClassID(facilityID, connectionID, rawID)
			classes[i].ID = newID
			classes[i].FacilityID = facilityID
			for j := range classes[i].Events {
				classes[i].Events[j].CohortID = newID
			}
		}
	}

	args := getQueryContext(r)
	args.Total = int64(len(classes))
	return writePaginatedResponse(w, http.StatusOK, classes, args.IntoMeta())
}

// handleGetCanvasClassesByFacility returns one ProgramClassDetail per (facility × Canvas course)
// with per-facility enrollment counts, used by the statewide overview so each facility row
// expands to show all Canvas courses with enrollment scoped to that facility.
func (srv *server) handleGetCanvasClassesByFacility(w http.ResponseWriter, r *http.Request, provider *ppapi.Provider, programID, connectionID uint) error {
	// Fetch all Canvas courses.
	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	listing, err := reader.ListCourses(r.Context())
	if err != nil {
		return newInternalServerServiceError(err, "failed to fetch canvas courses")
	}

	courseTimezones := listing.Timezones
	entries := listing.Courses

	// Get all facilities.
	facilities, err := srv.store.GetAllFacilitiesOrdered()
	if err != nil {
		return newDatabaseServiceError(err)
	}

	// Concurrently fetch per-facility enrollment counts for each course.
	facilityCounts := make([]map[uint]int64, len(entries))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range entries {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			counts := srv.countMappedCanvasEnrolleesPerFacility(r.Context(), reader, provider, entries[idx])
			mu.Lock()
			facilityCounts[idx] = counts
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	// Canvas calendar events carry absolute times, so one fetch serves every
	// facility. A weekly meeting pattern is wall clock in the facility's own zone,
	// so it has to be rebuilt for each facility row below.
	perFacilitySchedules := !isCanvasProvider(provider)
	var scheduleEvents map[uint][]ProgramClassEvent
	if !perFacilitySchedules {
		scheduleEvents = srv.fetchCanvasCoursesScheduleEvents(provider, entries, nil)
	}

	// Build one row per (facility × course).
	classes := make([]ProgramClassDetail, 0, len(facilities)*len(entries))
	for _, facility := range facilities {
		if perFacilitySchedules {
			scheduleEvents = srv.fetchCanvasCoursesScheduleEvents(provider, entries, locationOrUTC(facility.Timezone))
		}
		for i, entry := range entries {
			enrolled := int64(0)
			if facilityCounts[i] != nil {
				enrolled = facilityCounts[i][facility.ID]
			}
			scopedID := encodeFacilityCanvasClassID(facility.ID, connectionID, entry.rawID)
			var sched string
			var evSlice []ProgramClassEvent
			if evs := scheduleEvents[entry.rawID]; len(evs) > 0 {
				// Cloned because a Canvas listing shares one slice across every
				// facility row, and each row scopes the events to its own class ID.
				evSlice = slices.Clone(evs)
				for j := range evSlice {
					evSlice[j].CohortID = scopedID
				}
				tz := courseTimezones[entry.rawID]
				if tz == "" {
					tz = facility.Timezone
				}
				sched, _ = FormatClassScheduleAndRoom(evSlice, tz)
			}
			classes = append(classes, ProgramClassDetail{
				ProgramClassCohort: ProgramClassCohort{
					DatabaseFields: DatabaseFields{ID: scopedID},
					ProgramID:      programID,
					FacilityID:     facility.ID,
					ClassName:      entry.name, // see fetchCanvasClassesAllProviders
					Description:    entry.description,
					StartDt:        entry.startDt,
					EndDt:          entry.endDt,
					Status:         entry.status,
					Enrolled:       enrolled,
					Events:         evSlice,
				},
				FacilityName: facility.Name,
				Enrolled:     int(enrolled),
				Schedule:     sched,
			})
		}
	}

	args := getQueryContext(r)
	args.Total = int64(len(classes))
	return writePaginatedResponse(w, http.StatusOK, classes, args.IntoMeta())
}

// weekdayToRRuleDay converts a time.Weekday to the two-letter iCalendar abbreviation.
func weekdayToRRuleDay(day time.Weekday) string {
	switch day {
	case time.Monday:
		return "MO"
	case time.Tuesday:
		return "TU"
	case time.Wednesday:
		return "WE"
	case time.Thursday:
		return "TH"
	case time.Friday:
		return "FR"
	case time.Saturday:
		return "SA"
	default:
		return "SU"
	}
}

// buildCanvasEventRRule builds an iCalendar RRULE string and a Go duration string
// from a Canvas calendar event's start/end times and optional rrule fragment.
// When canvasRRule is present (e.g. "FREQ=WEEKLY;BYDAY=MO") it is combined with a
// DTSTART derived from startAt. When absent the event is treated as a single occurrence.
func buildCanvasEventRRule(startAt, endAt time.Time, canvasRRule string) (recurrenceRule, duration string) {
	duration = endAt.Sub(startAt).String()
	dtstart := "DTSTART:" + startAt.UTC().Format("20060102T150405Z")
	if canvasRRule != "" {
		rrulePart := canvasRRule
		if !strings.HasPrefix(rrulePart, "RRULE:") {
			rrulePart = "RRULE:" + rrulePart
		}
		recurrenceRule = dtstart + "\n" + rrulePart
	} else {
		recurrenceRule = fmt.Sprintf("%s\nRRULE:FREQ=WEEKLY;BYDAY=%s;COUNT=1", dtstart, weekdayToRRuleDay(startAt.UTC().Weekday()))
	}
	return
}

// weeklyScheduleClassEvents renders a recurring weekly meeting pattern as the
// synthetic ProgramClassEvents the class lists format with
// FormatClassScheduleAndRoom. A single rule carries one start time and
// one duration, so days that meet at the same time share a rule and every other
// distinct time -- including a second meeting on a day that already has one --
// gets its own. Each DTSTART is anchored on the next occurrence of that rule's
// earliest day, so the rendered wall clock matches the pattern in the facility's
// own zone.
func weeklyScheduleClassEvents(course liveCourse, loc *time.Location, now time.Time) []ProgramClassEvent {
	if len(course.weeklySchedule) == 0 {
		return nil
	}
	if loc == nil {
		loc = time.UTC
	}

	type ruleGroup struct {
		anchorDay time.Weekday
		meeting   meetingTime
		byDay     []string
	}
	var groups []*ruleGroup
	byMeeting := make(map[meetingTime]*ruleGroup)
	// Walk Sunday-to-Saturday rather than ranging the map, so the rules and their
	// BYDAY lists are stable between calls.
	for day := time.Sunday; day <= time.Saturday; day++ {
		rruleDay := weekdayToRRuleDay(day)
		for _, meeting := range course.weeklySchedule[day] {
			group, ok := byMeeting[meeting]
			if !ok {
				group = &ruleGroup{anchorDay: day, meeting: meeting}
				byMeeting[meeting] = group
				groups = append(groups, group)
			}
			if len(group.byDay) == 0 || group.byDay[len(group.byDay)-1] != rruleDay {
				group.byDay = append(group.byDay, rruleDay)
			}
		}
	}
	if len(groups) == 0 {
		return nil
	}

	local := now.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	events := make([]ProgramClassEvent, 0, len(groups))
	for _, group := range groups {
		offset := (int(group.anchorDay) - int(local.Weekday()) + 7) % 7
		dtstart := midnight.AddDate(0, 0, offset).Add(time.Duration(group.meeting.startMin) * time.Minute)
		events = append(events, ProgramClassEvent{
			RecurrenceRule: fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=WEEKLY;BYDAY=%s",
				dtstart.UTC().Format("20060102T150405Z"), strings.Join(group.byDay, ",")),
			Duration: (time.Duration(group.meeting.endMin-group.meeting.startMin) * time.Minute).String(),
		})
	}
	return events
}

// fetchCanvasCoursesScheduleEvents returns a map of rawCourseID → synthetic
// ProgramClassEvents carrying a RecurrenceRule and Duration suitable for
// FormatClassScheduleAndRoom, mirroring how regular class events work.
// Canvas is read from its calendar events endpoint; providers that describe a
// recurring weekly pattern instead have no such endpoint, so their schedule is
// built from the pattern the course listing already carried. A weekly pattern is
// wall clock in loc, so a caller building rows for more than one facility has to
// call this once per facility.
func (srv *server) fetchCanvasCoursesScheduleEvents(
	provider *ppapi.Provider,
	courses []liveCourse,
	loc *time.Location,
) map[uint][]ProgramClassEvent {
	result := make(map[uint][]ProgramClassEvent, len(courses))
	if len(courses) == 0 {
		return result
	}
	if !isCanvasProvider(provider) {
		now := time.Now()
		for _, course := range courses {
			if evs := weeklyScheduleClassEvents(course, loc, now); len(evs) > 0 {
				result[course.rawID] = evs
			}
		}
		return result
	}

	rawCourseIDs := make([]uint, 0, len(courses))
	for _, course := range courses {
		rawCourseIDs = append(rawCourseIDs, course.rawID)
	}

	today := time.Now()
	params := url.Values{}
	params.Set("start_date", today.Format("2006-01-02"))
	params.Set("end_date", today.AddDate(0, 0, 28).Format("2006-01-02"))
	params.Set("per_page", "100")
	params.Set("type", "event")
	for _, id := range rawCourseIDs {
		params.Add("context_codes[]", fmt.Sprintf("course_%d", id))
	}

	rawEvents, err := srv.fetchAllCanvasPages(context.Background(), provider, provider.BaseURL+"/api/v1/calendar_events?"+params.Encode(), 0)
	if err != nil {
		log.WithError(err).Warnf("fetchCanvasCoursesScheduleEvents: failed to fetch events for provider %d", provider.ID)
		return result
	}

	for _, event := range rawEvents {
		contextCode, _ := event["context_code"].(string)
		if !strings.HasPrefix(contextCode, "course_") {
			continue
		}
		courseIDInt, err := strconv.ParseUint(strings.TrimPrefix(contextCode, "course_"), 10, 64)
		if err != nil {
			continue
		}
		rawID := uint(courseIDInt)
		if _, seen := result[rawID]; seen {
			continue // keep only the first (earliest) event per course
		}
		startAtStr, _ := event["start_at"].(string)
		endAtStr, _ := event["end_at"].(string)
		if startAtStr == "" || endAtStr == "" {
			continue
		}
		startAt, err := time.Parse(time.RFC3339, startAtStr)
		if err != nil {
			continue
		}
		endAt, err := time.Parse(time.RFC3339, endAtStr)
		if err != nil {
			continue
		}
		canvasRRule, _ := event["rrule"].(string)
		rruleStr, durStr := buildCanvasEventRRule(startAt, endAt, canvasRRule)
		result[rawID] = []ProgramClassEvent{{
			RecurrenceRule: rruleStr,
			Duration:       durStr,
		}}
	}
	return result
}

// invalidateCanvasProgramCache deletes the NATS KV cache entries for the given Canvas
// provider so the next read recomputes enrollment counts. Both the global (scopedFacility=0)
// entry and the facility-scoped entry for the unlinked user's facility are purged.
// Non-Canvas providers and nil buckets are silently skipped.
func (srv *server) invalidateCanvasProgramCache(providerID uint, userID int) {
	provider, err := srv.providerByID(context.Background(), providerID)
	if err != nil || !isLiveProgramProvider(provider) {
		return
	}
	kv := srv.buckets[CanvasPrograms]
	if kv == nil {
		return
	}
	globalKey := fmt.Sprintf("canvas_program_%d_0", providerID)
	if err := kv.Delete(globalKey); err != nil {
		log.WithError(err).Warnf("invalidateCanvasProgramCache: failed to delete key %s", globalKey)
	}
	user, err := srv.store.GetUserByID(uint(userID))
	if err == nil && user.FacilityID != 0 {
		facilityKey := fmt.Sprintf("canvas_program_%d_%d", providerID, user.FacilityID)
		if err := kv.Delete(facilityKey); err != nil {
			log.WithError(err).Warnf("invalidateCanvasProgramCache: failed to delete key %s", facilityKey)
		}
	}
}

// countMappedCanvasEnrolleesPerFacility returns a map of facility_id → count of
// mapped users from that facility for a course. Enrollees are resolved through
// courseEnrollees, so a provider that reports them inline with its course listing
// costs no extra request here.
func (srv *server) countMappedCanvasEnrolleesPerFacility(ctx context.Context, reader LiveProgramProvider, provider *ppapi.Provider, course liveCourse) map[uint]int64 {
	canvasUserIDs, err := courseEnrollees(ctx, reader, course)
	if err != nil {
		log.WithError(err).Warnf("countMappedCanvasEnrolleesPerFacility: failed to fetch enrollments for course %d", course.rawID)
		return nil
	}
	if len(canvasUserIDs) == 0 {
		return map[uint]int64{}
	}
	// The count runs even if ctx's deadline passed during the Canvas fetch, so
	// the enrollees fetched in time still count.
	result, err := srv.providerPlatforms.CountMappedPerFacility(context.WithoutCancel(ctx), provider.ID, canvasUserIDs)
	if err != nil {
		log.WithError(err).Warnf("countMappedCanvasEnrolleesPerFacility: failed to count enrollees for course %d", course.rawID)
		return nil
	}
	return result
}

// decodeCanvasClassID recovers (providerID, rawCourseID) from an encoded Canvas class ID.
func decodeCanvasClassID(classID uint) (providerID uint, rawCourseID uint) {
	remainder := classID - CanvasClassIDOffset
	return remainder / 1_000_000, remainder % 1_000_000
}

// encodeFacilityCanvasClassID packs (facilityID, providerID, rawCourseID) into a
// facility-scoped synthetic class ID so detail/enrollment handlers can extract the
// facility from the ID itself without relying on a URL parameter.
// Limits: facilityID 0–999, providerID 0–999, rawCourseID 0–999_999.
func encodeFacilityCanvasClassID(facilityID, providerID, rawCourseID uint) uint {
	return CanvasFacilityClassIDOffset + facilityID*1_000_000_000 + providerID*1_000_000 + rawCourseID
}

// decodeFacilityCanvasClassID is the reverse of encodeFacilityCanvasClassID.
func decodeFacilityCanvasClassID(classID uint) (facilityID, providerID, rawCourseID uint) {
	remainder := classID - CanvasFacilityClassIDOffset
	facilityID = remainder / 1_000_000_000
	remainder = remainder % 1_000_000_000
	providerID = remainder / 1_000_000
	rawCourseID = remainder % 1_000_000
	return
}

// resolveCanvasClassParts extracts (facilityID, providerID, rawCourseID) from any
// canvas class ID. For facility-scoped IDs the embedded facilityID is returned;
// for old-style IDs facilityID is 0 and the caller should fall back to JWT claims.
func resolveCanvasClassParts(classID uint) (facilityID, providerID, rawCourseID uint) {
	if classID >= CanvasFacilityClassIDOffset {
		return decodeFacilityCanvasClassID(classID)
	}
	p, c := decodeCanvasClassID(classID)
	return 0, p, c
}

// countMappedCanvasEnrolleesForFacility is like countMappedCanvasEnrollees but
// only counts users belonging to the given facility.
func (srv *server) countMappedCanvasEnrolleesForFacility(ctx context.Context, reader LiveProgramProvider, provider *ppapi.Provider, course liveCourse, facilityID uint) int64 {
	canvasUserIDs, err := courseEnrollees(ctx, reader, course)
	if err != nil {
		log.WithError(err).Warnf("countMappedCanvasEnrolleesForFacility: failed to fetch enrollments for course %d", course.rawID)
		return 0
	}
	if len(canvasUserIDs) == 0 {
		return 0
	}
	// The count runs even if ctx's deadline passed during the Canvas fetch, so
	// the enrollees fetched in time still count.
	count, err := srv.providerPlatforms.CountMappedAtFacility(context.WithoutCancel(ctx), provider.ID, canvasUserIDs, facilityID)
	if err != nil {
		log.WithError(err).Warnf("countMappedCanvasEnrolleesForFacility: failed to count enrollees for course %d", course.rawID)
		return 0
	}
	return count
}

// countMappedCanvasEnrollees returns how many of a course's students have a
// ProviderUserMapping. See countMappedCanvasEnrolleesPerFacility on how the
// enrollees are resolved.
func (srv *server) countMappedCanvasEnrollees(ctx context.Context, reader LiveProgramProvider, provider *ppapi.Provider, course liveCourse) int64 {
	canvasUserIDs, err := courseEnrollees(ctx, reader, course)
	if err != nil {
		log.WithError(err).Warnf("countMappedCanvasEnrollees: failed to fetch enrollments for course %d", course.rawID)
		return 0
	}
	if len(canvasUserIDs) == 0 {
		return 0
	}
	// The count runs even if ctx's deadline passed during the Canvas fetch, so
	// the enrollees fetched in time still count.
	count, err := srv.providerPlatforms.CountMapped(context.WithoutCancel(ctx), provider.ID, canvasUserIDs)
	if err != nil {
		log.WithError(err).Warnf("countMappedCanvasEnrollees: failed to count enrollees for course %d", course.rawID)
		return 0
	}
	return count
}

// computeCanvasCompletionRate fetches all enrollments across every course for
// the provider (active + completed states) and returns the percentage of mapped
// enrollments that have enrollment_state == "completed".
// ctx should carry a deadline — callers launching this in a goroutine must use
// context.WithTimeout to bound the N+1 Canvas API requests.
func (srv *server) computeCanvasCompletionRate(ctx context.Context, provider *ppapi.Provider) *float64 {
	// The per-course loop below builds Canvas enrollment URLs and bearer auth by
	// hand rather than going through the provider's transport, so running it for
	// any other provider only spends a request per course to get an error back.
	if !isCanvasProvider(provider) {
		return nil
	}
	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		log.WithError(err).Warnf("canvas completion rate: cannot build provider client for provider %d", provider.ID)
		return nil
	}
	listing, err := reader.ListCourses(ctx)
	if err != nil {
		log.WithError(err).Warnf("canvas completion rate: ListCourses failed for provider %d", provider.ID)
		return nil
	}
	if len(listing.Courses) == 0 {
		return nil
	}

	type courseResult struct {
		allIDs       []string
		completedIDs []string
	}
	results := make([]courseResult, len(listing.Courses))
	var wg sync.WaitGroup
	var mu sync.Mutex
	sem := make(chan struct{}, 10)
	for i, course := range listing.Courses {
		rawCourseID := course.rawID
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, courseID uint) {
			defer wg.Done()
			defer func() { <-sem }()
			enrollURL := fmt.Sprintf(
				"%s/api/v1/courses/%d/enrollments?type[]=StudentEnrollment&state[]=active&state[]=completed&per_page=100",
				provider.BaseURL, courseID,
			)
			enrollments, err := srv.fetchAllCanvasPages(ctx, provider, enrollURL, 0)
			if err != nil {
				return
			}
			var all, completed []string
			for _, e := range enrollments {
				u, ok := e["user"].(map[string]interface{})
				if !ok {
					continue
				}
				id, ok := u["id"].(float64)
				if !ok {
					continue
				}
				sid := fmt.Sprintf("%d", int(id))
				all = append(all, sid)
				if state, _ := e["enrollment_state"].(string); state == "completed" {
					completed = append(completed, sid)
				}
			}
			mu.Lock()
			results[idx] = courseResult{allIDs: all, completedIDs: completed}
			mu.Unlock()
		}(i, rawCourseID)
	}
	wg.Wait()

	var allIDs, completedIDs []string
	for _, r := range results {
		allIDs = append(allIDs, r.allIDs...)
		completedIDs = append(completedIDs, r.completedIDs...)
	}
	if len(allIDs) == 0 {
		return nil
	}

	// The count runs even if ctx's deadline passed during the Canvas fetch, so
	// the enrollees fetched in time still count.
	countCtx := context.WithoutCancel(ctx)
	totalMapped, err := srv.providerPlatforms.CountMapped(countCtx, provider.ID, allIDs)
	if err != nil {
		log.WithError(err).Warnf("canvas completion rate: counting mapped enrollees failed for provider %d", provider.ID)
		return nil
	}
	if totalMapped == 0 {
		return nil
	}
	var completedMapped int64
	if len(completedIDs) > 0 {
		completedMapped, err = srv.providerPlatforms.CountMapped(countCtx, provider.ID, completedIDs)
		if err != nil {
			log.WithError(err).Warnf("canvas completion rate: counting completed mapped enrollees failed for provider %d", provider.ID)
			return nil
		}
	}
	rate := float64(completedMapped) / float64(totalMapped) * 100
	return &rate
}

func (srv *server) handleGetCanvasClassDetail(w http.ResponseWriter, r *http.Request, log sLog, classID uint) error {
	facilityIDFromID, providerID, rawCourseID := resolveCanvasClassParts(classID)
	provider, err := srv.providerByID(r.Context(), providerID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if !isLiveProgramProvider(provider) {
		return newInvalidIdServiceError(fmt.Errorf("provider %d is not a live program provider", providerID), "class ID")
	}
	claims := claimsFrom(r)
	if !claims.HasFeature(features.ProviderPlatforms) {
		return newInvalidIdServiceError(fmt.Errorf("provider access disabled for provider %d", providerID), "class ID")
	}

	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		return newInvalidIdServiceError(err, "class ID")
	}
	entry, canvasTimezone, err := reader.GetCourse(r.Context(), rawCourseID)
	if err != nil {
		return newInternalServerServiceError(err, "failed to fetch course from "+providerDisplayName(provider))
	}
	programID := CanvasProgramIDOffset + providerID

	// Use the facility embedded in the ID; fall back to JWT claims for old-style IDs.
	facilityID := facilityIDFromID
	if facilityID == 0 {
		facilityID = getQueryContext(r).FacilityID
	}
	var enrolled int64
	if facilityID != 0 {
		enrolled = srv.countMappedCanvasEnrolleesForFacility(r.Context(), reader, provider, entry, facilityID)
	} else {
		enrolled = srv.countMappedCanvasEnrollees(r.Context(), reader, provider, entry)
	}

	var facility *Facility
	if facilityID != 0 {
		if f, err := srv.store.GetFacilityByID(int(facilityID)); err == nil {
			facility = f
		}
	}

	scheduleEvents := srv.fetchCanvasCoursesScheduleEvents(provider, []liveCourse{entry}, srv.facilityLocation(facilityID))
	events := []ProgramClassEvent{}
	if evs := scheduleEvents[rawCourseID]; len(evs) > 0 {
		events = evs
	}

	cls := ProgramClassCohort{
		DatabaseFields: DatabaseFields{ID: classID},
		ProgramID:      programID,
		FacilityID:     facilityID,
		Facility:       facility,
		ClassName:      entry.name, // see fetchCanvasClassesAllProviders
		Description:    entry.description,
		StartDt:        entry.startDt,
		EndDt:          entry.endDt,
		Status:         entry.status,
		Enrolled:       enrolled,
		IsCanvas:       true,
		Source:         providerSourceLabel(provider),
		CanvasTimezone: canvasTimezone,
		Program: &Program{
			DatabaseFields: DatabaseFields{ID: programID},
			Name:           "College - " + provider.Name,
		},
		Events:      events,
		Enrollments: []ProgramClassEnrollment{},
	}
	return writeJsonResponse(w, http.StatusOK, cls)
}

// fetchCanvasCourseTimezone returns the IANA timezone string for a Canvas course,
// falling back to empty string on any error so callers can use a default.
func (srv *server) fetchCanvasCourseTimezone(provider *ppapi.Provider, rawCourseID uint) string {
	transport, err := srv.transportFor(provider)
	if err != nil {
		return ""
	}
	course, err := transport.fetchOne(context.Background(), provider, canvasCourseURL(provider, rawCourseID))
	if err != nil {
		return ""
	}
	tz, _ := course["time_zone"].(string)
	return tz
}

type canvasScheduleEvent struct {
	ID          uint      `json:"id"`
	Title       string    `json:"title"`
	StartAt     time.Time `json:"start_at"`
	EndAt       time.Time `json:"end_at"`
	IsCancelled bool      `json:"is_cancelled"`
	Timezone    string    `json:"timezone,omitempty"`
}

// expandWeeklySchedule turns a recurring weekly meeting pattern into concrete
// events across [start, end]. Providers that describe a weekly pattern rather
// than dated calendar events (Essential Ed) are rendered on the same calendar
// this way. Days outside the class's own open/close dates are skipped.
// The meeting times are wall-clock text with no zone attached -- an instructor
// typing "9:00 AM" means 9am where the class meets. loc is the facility's
// timezone, so the instants come out right for the people reading the calendar.
func expandWeeklySchedule(course liveCourse, start, end time.Time, loc *time.Location) []canvasScheduleEvent {
	events := []canvasScheduleEvent{}
	if len(course.weeklySchedule) == 0 {
		return events
	}
	if loc == nil {
		loc = time.UTC
	}
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		// The meeting below is emitted on day's calendar date in the facility's
		// zone, while the provider reports open/close as calendar dates in its own
		// bare-UTC wall clock. Comparing the two as instants silently drops the
		// meetings on the last day of class for any facility west of UTC, so both
		// sides are reduced to a plain date first.
		dayDate := calendarDate(day)
		if !course.startDt.IsZero() && dayDate.Before(calendarDate(course.startDt)) {
			continue
		}
		if course.endDt != nil && dayDate.After(calendarDate(*course.endDt)) {
			continue
		}
		for _, meeting := range course.weeklySchedule[day.Weekday()] {
			midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
			events = append(events, canvasScheduleEvent{
				ID:       uint(len(events) + 1),
				Title:    course.name,
				StartAt:  midnight.Add(time.Duration(meeting.startMin) * time.Minute),
				EndAt:    midnight.Add(time.Duration(meeting.endMin) * time.Minute),
				Timezone: loc.String(),
			})
		}
	}
	return events
}

// calendarDate strips a timestamp down to the Y/M/D it reads as in its own zone,
// so dates that arrived in different zones compare as dates rather than instants.
func calendarDate(t time.Time) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// facilityLocation returns a facility's timezone, falling back to UTC when the
// facility is unknown or its timezone will not load.
func (srv *server) facilityLocation(facilityID uint) *time.Location {
	if facilityID == 0 {
		return time.UTC
	}
	facility, err := srv.store.GetFacilityByID(int(facilityID))
	if err != nil {
		return time.UTC
	}
	return locationOrUTC(facility.Timezone)
}

// locationOrUTC loads an IANA timezone name, falling back to UTC when it will
// not load. Callers that already hold a facility use this rather than
// facilityLocation, which reads the facility back from the database.
func locationOrUTC(timezone string) *time.Location {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (srv *server) handleGetCanvasClassSchedule(w http.ResponseWriter, r *http.Request, log sLog) error {
	classID, err := strconv.Atoi(r.PathValue("cohort_id"))
	if err != nil {
		return newInvalidIdServiceError(err, "class_id")
	}
	if uint(classID) < CanvasClassIDOffset {
		return newInvalidIdServiceError(fmt.Errorf("class %d is not a canvas class", classID), "class_id")
	}
	claims := claimsFrom(r)
	if !claims.HasFeature(features.ProviderPlatforms) {
		return newInvalidIdServiceError(fmt.Errorf("provider access disabled for class %d", classID), "class_id")
	}
	facilityIDFromID, providerID, rawCourseID := resolveCanvasClassParts(uint(classID))
	provider, err := srv.providerByID(r.Context(), providerID)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	now := time.Now()
	var start, end time.Time
	if sdStr := r.URL.Query().Get("start_date"); sdStr != "" {
		if t, err := time.Parse("2006-01-02", sdStr); err == nil {
			start = t
		}
	}
	if edStr := r.URL.Query().Get("end_date"); edStr != "" {
		if t, err := time.Parse("2006-01-02", edStr); err == nil {
			end = t
		}
	}
	if start.IsZero() || end.IsZero() {
		month := int(now.Month())
		year := now.Year()
		if m, err := strconv.Atoi(r.URL.Query().Get("month")); err == nil && m >= 1 && m <= 12 {
			month = m
		}
		if y, err := strconv.Atoi(r.URL.Query().Get("year")); err == nil && y > 2000 {
			year = y
		}
		start = time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 1, -1)
	}

	// Providers that report a weekly meeting pattern instead of dated calendar
	// events have their pattern expanded across the requested range. Canvas has
	// real events, so it is read straight from the calendar endpoint.
	if !isCanvasProvider(provider) {
		reader, err := srv.newLiveProgramProvider(provider)
		if err != nil {
			return newInvalidIdServiceError(err, "class_id")
		}
		course, _, err := reader.GetCourse(r.Context(), rawCourseID)
		if err != nil {
			return newInternalServerServiceError(err, "failed to fetch course from "+providerDisplayName(provider))
		}
		args := getQueryContext(r)
		facilityID := facilityIDFromID
		if facilityID == 0 {
			facilityID = args.FacilityID
		}
		events := expandWeeklySchedule(course, start, end, srv.facilityLocation(facilityID))
		args.Total = int64(len(events))
		return writePaginatedResponse(w, http.StatusOK, events, args.IntoMeta())
	}

	fetchURL := fmt.Sprintf(
		"%s/api/v1/calendar_events?context_codes[]=course_%d&start_date=%s&end_date=%s&per_page=100",
		provider.BaseURL, rawCourseID,
		start.Format("2006-01-02"), end.Format("2006-01-02"),
	)

	raw, err := srv.fetchAllCanvasPages(r.Context(), provider, fetchURL, 0)
	if err != nil {
		return newInternalServerServiceError(err, "failed to fetch canvas events")
	}

	courseTimezone := srv.fetchCanvasCourseTimezone(provider, rawCourseID)

	events := make([]canvasScheduleEvent, 0, len(raw))
	for _, ev := range raw {
		startAtStr, _ := ev["start_at"].(string)
		endAtStr, _ := ev["end_at"].(string)
		if startAtStr == "" || endAtStr == "" {
			continue
		}
		startAt, err := time.Parse(time.RFC3339, startAtStr)
		if err != nil {
			continue
		}
		endAt, err := time.Parse(time.RFC3339, endAtStr)
		if err != nil {
			continue
		}
		title, _ := ev["title"].(string)
		isCancelled := ev["workflow_state"] == "deleted"
		var id uint
		if idFloat, ok := ev["id"].(float64); ok {
			id = encodeCanvasClassID(providerID, uint(idFloat))
		}
		events = append(events, canvasScheduleEvent{
			ID:          id,
			Title:       title,
			StartAt:     startAt,
			EndAt:       endAt,
			IsCancelled: isCancelled,
			Timezone:    courseTimezone,
		})
	}

	args := getQueryContext(r)
	args.Total = int64(len(events))
	return writePaginatedResponse(w, http.StatusOK, events, args.IntoMeta())
}

// canvasEnrollmentRow mirrors EnrollmentDetails JSON shape so the
// frontend's ClassEnrollment type maps correctly.
type canvasEnrollmentRow struct {
	ProgramClassEnrollment
	NameFull     string `json:"name_full"`
	DocID        string `json:"doc_id"`
	ClassName    string `json:"class_name"`
	StartDt      string `json:"start_dt"`
	CompletionDt string `json:"completion_dt"`
}

func (srv *server) handleGetCanvasClassEnrollments(w http.ResponseWriter, r *http.Request, log sLog, classID uint) error {
	facilityIDFromID, providerID, rawCourseID := resolveCanvasClassParts(classID)
	provider, err := srv.providerByID(r.Context(), providerID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if !isLiveProgramProvider(provider) {
		return newInvalidIdServiceError(fmt.Errorf("provider %d is not a live program provider", providerID), "class ID")
	}
	claims := claimsFrom(r)
	if !claims.HasFeature(features.ProviderPlatforms) {
		return newInvalidIdServiceError(fmt.Errorf("provider access disabled for provider %d", providerID), "class ID")
	}

	// Canvas reports when each student joined, in the same call that lists them.
	// Other live providers only report the roster, so EnrolledAt stays nil rather
	// than being invented.
	var (
		canvasUserIDs   []string
		enrollmentDates map[string]*time.Time
	)
	if isCanvasProvider(provider) {
		canvasUserIDs, enrollmentDates, err = srv.fetchCanvasCourseEnrollmentData(provider, rawCourseID)
	} else {
		canvasUserIDs, err = srv.providerCourseEnrolleeIDs(provider, rawCourseID)
	}
	if err != nil {
		return newInternalServerServiceError(err, "failed to fetch enrollments from "+providerDisplayName(provider))
	}

	facilityID := facilityIDFromID
	if facilityID == 0 {
		facilityID = getQueryContext(r).FacilityID
	}
	userMap, err := srv.fetchCanvasMappedUsers(providerID, canvasUserIDs, facilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	rows := make([]canvasEnrollmentRow, 0, len(canvasUserIDs))
	for i, externalID := range canvasUserIDs {
		info, matched := userMap[externalID]
		if !matched {
			continue
		}
		rows = append(rows, canvasEnrollmentRow{
			ProgramClassEnrollment: ProgramClassEnrollment{
				DatabaseFields:   DatabaseFields{ID: uint(i + 1)},
				CohortID:         classID,
				UserID:           info.UserID,
				EnrollmentStatus: Enrolled,
				EnrolledAt:       enrollmentDates[externalID],
			},
			NameFull:  info.NameFirst + " " + info.NameLast,
			DocID:     info.DocID,
			ClassName: "",
		})
	}

	args := getQueryContext(r)
	args.Total = int64(len(rows))
	return writePaginatedResponse(w, http.StatusOK, rows, args.IntoMeta())
}

// handleGetCanvasAtRiskStudents calls the Canvas student_summaries analytics
// endpoint and returns AttendanceFlag entries for students showing low engagement.
// A student is flagged if they trigger at least two of:
//   - page_views == 0 (never logged in)
//   - participations == 0 with assignments present (no submissions)
//   - missing / total > 0.30 (>30 % of assignments not submitted)
func (srv *server) handleGetCanvasAtRiskStudents(w http.ResponseWriter, r *http.Request, classID uint) error {
	facilityIDFromID, providerID, rawCourseID := resolveCanvasClassParts(classID)
	provider, err := srv.providerByID(r.Context(), providerID)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if !isLiveProgramProvider(provider) {
		return newInvalidIdServiceError(fmt.Errorf("provider %d is not a live program provider", providerID), "class ID")
	}
	claims := claimsFrom(r)
	if !claims.HasFeature(features.ProviderPlatforms) {
		return newInvalidIdServiceError(fmt.Errorf("provider access disabled for provider %d", providerID), "class ID")
	}

	// Course analytics are Canvas-only; other live providers report no engagement
	// data, so there is nothing to flag rather than an error to report.
	if !isCanvasProvider(provider) {
		args := getQueryContext(r)
		args.Total = 0
		return writePaginatedResponse(w, http.StatusOK, []AttendanceFlag{}, args.IntoMeta())
	}

	summaryURL := fmt.Sprintf("%s/api/v1/courses/%d/analytics/student_summaries", provider.BaseURL, rawCourseID)
	summaries, err := srv.fetchAllCanvasPages(r.Context(), provider, summaryURL, 0)
	if err != nil {
		// Analytics may not be enabled on this Canvas instance — return empty rather than error.
		log.WithError(err).Warnf("canvas student_summaries unavailable for course %d, returning empty at-risk list", rawCourseID)
		args := getQueryContext(r)
		args.Total = 0
		return writePaginatedResponse(w, http.StatusOK, []AttendanceFlag{}, args.IntoMeta())
	}

	canvasUserIDs := make([]string, 0, len(summaries))
	for _, s := range summaries {
		if id, ok := s["id"].(float64); ok {
			canvasUserIDs = append(canvasUserIDs, fmt.Sprintf("%d", int(id)))
		}
	}

	facilityID := facilityIDFromID
	if facilityID == 0 {
		facilityID = getQueryContext(r).FacilityID
	}
	userMap, err := srv.fetchCanvasMappedUsers(providerID, canvasUserIDs, facilityID)
	if err != nil {
		return newDatabaseServiceError(err)
	}

	flags := make([]AttendanceFlag, 0)
	for _, s := range summaries {
		canvasIDStr := ""
		if id, ok := s["id"].(float64); ok {
			canvasIDStr = fmt.Sprintf("%d", int(id))
		}
		info, matched := userMap[canvasIDStr]
		if !matched {
			continue
		}

		pageViews, participations := 0, 0
		if v, ok := s["page_views"].(float64); ok {
			pageViews = int(v)
		}
		if v, ok := s["participations"].(float64); ok {
			participations = int(v)
		}

		total, missing, onTime, late := 0, 0, 0, 0
		if td, ok := s["tardiness_breakdown"].(map[string]interface{}); ok {
			if v, ok := td["total"].(float64); ok {
				total = int(v)
			}
			if v, ok := td["missing"].(float64); ok {
				missing = int(v)
			}
			if v, ok := td["on_time"].(float64); ok {
				onTime = int(v)
			}
			if v, ok := td["late"].(float64); ok {
				late = int(v)
			}
		}

		riskScore := 0
		if pageViews == 0 {
			riskScore++
		}
		if total > 0 && participations == 0 {
			riskScore++
		}
		if total > 0 && float64(missing)/float64(total) > 0.30 {
			riskScore++
		}
		if riskScore < 2 {
			continue
		}

		submitted := onTime + late
		attendanceRate := 0
		if total > 0 {
			attendanceRate = submitted * 100 / total
		}
		flagType := MultipleAbsences
		if pageViews == 0 || participations == 0 {
			flagType = NoAttendance
		}

		flags = append(flags, AttendanceFlag{
			NameFirst:           info.NameFirst,
			NameLast:            info.NameLast,
			DocID:               info.DocID,
			FlagType:            flagType,
			UserID:              info.UserID,
			TotalSessions:       total,
			AttendedSessions:    submitted,
			MissedSessions:      missing,
			AttendanceRate:      attendanceRate,
			ConsecutiveAbsences: 0,
		})
	}

	args := getQueryContext(r)
	args.Total = int64(len(flags))
	return writePaginatedResponse(w, http.StatusOK, flags, args.IntoMeta())
}
