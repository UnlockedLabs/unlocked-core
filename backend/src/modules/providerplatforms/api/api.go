// Package api is the only part of providerplatforms that core and other
// feature modules may import: the questions Provider Platforms answers for
// them.
//
// The JSON names match what core already sends, so a response built from
// these types doesn't change.
//
// Planned (EN-186): Canvas and Essential Education classes for Programs
// (ListExternalClasses).
package api

import (
	"context"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"
)

// ProviderPlatforms answers core's questions about LMS connections, the users
// linked to them, and their learning data.
type ProviderPlatforms interface {
	// Provider is the provider platform with id. A missing one is the zero
	// Provider, not an error, so callers check its Type.
	Provider(ctx context.Context, id uint) (Provider, error)
	// ActiveProviders is every enabled provider platform.
	ActiveProviders(ctx context.Context) ([]Provider, error)

	// UserMappings is every provider account linked to the user.
	UserMappings(ctx context.Context, userID uint) ([]Mapping, error)
	// MappedUserIDs is the users linked to an account at the provider.
	MappedUserIDs(ctx context.Context, providerID uint) ([]uint, error)
	// DeleteUserMappings unlinks every provider account from the user. Call it
	// through registry.ProviderPlatformsInTx to make it part of core's
	// transaction.
	DeleteUserMappings(ctx context.Context, userID uint) error

	// The users linked to the given accounts at a provider, for Programs'
	// Canvas and Essential Education screens. MappedUsers
	// has only those at facilityID when it isn't zero, CountMappedAtFacility
	// counts only those at facilityID, and CountMappedPerFacility counts them
	// by facility ID.
	MappedUsers(ctx context.Context, providerID uint, externalUserIDs []string, facilityID uint) ([]MappedUser, error)
	CountMapped(ctx context.Context, providerID uint, externalUserIDs []string) (int64, error)
	CountMappedAtFacility(ctx context.Context, providerID uint, externalUserIDs []string, facilityID uint) (int64, error)
	CountMappedPerFacility(ctx context.Context, providerID uint, externalUserIDs []string) (map[uint]int64, error)

	// AddUserToProviders creates the user's account at each provider and links
	// it. It returns an error only when a provider can't be read; a failure at
	// one provider is logged and the rest still run.
	AddUserToProviders(ctx context.Context, user module.User, providerIDs []uint) error
	// AddUserToKolibri creates the user's Kolibri account and links it. It
	// fails when there is no Kolibri provider.
	AddUserToKolibri(ctx context.Context, user module.User) error
	// SetupKolibri gives the Kolibri set by KOLIBRI_URL a provider platform, if
	// it has none, and an OIDC client. Core calls it once, when it creates the
	// first admin. It does nothing when KOLIBRI_URL is empty.
	SetupKolibri(ctx context.Context) error

	// LearningStats is the course numbers on the admin dashboard, for
	// facilityID (every facility when nil). Insights always covers every
	// facility.
	LearningStats(ctx context.Context, facilityID *uint) (LearningStats, error)

	// LastDemoSeed is when SeedDemoLearning last ran. found is false when it
	// never has.
	LastDemoSeed(ctx context.Context) (last time.Time, found bool, err error)
	// ResetDemoLearning deletes the learning data made on or before the given
	// day.
	ResetDemoLearning(ctx context.Context, through time.Time) error
	// SeedDemoLearning enrolls each user in half the courses, at random, and
	// adds made-up activity, milestones and outcomes.
	SeedDemoLearning(ctx context.Context, userIDs []uint) error
}

// ProviderType is the kind of LMS a provider platform is. The values are
// stored in the database, so they never change.
type ProviderType string

// Provider types.
const (
	CanvasOSS   ProviderType = "canvas_oss"
	CanvasCloud ProviderType = "canvas_cloud"
	Kolibri     ProviderType = "kolibri"
	Brightspace ProviderType = "brightspace"
	EssentialEd ProviderType = "essential_ed"
)

// Provider is a connection to one LMS.
type Provider struct {
	ID        uint
	Type      ProviderType
	Name      string
	BaseURL   string
	AccountID string
	// AccessKey is decrypted.
	AccessKey string
}

// Mapping links a user to their account at a provider.
type Mapping struct {
	ProviderID     uint
	ExternalUserID string
}

// MappedUser is a user linked to an account at a provider.
type MappedUser struct {
	ExternalUserID string
	UserID         uint
	NameFirst      string
	NameLast       string
	DocID          string
}

// LearningStats is the course numbers on the admin dashboard.
type LearningStats struct {
	TotalCoursesOffered   int64
	TotalStudentsEnrolled int64
	TotalHourlyActivity   int64
	Insights              []LearningInsight
}

// LearningInsight is one course's completion and activity.
type LearningInsight struct {
	CourseName             string  `json:"course_name"`
	TotalStudentsCompleted int64   `json:"total_students_completed"`
	TotalStudentsEnrolled  int64   `json:"total_students_enrolled"`
	CompletionRate         float32 `json:"completion_rate"`
	ActivityHours          float32 `json:"activity_hours"`
}
