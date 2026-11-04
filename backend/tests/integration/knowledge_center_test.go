package integration

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	ocapi "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/stretchr/testify/require"
)

// The Knowledge Center page asks Open Content about the facility and dates it
// shows, and passes each answer through. Open Content's own tests check the
// numbers themselves (EN-205).
func TestKnowledgeCenterMetrics(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("KC Facility")
	require.NoError(t, err)

	repeat := ocapi.RepeatEngagement{Once: 2, TwoToFour: 1, FivePlus: 0}
	categories := []ocapi.CategoryViews{{Category: "Vocational", Views: 2}, {Category: "Life Skills", Views: 1}}
	libraries := []ocapi.ContentVisits{{Title: "Career Library", Visits: 2, Change: 100}, {Title: "Recovery Library", Visits: 1, Change: 0}}
	videos := []ocapi.ContentVisits{{Title: "Welding Basics", Visits: 2, Change: -33}}
	env.OpenContent.set(func(f *fakeOpenContent) {
		f.interactions = ocapi.Interactions{Total: 5, Unique: 3, Change: 25}
		f.avgSessionMinutes = 15
		f.repeat = repeat
		f.categories = categories
		f.topLibraries = libraries
		f.topVideos = videos
	})

	metrics := NewRequest[models.KnowledgeCenterMetrics](env.Client, t, http.MethodGet,
		"/api/department-metrics/knowledge-center?facility="+strconv.Itoa(int(facility.ID))+
			"&start_date=2026-01-15&end_date=2026-01-16", nil).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, FacilityID: facility.ID}).
		Do().
		ExpectStatus(http.StatusOK).
		GetData()

	require.Equal(t, int64(5), metrics.TotalInteractions)
	require.Equal(t, 25, metrics.TotalInteractionsChange)
	require.Equal(t, int64(3), metrics.UniqueResidents)
	require.InDelta(t, 15.0, metrics.AvgSessionMinutes, 0.01)
	require.Equal(t, repeat, metrics.RepeatEngagement)
	require.Equal(t, categories, metrics.LibraryViewsByCategory)
	require.Equal(t, libraries, metrics.TopLibraries)
	require.Equal(t, videos, metrics.TopVideos)

	// Every question is about the page's facility and its two days, the end
	// date included.
	env.OpenContent.mu.Lock()
	defer env.OpenContent.mu.Unlock()
	require.Len(t, env.OpenContent.windows, 6)
	for _, w := range env.OpenContent.windows {
		require.NotNil(t, w.FacilityID)
		require.Equal(t, facility.ID, *w.FacilityID)
		require.True(t, w.Start.Equal(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)), "start %s", w.Start)
		require.True(t, w.End.Equal(time.Date(2026, 1, 17, 0, 0, 0, 0, time.UTC)), "end %s", w.End)
	}
}
