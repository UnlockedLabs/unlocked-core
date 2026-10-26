package programs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fakeScheduler records what a module schedules.
type fakeScheduler struct {
	specs   []string
	atStart int
}

func (s *fakeScheduler) Cron(spec string, task func()) error {
	s.specs = append(s.specs, spec)
	return nil
}

func (s *fakeScheduler) AtStart(task func()) error {
	s.atStart++
	return nil
}

// Class activation runs every morning at 5 and, outside dev, once at start.
func TestScheduleJobs(t *testing.T) {
	for _, dev := range []bool{false, true} {
		s := &fakeScheduler{}
		scheduleJobs(s, &Store{newTestDB(t)}, dev)
		assert.Equal(t, []string{"0 5 * * *"}, s.specs)
		wantAtStart := 1
		if dev {
			wantAtStart = 0
		}
		assert.Equal(t, wantAtStart, s.atStart, "dev=%v", dev)
	}
}

func createCohort(t *testing.T, db *gorm.DB, name string, status ClassStatus) ProgramClassCohort {
	t.Helper()
	program := Program{Name: name}
	require.NoError(t, db.Create(&program).Error)
	cohort := ProgramClassCohort{
		ProgramID:   program.ID,
		FacilityID:  1,
		Capacity:    10,
		Description: name,
		StartDt:     time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		Status:      status,
	}
	require.NoError(t, db.Create(&cohort).Error)
	return cohort
}

func createEnrollment(t *testing.T, db *gorm.DB, cohort ProgramClassCohort, userID uint, status ProgramEnrollmentStatus, enrolledAt *time.Time) ProgramClassEnrollment {
	t.Helper()
	e := ProgramClassEnrollment{CohortID: cohort.ID, UserID: userID, EnrollmentStatus: status, EnrolledAt: enrolledAt}
	require.NoError(t, db.Create(&e).Error)
	return e
}

// Activating classes makes them Active and gives their Enrolled enrollments
// with no enrolled date the activation time, all as the batch user. Other
// classes and enrollments are left alone.
func TestActivateCohorts(t *testing.T) {
	db := newTestDB(t)
	store := &Store{db}
	due := createCohort(t, db, "due", Scheduled)
	other := createCohort(t, db, "other", Scheduled)
	earlier := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	undated := createEnrollment(t, db, due, 1, Enrolled, nil)
	dated := createEnrollment(t, db, due, 2, Enrolled, &earlier)
	dropped := createEnrollment(t, db, due, 3, EnrollmentIncompleteDropped, nil)
	elsewhere := createEnrollment(t, db, other, 4, Enrolled, nil)
	const batchUserID = 99
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	ctx := identity.NewContext(context.Background(), &identity.Claims{UserID: batchUserID})
	require.NoError(t, store.ActivateCohorts(ctx, []int{int(due.ID)}, batchUserID, at))

	var got ProgramClassCohort
	require.NoError(t, db.First(&got, due.ID).Error)
	assert.Equal(t, Active, got.Status)
	require.NotNil(t, got.UpdateUserID)
	assert.Equal(t, uint(batchUserID), *got.UpdateUserID)
	var untouched ProgramClassCohort
	require.NoError(t, db.First(&untouched, other.ID).Error)
	assert.Equal(t, Scheduled, untouched.Status)

	enrolledAt := func(e ProgramClassEnrollment) *time.Time {
		var row ProgramClassEnrollment
		require.NoError(t, db.First(&row, e.ID).Error)
		return row.EnrolledAt
	}
	require.NotNil(t, enrolledAt(undated))
	assert.True(t, enrolledAt(undated).Equal(at))
	assert.True(t, enrolledAt(dated).Equal(earlier))
	assert.Nil(t, enrolledAt(dropped))
	assert.Nil(t, enrolledAt(elsewhere))
	var row ProgramClassEnrollment
	require.NoError(t, db.First(&row, undated.ID).Error)
	require.NotNil(t, row.UpdateUserID)
	assert.Equal(t, uint(batchUserID), *row.UpdateUserID)
}

// Without the system_batch user the job stops before changing anything.
func TestActivateScheduledClassesNoBatchUser(t *testing.T) {
	db := newTestDB(t)
	cohort := createCohort(t, db, "scheduled", Scheduled)

	err := activateScheduledClasses(context.Background(), &Store{db})

	require.Error(t, err)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
	var got ProgramClassCohort
	require.NoError(t, db.First(&got, cohort.ID).Error)
	assert.Equal(t, Scheduled, got.Status)
}

func TestSystemBatchUserID(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.Create(&User{Username: "someone", Email: "a@x"}).Error)
	batch := User{Username: "system_batch", Email: "b@x"}
	require.NoError(t, db.Create(&batch).Error)

	id, err := (&Store{db}).SystemBatchUserID(context.Background())
	require.NoError(t, err)
	assert.Equal(t, batch.ID, id)
}
