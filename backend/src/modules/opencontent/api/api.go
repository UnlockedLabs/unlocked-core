// Package api is the only part of opencontent that core and other feature
// modules may import: the questions Open Content answers for them.
//
// The JSON names match what core already sends, so a response built from
// these types doesn't change.
package api

import (
	"context"
	"time"
)

// OpenContent answers core's questions about libraries, videos and reading
// activity.
type OpenContent interface {
	// EndActivity records that the user stopped viewing the content of an
	// activity. Core calls it when the user's tab says so or closes. It logs a
	// failure instead of returning it.
	EndActivity(ctx context.Context, activityID int64)

	// TopLibraries is a resident's five most-read libraries, with the
	// visibility at facilityID.
	TopLibraries(ctx context.Context, userID, facilityID uint) ([]ContentUsage, error)
	// RecentVideos is the five videos a resident watched most recently.
	RecentVideos(ctx context.Context, userID uint) ([]ContentUsage, error)
	// ResourcesAccessed counts the different libraries, videos and links a
	// resident has opened.
	ResourcesAccessed(ctx context.Context, userID uint) (int64, error)
	// ActivityEngagement is a resident's reading time, for their profile.
	ActivityEngagement(ctx context.Context, userID uint) (ActivityEngagement, error)

	// FacilityContent is the Open Content data each of facilityIDs holds, for
	// core's facility list and facility delete check. A facility that holds
	// none is left out.
	FacilityContent(ctx context.Context, facilityIDs []uint) (map[uint]FacilityContent, error)

	// The Knowledge Center numbers, for the activity between start and end
	// (all of it when either is nil) at facilityID (every facility when nil).
	// The change values compare with the window of the same length before
	// start.
	Interactions(ctx context.Context, w Window) (Interactions, error)
	AvgSessionMinutes(ctx context.Context, w Window) (float64, error)
	RepeatEngagement(ctx context.Context, w Window) (RepeatEngagement, error)
	LibraryViewsByCategory(ctx context.Context, w Window) ([]CategoryViews, error)
	TopLibraryVisits(ctx context.Context, w Window, limit int) ([]ContentVisits, error)
	TopVideoVisits(ctx context.Context, w Window, limit int) ([]ContentVisits, error)

	// ResetDemoActivity deletes demo reading activity from before the given
	// day.
	ResetDemoActivity(ctx context.Context, before time.Time) error
	// SeedDemoActivity adds six months of made-up reading activity for each
	// user, at facilityID.
	SeedDemoActivity(ctx context.Context, facilityID uint, userIDs []uint) error
}

// ContentUsage is one library or video a resident used, and for how long.
type ContentUsage struct {
	Title                 string `json:"title"`
	Url                   string `json:"url"`
	ThumbnailUrl          string `json:"thumbnail_url"`
	Description           string `json:"description,omitempty"`
	VisibilityStatus      *bool  `json:"visibility_status"`
	OpenContentProviderId uint   `json:"open_content_provider_id"`
	ContentId             uint   `json:"content_id"`
	ContentType           string `json:"content_type"`
	ProviderName          string `json:"provider_name,omitempty"`
	ChannelTitle          string `json:"channel_title,omitempty"`

	IsFeatured   bool    `json:"is_featured"`
	TotalHours   float64 `json:"total_hours"`
	TotalMinutes float64 `json:"total_minutes"`
}

// ActivityEngagement totals a resident's reading time.
type ActivityEngagement struct {
	// TotalActiveDaysMonthly is the days with any reading activity.
	TotalActiveDaysMonthly int64
	// TotalHoursActiveMonthly is the average activity's length, in hours.
	TotalHoursActiveMonthly float64
	// TotalHoursActiveWeekly and TotalMinutesActiveWeekly are all the
	// activity's time, divided by four.
	TotalHoursActiveWeekly   float64
	TotalMinutesActiveWeekly float64
	// TotalHoursEngaged and TotalMinutesEngaged are this week's activity time.
	TotalHoursEngaged   float64
	TotalMinutesEngaged float64
}

// FacilityContent counts the Open Content rows recorded at a facility.
type FacilityContent struct {
	Activities         int64
	Favorites          int64
	VisibilityStatuses int64
}

// Window is the activity a Knowledge Center number counts.
type Window struct {
	Start, End *time.Time
	FacilityID *uint
}

// Interactions counts reading activity in a window.
type Interactions struct {
	Total  int64
	Unique int64
	// Change is the percent change in Total from the window before.
	Change int
}

// RepeatEngagement counts residents by how often they read in a window.
type RepeatEngagement struct {
	Once      int64 `json:"once"`
	TwoToFour int64 `json:"two_to_four"`
	FivePlus  int64 `json:"five_plus"`
}

// CategoryViews counts library visits by category.
type CategoryViews struct {
	Category string `json:"category"`
	Views    int64  `json:"views"`
}

// ContentVisits counts visits to one library or video, and the percent
// change from the window before.
type ContentVisits struct {
	Title  string `json:"title"`
	Visits int64  `json:"visits"`
	Change int    `json:"change"`
}
