package programs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"
)

// A live provider is one whose courses are read over HTTP on request and projected
// as synthetic programs and classes, rather than synced into our tables. Canvas is
// the first; the seams here exist so a second provider can differ in the two ways
// that actually matter -- how it authenticates, and how it pages -- without
// duplicating the handler layer.
//
// Synthetic IDs are deliberately not part of this: encodeCanvasClassID and friends
// key off provider_platforms.id, never the provider type, so every live provider
// already gets a disjoint ID range for free.

// providerTransport is the per-provider HTTP seam. Canvas authenticates with a
// bearer token and pages via RFC-5988 Link headers; other providers set a
// different header and page by other means.
type providerTransport interface {
	// authorize applies whatever credential scheme the provider uses.
	authorize(req *http.Request, provider *ppapi.Provider)

	// fetchAll retrieves every page starting at startURL and returns the combined
	// records. max limits the total returned; 0 means fetch everything.
	fetchAll(ctx context.Context, provider *ppapi.Provider, startURL string, max int) ([]map[string]interface{}, error)

	// fetchOne retrieves a single JSON object, for endpoints that return a bare
	// record rather than a collection.
	fetchOne(ctx context.Context, provider *ppapi.Provider, url string) (map[string]interface{}, error)
}

// requireHTTPS reports whether a URL is safe to send a provider's access key to.
// The handlers reject a non-https base URL on the way in; this is the backstop
// for rows that predate that check and for a paging link that points somewhere
// the provider chose.
func requireHTTPS(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("parsing provider url: %w", err)
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("provider url must use https, got %q", parsed.Scheme)
	}
	return nil
}

// canvasTransport implements providerTransport for Canvas.
type canvasTransport struct {
	srv *server
}

func (t canvasTransport) authorize(req *http.Request, provider *ppapi.Provider) {
	req.Header.Add("Authorization", "Bearer "+provider.AccessKey)
	req.Header.Add("Accept", "application/json")
}

func (t canvasTransport) fetchAll(ctx context.Context, provider *ppapi.Provider, startURL string, max int) ([]map[string]interface{}, error) {
	return t.srv.fetchAllCanvasPages(ctx, provider, startURL, max)
}

func (t canvasTransport) fetchOne(ctx context.Context, provider *ppapi.Provider, url string) (map[string]interface{}, error) {
	if err := requireHTTPS(url); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
		return nil, fmt.Errorf("canvas returned %d", resp.StatusCode)
	}
	var record map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, err
	}
	return record, nil
}

// maxRawCourseID is the largest provider course ID the synthetic class ID
// encoding can carry. encodeCanvasClassID packs the course into the low six
// digits, so an ID at or above this overflows into the provider field and the
// class would silently be attributed to a different provider. Callers skip such
// courses rather than emitting a corrupt ID.
const maxRawCourseID = 999_999

// courseIDFitsEncoding reports whether a provider course ID can be encoded
// without colliding with another provider's range.
func courseIDFitsEncoding(rawCourseID uint) bool {
	return rawCourseID <= maxRawCourseID
}

// liveCourseList is what a provider reports for one read of its course listing.
type liveCourseList struct {
	// Courses are the records that parsed successfully.
	Courses []liveCourse
	// Timezones maps a raw course ID to its IANA timezone, for the courses where
	// the provider reports one.
	Timezones map[uint]string
	// Total is how many course records the provider returned, including any that
	// could not be parsed. Class counts are reported against this, not against
	// len(Courses), so a malformed record still shows up in the total.
	Total int
}

// LiveProgramProvider reads a provider's courses on request. Implementations are
// obtained through newLiveProgramProvider, which fails for provider types that
// are not read this way.
type LiveProgramProvider interface {
	// ListCourses returns every course this provider platform exposes.
	ListCourses(ctx context.Context) (liveCourseList, error)

	// ListCourseEnrollees returns the provider's own user IDs for the students in
	// a course. Only called when a liveCourse did not already carry them.
	ListCourseEnrollees(ctx context.Context, rawCourseID uint) ([]string, error)

	// GetCourse returns one course by the provider's own course ID, along with
	// its IANA timezone where the provider reports one.
	GetCourse(ctx context.Context, rawCourseID uint) (liveCourse, string, error)
}

// courseEnrollees returns the enrollee IDs for a course, using the ones the
// listing already supplied when the provider reports them inline and falling
// back to a per-course request when it does not.
func courseEnrollees(ctx context.Context, reader LiveProgramProvider, course liveCourse) ([]string, error) {
	if course.enrolleeIDs != nil {
		return course.enrolleeIDs, nil
	}
	return reader.ListCourseEnrollees(ctx, course.rawID)
}

// canvasProvider is the Canvas implementation of LiveProgramProvider.
type canvasProvider struct {
	srv       *server
	provider  *ppapi.Provider
	transport providerTransport
}

func (p canvasProvider) ListCourses(ctx context.Context) (liveCourseList, error) {
	raw, err := p.transport.fetchAll(ctx, p.provider, canvasAccountCoursesURL(p.provider), 0)
	if err != nil {
		return liveCourseList{}, fmt.Errorf("canvas API error for provider %d: %w", p.provider.ID, err)
	}
	now := time.Now()
	courses := make([]liveCourse, 0, len(raw))
	for _, record := range raw {
		course, ok := parseCanvasCourse(record, p.provider.ID, now)
		if !ok {
			continue
		}
		courses = append(courses, course)
	}
	return liveCourseList{
		Courses:   courses,
		Timezones: buildCourseTimezoneMap(raw),
		Total:     len(raw),
	}, nil
}

// GetCourse reads the single-course endpoint. Canvas reports a per-course
// timezone, which the class detail page uses to render schedule times.
func (p canvasProvider) GetCourse(ctx context.Context, rawCourseID uint) (liveCourse, string, error) {
	record, err := p.transport.fetchOne(ctx, p.provider, canvasCourseURL(p.provider, rawCourseID))
	if err != nil {
		return liveCourse{}, "", fmt.Errorf("fetching canvas course %d: %w", rawCourseID, err)
	}
	course, ok := parseCanvasCourse(record, p.provider.ID, time.Now())
	if !ok {
		return liveCourse{}, "", fmt.Errorf("canvas course %d could not be parsed", rawCourseID)
	}
	timezone, _ := record["time_zone"].(string)
	return course, timezone, nil
}

func (p canvasProvider) ListCourseEnrollees(ctx context.Context, rawCourseID uint) ([]string, error) {
	return p.srv.fetchCanvasCourseEnrolleeIDs(p.provider, rawCourseID)
}

// newLiveProgramProvider returns the live reader for a provider platform, or an
// error when the type is not read live.
func (srv *server) newLiveProgramProvider(provider *ppapi.Provider) (LiveProgramProvider, error) {
	transport, err := srv.transportFor(provider)
	if err != nil {
		return nil, err
	}
	if provider.Type == ppapi.EssentialEd {
		return essentialEdProvider{srv: srv, provider: provider, transport: transport}, nil
	}
	return canvasProvider{srv: srv, provider: provider, transport: transport}, nil
}

// canvasAccountCoursesURL is the account course listing every Canvas read path
// starts from. It was copied inline in seven places before this existed.
func canvasAccountCoursesURL(provider *ppapi.Provider) string {
	return provider.BaseURL + "/api/v1/accounts/" + provider.AccountID + "/courses?per_page=100"
}

// canvasCourseURL is the single-course endpoint.
func canvasCourseURL(provider *ppapi.Provider, rawCourseID uint) string {
	return fmt.Sprintf("%s/api/v1/courses/%d", provider.BaseURL, rawCourseID)
}

// liveUserLister is implemented by live providers that can list their own user
// accounts directly. Providers that do not implement it (Canvas) continue to be
// served by Provider Platforms' worker over HTTP.
type liveUserLister interface {
	ListUsers(ctx context.Context) ([]module.ProviderUser, error)
}

// liveUserListerFor returns the provider's own user lister when it has one.
func (srv *server) liveUserListerFor(provider *ppapi.Provider) (liveUserLister, bool) {
	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		return nil, false
	}
	lister, ok := reader.(liveUserLister)
	return lister, ok
}

// liveUserCourseLister is implemented by live providers that can report the
// courses one of their users is enrolled in. Canvas is not: it has a dedicated
// per-user endpoint, served by fetchCanvasCoursesForUser.
type liveUserCourseLister interface {
	ListUserCourses(ctx context.Context, externalUserID string) ([]liveCourse, error)
}

// providerByID is the provider platform with id, from Provider Platforms. A
// missing one is the zero Provider, which isn't live.
func (srv *server) providerByID(ctx context.Context, id uint) (*ppapi.Provider, error) {
	provider, err := srv.providerPlatforms.Provider(ctx, id)
	if err != nil {
		return nil, newDBError(err, "provider_platforms not found")
	}
	return &provider, nil
}

// activeProviders is every enabled provider platform, from Provider Platforms.
func (srv *server) activeProviders(ctx context.Context) ([]ppapi.Provider, error) {
	providers, err := srv.providerPlatforms.ActiveProviders(ctx)
	if err != nil {
		return nil, newDBError(err, "error getting provider_platforms")
	}
	return providers, nil
}

// providerCourseEnrolleeIDs returns the provider's own student IDs for a course,
// dispatching on provider type. This is what the enrollee counters use, so they
// work for any live provider rather than only Canvas.
func (srv *server) providerCourseEnrolleeIDs(provider *ppapi.Provider, rawCourseID uint) ([]string, error) {
	reader, err := srv.newLiveProgramProvider(provider)
	if err != nil {
		return nil, err
	}
	return reader.ListCourseEnrollees(context.Background(), rawCourseID)
}

// providerSourceLabel is the value the frontend uses to tell where a synthetic
// program came from. It is sent as ProgramsOverviewTable.Source.
func providerSourceLabel(p *ppapi.Provider) string {
	if p.Type == ppapi.EssentialEd {
		return "essential_ed"
	}
	return "canvas"
}

// providerDisplayName is the human-readable provider name used in descriptions.
func providerDisplayName(p *ppapi.Provider) string {
	if p.Type == ppapi.EssentialEd {
		return "Essential Education"
	}
	return "Canvas"
}

// isLiveProgramProvider reports whether this provider platform is read live and
// projected as synthetic programs. Prefer this over isCanvasProvider at call
// sites that only mean "is this one of the live providers".
func isLiveProgramProvider(p *ppapi.Provider) bool {
	return isCanvasProvider(p) || p.Type == ppapi.EssentialEd
}

// transportFor returns the HTTP seam for a provider platform, or an error when
// the type is not a live provider.
func (srv *server) transportFor(provider *ppapi.Provider) (providerTransport, error) {
	switch {
	case isCanvasProvider(provider):
		return canvasTransport{srv: srv}, nil
	case provider.Type == ppapi.EssentialEd:
		return essentialEdTransport{srv: srv}, nil
	}
	return nil, fmt.Errorf("provider %d (%s) is not a live program provider", provider.ID, provider.Type)
}
