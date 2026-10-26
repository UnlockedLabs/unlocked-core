package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/suite"
)

type ClassDeleteGuardTestSuite struct {
	suite.Suite
	env *TestEnv
}

func (suite *ClassDeleteGuardTestSuite) SetupSuite() {
	suite.env = SetupTestEnv(suite.T())
}

func (suite *ClassDeleteGuardTestSuite) TearDownSuite() {
	suite.env.CleanupTestEnv()
}

func (suite *ClassDeleteGuardTestSuite) SetupTest() {
	suite.env.DB.Exec("DELETE FROM program_class_event_attendance")
	suite.env.DB.Exec("DELETE FROM program_class_events")
	suite.env.DB.Exec("DELETE FROM program_class_enrollments")
	suite.env.DB.Exec("DELETE FROM class_completions")
	suite.env.DB.Exec("DELETE FROM change_log_entries")
	suite.env.DB.Exec("DELETE FROM program_classes_history")
	suite.env.DB.Exec("DELETE FROM program_class_cohorts")
	suite.env.DB.Exec("DELETE FROM program_class_credit_types")
	suite.env.DB.Exec("DELETE FROM program_classes")
	suite.env.DB.Exec("DELETE FROM facilities_programs")
	suite.env.DB.Exec("DELETE FROM programs")
	suite.env.DB.Exec("DELETE FROM rooms")
	suite.env.DB.Exec("DELETE FROM facilities")
	suite.env.DB.Exec("DELETE FROM users WHERE role != 'system_admin'")
}

// seedEmptyClass returns (admin, class) with an empty program_class — no children.
func (suite *ClassDeleteGuardTestSuite) seedEmptyClass() (*models.User, *programs.ProgramClassCohort) {
	timestamp := fmt.Sprintf("%d", time.Now().UnixNano())
	facility := &models.Facility{Name: "Test Facility " + timestamp}
	suite.env.DB.Create(facility)

	admin := &models.User{
		NameFirst: "Sys", NameLast: "Admin",
		Username: "sys" + timestamp, Email: "sys" + timestamp + "@t.test",
		Role: models.SystemAdmin, FacilityID: facility.ID,
	}
	suite.env.DB.Create(admin)

	program := &programs.Program{Name: "Prog " + timestamp, IsActive: true}
	suite.env.DB.Create(program)
	suite.env.DB.Create(&programs.FacilitiesPrograms{FacilityID: facility.ID, ProgramID: program.ID})

	class := &programs.ProgramClassCohort{
		ProgramID: program.ID, FacilityID: facility.ID,
		Status: programs.Scheduled, Capacity: 10, Description: "x",
	}
	suite.env.DB.Create(class)
	return admin, class
}

func (suite *ClassDeleteGuardTestSuite) TestDelete_EmptyClass_Succeeds() {
	admin, class := suite.seedEmptyClass()

	NewRequest[any](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/program-classes/%d", class.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusNoContent)
}

func (suite *ClassDeleteGuardTestSuite) TestDelete_BlockedByEnrollment() {
	admin, class := suite.seedEmptyClass()

	student := &models.User{
		NameFirst: "Stu", NameLast: "Dent",
		Username: fmt.Sprintf("stu%d", time.Now().UnixNano()),
		Email:    fmt.Sprintf("stu%d@t.test", time.Now().UnixNano()),
		Role:     models.Student, FacilityID: class.FacilityID,
	}
	suite.env.DB.Create(student)

	enrollment := &programs.ProgramClassEnrollment{
		CohortID:         class.ID,
		UserID:           student.ID,
		EnrollmentStatus: programs.Enrolled,
	}
	suite.env.DB.Create(enrollment)

	resp := NewRequest[programs.DeleteBlockingChildren](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/program-classes/%d", class.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusConflict)

	got := resp.GetData()
	suite.Equal(int64(1), got.Enrollments, "should report 1 blocking enrollment")
	suite.Equal(int64(0), got.Events)
	suite.Equal(int64(0), got.Completions)

	// Verify the class was NOT deleted.
	var count int64
	suite.env.DB.Model(&programs.ProgramClassCohort{}).Where("id = ?", class.ID).Count(&count)
	suite.Equal(int64(1), count, "class should still exist after blocked delete")
}

func (suite *ClassDeleteGuardTestSuite) TestDelete_BlockedByCompletedEnrollment() {
	admin, class := suite.seedEmptyClass()
	student := suite.makeStudent(class.FacilityID)

	now := time.Now()
	suite.env.DB.Create(&programs.ProgramClassEnrollment{
		CohortID:          class.ID,
		UserID:            student.ID,
		EnrollmentStatus:  programs.EnrollmentCompleted,
		EnrollmentEndedAt: &now,
	})

	resp := NewRequest[programs.DeleteBlockingChildren](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/program-classes/%d", class.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusConflict)

	got := resp.GetData()
	suite.Equal(int64(1), got.Enrollments, "should report 1 blocking completed enrollment")
	suite.Equal(int64(0), got.Events)
	suite.Equal(int64(0), got.Completions)

	var count int64
	suite.env.DB.Model(&programs.ProgramClassCohort{}).Where("id = ?", class.ID).Count(&count)
	suite.Equal(int64(1), count, "class should still exist after blocked delete")
}

func (suite *ClassDeleteGuardTestSuite) TestDelete_BlockedByCancelledEnrollment() {
	admin, class := suite.seedEmptyClass()
	student := suite.makeStudent(class.FacilityID)

	now := time.Now()
	suite.env.DB.Create(&programs.ProgramClassEnrollment{
		CohortID:          class.ID,
		UserID:            student.ID,
		EnrollmentStatus:  programs.EnrollmentCancelled,
		EnrollmentEndedAt: &now,
	})

	resp := NewRequest[programs.DeleteBlockingChildren](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/program-classes/%d", class.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusConflict)

	got := resp.GetData()
	suite.Equal(int64(1), got.Enrollments, "should report 1 blocking cancelled enrollment")
	suite.Equal(int64(0), got.Events)
	suite.Equal(int64(0), got.Completions)

	var count int64
	suite.env.DB.Model(&programs.ProgramClassCohort{}).Where("id = ?", class.ID).Count(&count)
	suite.Equal(int64(1), count, "class should still exist after blocked delete")
}

func (suite *ClassDeleteGuardTestSuite) TestDelete_EventsOnly_Succeeds() {
	admin, class := suite.seedEmptyClass()

	room := &programs.Room{FacilityID: class.FacilityID, Name: "101"}
	suite.env.DB.Create(room)
	instructor := &models.User{
		NameFirst: "Inst", NameLast: "Ructor",
		Username: fmt.Sprintf("inst%d", time.Now().UnixNano()),
		Email:    fmt.Sprintf("inst%d@t.test", time.Now().UnixNano()),
		Role:     models.FacilityAdmin, FacilityID: class.FacilityID,
	}
	suite.env.DB.Create(instructor)
	suite.env.DB.Create(&programs.ProgramClassEvent{
		CohortID:       class.ID,
		Duration:       "1h0m0s",
		RecurrenceRule: "DTSTART:20260302T093000Z\nRRULE:FREQ=WEEKLY;COUNT=1",
		RoomID:         &room.ID,
		InstructorID:   &instructor.ID,
	})

	NewRequest[any](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/program-classes/%d", class.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusNoContent)

	var liveEvents int64
	suite.env.DB.Model(&programs.ProgramClassEvent{}).
		Where("cohort_id = ? AND deleted_at IS NULL", class.ID).
		Count(&liveEvents)
	suite.Equal(int64(0), liveEvents, "events should be soft-deleted alongside the class")
}

func (suite *ClassDeleteGuardTestSuite) TestDelete_BlockedByCompletion() {
	admin, class := suite.seedEmptyClass()
	student := suite.makeStudent(class.FacilityID)

	cohortID, classID, programID := class.ID, class.ClassID, class.ProgramID
	suite.env.DB.Create(&programs.ClassCompletion{
		UserID:       student.ID,
		CohortID:     &cohortID,
		ClassID:      &classID,
		ProgramID:    &programID,
		FacilityName: "Test Facility",
		CreditType:   "Completion",
		AdminEmail:   "admin@t.test",
		ProgramOwner: "Owner",
		ProgramName:  "Prog",
	})

	resp := NewRequest[programs.DeleteBlockingChildren](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/program-classes/%d", class.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusConflict)

	got := resp.GetData()
	suite.Equal(int64(0), got.Enrollments)
	suite.Equal(int64(0), got.Events)
	suite.Equal(int64(1), got.Completions, "should report 1 blocking completion")

	var count int64
	suite.env.DB.Model(&programs.ProgramClassCohort{}).Where("id = ?", class.ID).Count(&count)
	suite.Equal(int64(1), count, "class should still exist after blocked delete")
}

func (suite *ClassDeleteGuardTestSuite) TestDelete_NonScheduledStatus_Blocked() {
	for _, status := range []programs.ClassStatus{programs.Active, programs.Completed, programs.Cancelled, programs.Paused} {
		admin, class := suite.seedEmptyClass()
		suite.env.DB.Model(&programs.ProgramClassCohort{}).Where("id = ?", class.ID).Update("status", status)

		resp := NewRequest[programs.DeleteBlockingChildren](suite.env.Client, suite.T(), http.MethodDelete,
			fmt.Sprintf("/api/program-classes/%d", class.ID), nil).
			WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
			Do().
			ExpectStatus(http.StatusConflict)

		got := resp.GetData()
		suite.Equal(string(status), got.NonDeletableStatus, "blocker should report the class status")

		var count int64
		suite.env.DB.Model(&programs.ProgramClassCohort{}).Where("id = ?", class.ID).Count(&count)
		suite.Equal(int64(1), count, "class with status %s should still exist after blocked delete", status)
	}
}

func (suite *ClassDeleteGuardTestSuite) TestDelete_HistoryOnly_Succeeds() {
	admin, class := suite.seedEmptyClass()

	suite.env.DB.Create(programs.NewChangeLogEntry(
		programs.TableNameCohort, "name",
		models.StringPtr("old"), models.StringPtr("new"),
		class.ID, admin.ID,
	))

	NewRequest[any](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/program-classes/%d", class.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusNoContent)

	var changeLogCount int64
	suite.env.DB.Table("change_log_entries").
		Where("table_name = 'program_class_cohorts' AND parent_ref_id = ?", class.ID).
		Count(&changeLogCount)
	suite.Equal(int64(0), changeLogCount, "change_log_entries for the class should be cleaned up")

	var historyCount int64
	suite.env.DB.Table("program_classes_history").
		Where("table_name = 'program_class_cohorts' AND parent_ref_id = ?", class.ID).
		Count(&historyCount)
	suite.Equal(int64(0), historyCount, "program_classes_history rows for the class should be cleaned up")
}

func (suite *ClassDeleteGuardTestSuite) makeStudent(facilityID uint) *models.User {
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	u := &models.User{
		NameFirst: "Stu", NameLast: "Dent",
		Username: "stu" + ts, Email: "stu" + ts + "@t.test",
		Role: models.Student, FacilityID: facilityID,
	}
	suite.env.DB.Create(u)
	return u
}

func TestClassDeleteGuardTestSuite(t *testing.T) {
	suite.Run(t, new(ClassDeleteGuardTestSuite))
}
