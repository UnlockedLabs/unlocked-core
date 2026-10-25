package models

import (
	"time"

	ocapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/opencontent/api"
	ppapi "github.com/UnlockedLabs/unlocked-core/backend/src/modules/providerplatforms/api"
)

// KnowledgeCenterMetrics is the Knowledge Center page's numbers, from Open
// Content.
type KnowledgeCenterMetrics struct {
	TotalInteractions       int64                  `json:"total_interactions"`
	TotalInteractionsChange int                    `json:"total_interactions_change"`
	UniqueResidents         int64                  `json:"unique_residents"`
	AvgSessionMinutes       float64                `json:"avg_session_minutes"`
	RepeatEngagement        ocapi.RepeatEngagement `json:"repeat_engagement"`
	LibraryViewsByCategory  []ocapi.CategoryViews  `json:"library_views_by_category"`
	TopLibraries            []ocapi.ContentVisits  `json:"top_libraries"`
	TopVideos               []ocapi.ContentVisits  `json:"top_videos"`
}

type CachedDashboard[T any] struct {
	LastCache time.Time `json:"last_cache"`
	Data      T         `json:"data"`
}

// AdminLayer2Join is the course numbers on the admin dashboard.
type AdminLayer2Join struct {
	TotalCoursesOffered   int64                   `json:"total_courses_offered"`
	TotalStudentsEnrolled int64                   `json:"total_students_enrolled"`
	TotalHourlyActivity   int64                   `json:"total_hourly_activity"`
	LearningInsights      []ppapi.LearningInsight `json:"learning_insights"`
}
