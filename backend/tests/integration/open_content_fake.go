package integration

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	ocapi "github.com/UnlockedLabs/unlocked-contracts/opencontent"
)

// fakeOpenContent stands in for Open Content's service (EN-205). Core asks it
// over HTTP, through the same client and handler as in production, and it
// answers with what the test sets. Unset answers are empty, as a site with no
// Open Content data answers.
type fakeOpenContent struct {
	mu                sync.Mutex
	facilityContent   map[uint]ocapi.FacilityContent
	engagement        ocapi.ActivityEngagement
	interactions      ocapi.Interactions
	avgSessionMinutes float64
	repeat            ocapi.RepeatEngagement
	categories        []ocapi.CategoryViews
	topLibraries      []ocapi.ContentVisits
	topVideos         []ocapi.ContentVisits
	// windows are the windows core asked about, in order.
	windows []ocapi.Window
}

var _ ocapi.OpenContent = (*fakeOpenContent)(nil)

// fakeOpenContentToken is the site's secret in core's tests.
const fakeOpenContentToken = "test-token"

// startFakeOpenContent serves a fakeOpenContent and points OPEN_CONTENT_URL and
// OPEN_CONTENT_TOKEN at it, for the test server core builds next.
func startFakeOpenContent(t *testing.T) *fakeOpenContent {
	t.Helper()
	fake := &fakeOpenContent{}
	ts := httptest.NewServer(ocapi.NewHandler(fake, fakeOpenContentToken))
	t.Cleanup(ts.Close)
	t.Setenv("OPEN_CONTENT_URL", ts.URL)
	t.Setenv("OPEN_CONTENT_TOKEN", fakeOpenContentToken)
	return fake
}

// set changes the fake's answers.
func (f *fakeOpenContent) set(change func(f *fakeOpenContent)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeOpenContent) asked(w ocapi.Window) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windows = append(f.windows, w)
}

func (f *fakeOpenContent) EndActivity(context.Context, int64) {}

func (f *fakeOpenContent) TopLibraries(context.Context, uint, uint) ([]ocapi.ContentUsage, error) {
	return []ocapi.ContentUsage{}, nil
}

func (f *fakeOpenContent) RecentVideos(context.Context, uint) ([]ocapi.ContentUsage, error) {
	return []ocapi.ContentUsage{}, nil
}

func (f *fakeOpenContent) ResourcesAccessed(context.Context, uint) (int64, error) {
	return 0, nil
}

func (f *fakeOpenContent) ActivityEngagement(context.Context, uint) (ocapi.ActivityEngagement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.engagement, nil
}

func (f *fakeOpenContent) FacilityContent(_ context.Context, facilityIDs []uint) (map[uint]ocapi.FacilityContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	content := map[uint]ocapi.FacilityContent{}
	for _, id := range facilityIDs {
		if c, ok := f.facilityContent[id]; ok {
			content[id] = c
		}
	}
	return content, nil
}

func (f *fakeOpenContent) Interactions(_ context.Context, w ocapi.Window) (ocapi.Interactions, error) {
	f.asked(w)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interactions, nil
}

func (f *fakeOpenContent) AvgSessionMinutes(_ context.Context, w ocapi.Window) (float64, error) {
	f.asked(w)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.avgSessionMinutes, nil
}

func (f *fakeOpenContent) RepeatEngagement(_ context.Context, w ocapi.Window) (ocapi.RepeatEngagement, error) {
	f.asked(w)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.repeat, nil
}

func (f *fakeOpenContent) LibraryViewsByCategory(_ context.Context, w ocapi.Window) ([]ocapi.CategoryViews, error) {
	f.asked(w)
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.categories), nil
}

func (f *fakeOpenContent) TopLibraryVisits(_ context.Context, w ocapi.Window, _ int) ([]ocapi.ContentVisits, error) {
	f.asked(w)
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.topLibraries), nil
}

func (f *fakeOpenContent) TopVideoVisits(_ context.Context, w ocapi.Window, _ int) ([]ocapi.ContentVisits, error) {
	f.asked(w)
	f.mu.Lock()
	defer f.mu.Unlock()
	return orEmpty(f.topVideos), nil
}

func (f *fakeOpenContent) ResetDemoActivity(context.Context, time.Time) error { return nil }

func (f *fakeOpenContent) SeedDemoActivity(context.Context, uint, []uint) error { return nil }

// orEmpty is s, or an empty slice for nil, as Open Content answers.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
