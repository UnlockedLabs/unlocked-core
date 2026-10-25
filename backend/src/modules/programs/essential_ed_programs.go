package programs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"

	log "github.com/sirupsen/logrus"
)

// Essential Education's API differs from Canvas in every mechanical respect:
// a token header instead of bearer auth, a required User-Agent, a {records,
// at_end} envelope instead of a bare array, offset paging instead of Link
// headers, and space-separated UTC datetimes. The provider platform's
// account_id holds the district ID; classes are read across the whole district
// rather than per school.

const (
	// essentialEdPageSize is the API's documented maximum per request.
	essentialEdPageSize = 100

	// essentialEdMaxPages bounds the paging loop. at_end is what normally stops
	// it; this only prevents an unbounded loop if the API never sets it.
	essentialEdMaxPages = 500

	// essentialEdTimeFormat is the datetime format used throughout the API.
	// Values are UTC. Note this is not RFC3339.
	essentialEdTimeFormat = "2006-01-02 15:04:05"

	// essentialEdUserAgent identifies us. The API rejects requests without a
	// User-Agent, and does so with an HTML body rather than JSON.
	essentialEdUserAgent = "UnlockEdv2"
)

// essentialEdTransport implements providerTransport for Essential Education.
type essentialEdTransport struct {
	srv *server
}

func (t essentialEdTransport) authorize(req *http.Request, provider *ppapi.Provider) {
	req.Header.Set("X-API-Token", provider.AccessKey)
	req.Header.Set("Accept", "application/json")
	// Required. Without it the API returns an HTML error page, which then fails
	// to decode with a message that says nothing about the missing header.
	req.Header.Set("User-Agent", essentialEdUserAgent)
}

// fetchAll walks the start/limit pages until the API reports at_end.
func (t essentialEdTransport) fetchAll(ctx context.Context, provider *ppapi.Provider, startURL string, max int) ([]map[string]interface{}, error) {
	parsed, err := url.Parse(startURL)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()

	var all []map[string]interface{}
	for page := 0; page < essentialEdMaxPages; page++ {
		query.Set("start", strconv.Itoa(len(all)))
		query.Set("limit", strconv.Itoa(essentialEdPageSize))
		parsed.RawQuery = query.Encode()

		envelope, err := t.fetchPage(ctx, provider, parsed.String())
		if err != nil {
			return nil, err
		}
		all = append(all, envelope.Records...)

		// An empty page also terminates: without it, an at_end that never flips
		// would spin until the page cap.
		if envelope.AtEnd || len(envelope.Records) == 0 {
			break
		}
		if max > 0 && len(all) >= max {
			break
		}
	}
	return all, nil
}

// essentialEdPage is the paginated response envelope.
type essentialEdPage struct {
	Records []map[string]interface{} `json:"records"`
	AtEnd   bool                     `json:"at_end"`
}

func (t essentialEdTransport) fetchPage(ctx context.Context, provider *ppapi.Provider, pageURL string) (essentialEdPage, error) {
	var envelope essentialEdPage
	if err := requireHTTPS(pageURL); err != nil {
		return envelope, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return envelope, err
	}
	t.authorize(req, provider)
	resp, err := t.srv.Client.Do(req)
	if err != nil {
		return envelope, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return envelope, fmt.Errorf("essential ed API returned %d: %s", resp.StatusCode, readErrorBody(resp))
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return envelope, fmt.Errorf("decoding essential ed response: %w", err)
	}
	return envelope, nil
}

func (t essentialEdTransport) fetchOne(ctx context.Context, provider *ppapi.Provider, recordURL string) (map[string]interface{}, error) {
	if err := requireHTTPS(recordURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, recordURL, nil)
	if err != nil {
		return nil, err
	}
	t.authorize(req, provider)
	resp, err := t.srv.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("essential ed API returned %d: %s", resp.StatusCode, readErrorBody(resp))
	}
	var record map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, err
	}
	return record, nil
}

// essentialEdClassesURL is the district-wide class listing. The token's scope
// determines which schools are included, so no school filter is applied.
func essentialEdClassesURL(provider *ppapi.Provider) string {
	return strings.TrimSuffix(provider.BaseURL, "/") + "/classes"
}

// essentialEdProvider is the Essential Education implementation of LiveProgramProvider.
type essentialEdProvider struct {
	srv       *server
	provider  *ppapi.Provider
	transport providerTransport
}

func (p essentialEdProvider) ListCourses(ctx context.Context) (liveCourseList, error) {
	raw, err := p.transport.fetchAll(ctx, p.provider, essentialEdClassesURL(p.provider), 0)
	if err != nil {
		return liveCourseList{}, fmt.Errorf("essential ed API error for provider %d: %w", p.provider.ID, err)
	}
	now := time.Now()
	courses := make([]liveCourse, 0, len(raw))
	for _, record := range raw {
		course, ok := parseEssentialEdClass(record, p.provider.ID, now)
		if !ok {
			continue
		}
		courses = append(courses, course)
	}
	return liveCourseList{
		Courses: courses,
		// Essential Ed reports no per-class timezone; callers fall back to the
		// facility default.
		Timezones: map[uint]string{},
		Total:     len(raw),
	}, nil
}

// ListCourseEnrollees re-reads the class listing to find one class's students.
// Callers that already hold a liveCourse should go through courseEnrollees
// instead, which uses the inline enrolleeIDs the listing supplied and costs no
// request; this is only for the paths that have nothing but a course ID.
func (p essentialEdProvider) ListCourseEnrollees(ctx context.Context, rawCourseID uint) ([]string, error) {
	listing, err := p.ListCourses(ctx)
	if err != nil {
		return nil, err
	}
	for _, course := range listing.Courses {
		if course.rawID == rawCourseID {
			return course.enrolleeIDs, nil
		}
	}
	return nil, fmt.Errorf("essential ed class %d not found for provider %d", rawCourseID, p.provider.ID)
}

// GetCourse reads the class listing and picks out one class. Essential Ed has no
// documented single-class endpoint, and the listing is what carries the inline
// students, so re-reading it keeps the detail view identical to the list view.
// Essential Ed reports no per-class timezone.
func (p essentialEdProvider) GetCourse(ctx context.Context, rawCourseID uint) (liveCourse, string, error) {
	listing, err := p.ListCourses(ctx)
	if err != nil {
		return liveCourse{}, "", err
	}
	for _, course := range listing.Courses {
		if course.rawID == rawCourseID {
			return course, "", nil
		}
	}
	return liveCourse{}, "", fmt.Errorf("essential ed class %d not found for provider %d", rawCourseID, p.provider.ID)
}

// ListUserCourses returns the classes a student is enrolled in. The class
// listing carries its students inline, so this filters that rather than asking
// for a per-user endpoint Essential Ed does not have.
func (p essentialEdProvider) ListUserCourses(ctx context.Context, externalUserID string) ([]liveCourse, error) {
	listing, err := p.ListCourses(ctx)
	if err != nil {
		return nil, err
	}
	var enrolled []liveCourse
	for _, course := range listing.Courses {
		if slices.Contains(course.enrolleeIDs, externalUserID) {
			enrolled = append(enrolled, course)
		}
	}
	return enrolled, nil
}

// parseEssentialEdClass maps one class record onto liveCourse. Returns false for
// records that cannot be represented, rather than guessing.
func parseEssentialEdClass(record map[string]interface{}, providerID uint, now time.Time) (liveCourse, bool) {
	rawID, ok := essentialEdUint(record["id"])
	if !ok {
		return liveCourse{}, false
	}
	if !courseIDFitsEncoding(rawID) {
		log.Warnf("essential ed class %d exceeds the synthetic class ID range, skipping", rawID)
		return liveCourse{}, false
	}

	name, _ := record["name"].(string)
	startDt := essentialEdTime(record["open_date"])
	endDt := essentialEdTimePtr(record["close_date"])

	// A class is Completed when the provider has disabled it or its close date
	// has passed. Canvas has no active flag and infers status from dates alone.
	status := Active
	if active, known := essentialEdBool(record["active"]); known && !active {
		status = Completed
	} else if endDt != nil && !endDt.After(now) {
		status = Completed
	}

	return liveCourse{
		encodedID: encodeCanvasClassID(providerID, rawID),
		rawID:     rawID,
		name:      name,
		// No course_code analogue exists in the class record.
		description:    "",
		startDt:        startDt,
		endDt:          endDt,
		status:         status,
		enrolleeIDs:    essentialEdStudentIDs(record["students"]),
		weeklySchedule: essentialEdWeeklySchedule(record),
	}, true
}

// essentialEdScheduleFields maps the per-weekday class record fields onto the
// weekday they describe.
var essentialEdScheduleFields = map[string]time.Weekday{
	"schedule_sun": time.Sunday,
	"schedule_mon": time.Monday,
	"schedule_tue": time.Tuesday,
	"schedule_wed": time.Wednesday,
	"schedule_thu": time.Thursday,
	"schedule_fri": time.Friday,
	"schedule_sat": time.Saturday,
}

// essentialEdWeeklySchedule reads the recurring meeting pattern off a class
// record. Days with no parseable times are left out entirely, so an empty result
// means the class has no usable schedule rather than an empty week.
func essentialEdWeeklySchedule(record map[string]interface{}) map[time.Weekday][]meetingTime {
	schedule := make(map[time.Weekday][]meetingTime, len(essentialEdScheduleFields))
	for field, weekday := range essentialEdScheduleFields {
		text, _ := record[field].(string)
		if times := parseMeetingTimes(text); len(times) > 0 {
			schedule[weekday] = times
		}
	}
	if len(schedule) == 0 {
		return nil
	}
	return schedule
}

// meetingTimeSeparators are the ways instructors separate a start from an end.
// The field is free-form text, so the recommended "9:00 AM - 11:00 AM" is only
// the most likely of several spellings.
var meetingTimeSeparators = []string{"--", "-", "\u2013", "\u2014", " to ", " until "}

// parseMeetingTimes reads one day's free-form meeting times. The documented
// format is comma separated ranges with a hyphen between start and end, but the
// field is typed by instructors, so anything unparseable is skipped rather than
// guessed at.
func parseMeetingTimes(text string) []meetingTime {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var times []meetingTime
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		start, end, ok := splitMeetingRange(part)
		if !ok {
			continue
		}
		startMin, ok := parseClockMinutes(start)
		if !ok {
			continue
		}
		endMin, ok := parseClockMinutes(end)
		if !ok || endMin <= startMin {
			continue
		}
		times = append(times, meetingTime{startMin: startMin, endMin: endMin})
	}
	return times
}

// splitMeetingRange splits "9:00 AM - 11:00 AM" into its two halves.
func splitMeetingRange(part string) (string, string, bool) {
	// Lowercasing can change byte length for exotic runes; fall back to the
	// original string when it does, so the split indexes stay valid.
	lower := strings.ToLower(part)
	if len(lower) != len(part) {
		lower = part
	}
	for _, sep := range meetingTimeSeparators {
		if idx := strings.Index(lower, sep); idx > 0 {
			return part[:idx], part[idx+len(sep):], true
		}
	}
	return "", "", false
}

// parseClockMinutes reads a single clock time as minutes past midnight,
// accepting 12-hour times with a meridiem and bare 24-hour times.
func parseClockMinutes(value string) (int, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, ".", "")
	pm := strings.HasSuffix(value, "pm")
	am := strings.HasSuffix(value, "am")
	if pm || am {
		value = strings.TrimSpace(value[:len(value)-2])
	}
	hourStr, minStr, hasMinutes := strings.Cut(value, ":")
	hour, err := strconv.Atoi(strings.TrimSpace(hourStr))
	if err != nil {
		return 0, false
	}
	minute := 0
	if hasMinutes {
		if minute, err = strconv.Atoi(strings.TrimSpace(minStr)); err != nil {
			return 0, false
		}
	}
	if minute < 0 || minute > 59 {
		return 0, false
	}
	switch {
	case pm && hour != 12:
		hour += 12
	case am && hour == 12:
		hour = 0
	}
	if hour < 0 || hour > 23 {
		return 0, false
	}
	return hour*60 + minute, true
}

// essentialEdStudentIDs pulls the provider's student IDs out of the inline
// students array. Returns an empty (non-nil) slice when the key is present but
// empty, so callers can tell "no students" from "not supplied".
func essentialEdStudentIDs(value any) []string {
	students, ok := value.([]interface{})
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(students))
	for _, entry := range students {
		student, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := essentialEdUint(student["id"]); ok {
			ids = append(ids, strconv.FormatUint(uint64(id), 10))
		}
	}
	return ids
}

// essentialEdBool reads a boolean. The docs say responses always use 1/0, but
// the API returns real JSON booleans on some endpoints, so both are accepted.
// The second return distinguishes "false" from "absent".
func essentialEdBool(value any) (result bool, known bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case float64:
		return v != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "y", "yes", "t", "true":
			return true, true
		case "0", "n", "no", "f", "false":
			return false, true
		}
	}
	return false, false
}

// essentialEdUint reads an ID, which JSON decoding gives us as a float64.
func essentialEdUint(value any) (uint, bool) {
	switch v := value.(type) {
	case float64:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, false
		}
		return uint(parsed), true
	}
	return 0, false
}

// essentialEdTime parses a datetime, returning the zero time when absent or
// malformed. Values are UTC and use a space separator, not RFC3339.
func essentialEdTime(value any) time.Time {
	text, ok := value.(string)
	if !ok || text == "" {
		return time.Time{}
	}
	parsed, err := time.ParseInLocation(essentialEdTimeFormat, text, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// essentialEdTimePtr is essentialEdTime for optional fields, which arrive as
// JSON null. Returns nil when there is no usable value.
func essentialEdTimePtr(value any) *time.Time {
	parsed := essentialEdTime(value)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}

// essentialEdUsersURL is the district-wide user listing, narrowed to students.
// roles must be sent as an array parameter -- a bare roles=STUDENT is rejected
// with "The roles parameter must be an array."
func essentialEdUsersURL(provider *ppapi.Provider) string {
	query := url.Values{"roles[]": {"STUDENT"}}
	return strings.TrimSuffix(provider.BaseURL, "/") + "/users?" + query.Encode()
}

// ListUsers returns the district's student accounts. Implementing this lets the
// user-matching screen read Essential Ed directly instead of going through
// provider-middleware, which has no Essential Ed service.
func (p essentialEdProvider) ListUsers(ctx context.Context) ([]module.ProviderUser, error) {
	raw, err := p.transport.fetchAll(ctx, p.provider, essentialEdUsersURL(p.provider), 0)
	if err != nil {
		return nil, fmt.Errorf("essential ed users error for provider %d: %w", p.provider.ID, err)
	}
	users := make([]module.ProviderUser, 0, len(raw))
	for _, record := range raw {
		user, ok := parseEssentialEdUser(record)
		if !ok {
			continue
		}
		users = append(users, user)
	}
	return users, nil
}

// parseEssentialEdUser maps a user record onto ImportUser. Note the response
// field is email_address, not the "email" the published docs name.
func parseEssentialEdUser(record map[string]interface{}) (module.ProviderUser, bool) {
	id, ok := essentialEdUint(record["id"])
	if !ok {
		return module.ProviderUser{}, false
	}
	nameFirst, _ := record["first_name"].(string)
	nameLast, _ := record["last_name"].(string)
	email, _ := record["email_address"].(string)

	// Students authenticate with a username and often have no email; staff are
	// the reverse. Fall back so the matching screen always has a handle to show.
	username, _ := record["username"].(string)
	if username == "" {
		username = email
	}
	if username == "" {
		username = strconv.FormatUint(uint64(id), 10)
	}

	return module.ProviderUser{
		ExternalUserID:   strconv.FormatUint(uint64(id), 10),
		ExternalUsername: username,
		Username:         username,
		NameFirst:        nameFirst,
		NameLast:         nameLast,
		Email:            email,
	}, true
}

// readErrorBody returns a short excerpt of an error response. Essential Ed
// explains 4xx failures in the body (for example "The roles parameter must be
// an array."), which the status code alone hides.
func readErrorBody(resp *http.Response) string {
	const limit = 512
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil || len(body) == 0 {
		return "no response body"
	}
	return strings.TrimSpace(string(body))
}
