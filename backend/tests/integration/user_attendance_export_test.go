package integration

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

func TestExportResidentAttendanceCSV(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility1, err := env.CreateTestFacility("Test Facility 1")
	require.NoError(t, err)

	facility2, err := env.CreateTestFacility("Test Facility 2")
	require.NoError(t, err)

	_, err = env.CreateTestUser("sysadmin", models.SystemAdmin, facility1.ID, "")
	require.NoError(t, err)

	_, err = env.CreateTestUser("facadmin1", models.FacilityAdmin, facility1.ID, "")
	require.NoError(t, err)

	_, err = env.CreateTestUser("facadmin2", models.FacilityAdmin, facility2.ID, "")
	require.NoError(t, err)

	student1, err := env.CreateTestUser("student1", models.Student, facility1.ID, "STU001")
	require.NoError(t, err)

	student2, err := env.CreateTestUser("student2", models.Student, facility2.ID, "STU002")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Math Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility1.ID, facility2.ID})
	require.NoError(t, err)

	instructor1, err := env.CreateTestInstructor(facility1.ID, "export1")
	require.NoError(t, err)

	class1, err := env.CreateTestClass(program, facility1, programs.Active, &instructor1.ID)
	require.NoError(t, err)
	require.NoError(t, env.NameCohortClass(class1.ID, "Math Class"))

	event1, err := env.CreateTestEvent(class1.ID, "", instructor1.ID)
	require.NoError(t, err)

	date1 := time.Now().AddDate(0, 0, -5).Format("2006-01-02")
	date2 := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	date3 := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	enrollmentDate := time.Now().AddDate(0, 0, -10)
	enrollment := programs.ProgramClassEnrollment{
		CohortID:         class1.ID,
		UserID:           student1.ID,
		EnrollmentStatus: programs.Enrolled,
		EnrolledAt:       &enrollmentDate,
	}
	require.NoError(t, env.DB.Create(&enrollment).Error)

	attendance1 := programs.ProgramClassEventAttendance{
		EventID:          event1.ID,
		UserID:           student1.ID,
		Date:             date1,
		AttendanceStatus: programs.Present,
		Note:             models.StringPtr("Great participation"),
	}
	require.NoError(t, env.DB.Create(&attendance1).Error)

	attendance2 := programs.ProgramClassEventAttendance{
		EventID:          event1.ID,
		UserID:           student1.ID,
		Date:             date2,
		AttendanceStatus: programs.Absent_Excused,
		Note:             models.StringPtr("Doctor appointment"),
	}
	require.NoError(t, env.DB.Create(&attendance2).Error)

	attendance3 := programs.ProgramClassEventAttendance{
		EventID:          event1.ID,
		UserID:           student1.ID,
		Date:             date3,
		AttendanceStatus: programs.Absent_Unexcused,
		Note:             nil,
	}
	require.NoError(t, env.DB.Create(&attendance3).Error)

	t.Run("Successful CSV export with attendance data", func(t *testing.T) {
		resp := NewRequest[any](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/attendance-export", student1.ID), nil).
			WithTestClaims(&handlers.Claims{
				Role:       models.SystemAdmin,
				FacilityID: facility1.ID,
			}).
			AsRaw().
			Do().
			ExpectStatus(http.StatusOK)

		require.Contains(t, resp.resp.Header.Get("Content-Type"), "text/csv")
		require.Contains(t, resp.resp.Header.Get("Content-Disposition"), "attachment")
		require.Contains(t, resp.resp.Header.Get("Content-Disposition"), "Attendance-STU001-")

		csvReader := csv.NewReader(strings.NewReader(resp.rawBody))
		records, err := csvReader.ReadAll()
		require.NoError(t, err)

		require.GreaterOrEqual(t, len(records), 4, "Expected at least header + 3 attendance records")

		require.Equal(t, []string{"Program Name", "Class Name", "Session Date", "Attendance Status", "Note"}, records[0])

		foundPresent := false
		foundAbsentExcused := false
		foundAbsentUnexcused := false

		for _, record := range records[1:] {
			require.Equal(t, "Math Program", record[0])
			require.Contains(t, record[1], "Class")

			if record[3] == "Present" {
				foundPresent = true
				require.Equal(t, "Great participation", record[4])
			}
			if record[3] == "Absent Excused" {
				foundAbsentExcused = true
				require.Equal(t, "Doctor appointment", record[4])
			}
			if record[3] == "Absent Unexcused" {
				foundAbsentUnexcused = true
				require.Equal(t, "", record[4])
			}
		}

		require.True(t, foundPresent, "Expected to find 'Present' attendance status")
		require.True(t, foundAbsentExcused, "Expected to find 'Absent Excused' attendance status")
		require.True(t, foundAbsentUnexcused, "Expected to find 'Absent Unexcused' attendance status")
	})

	t.Run("Facility admin cannot export attendance from different facility", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/attendance-export", student2.ID), nil).
			WithTestClaims(&handlers.Claims{
				Role:       models.FacilityAdmin,
				FacilityID: facility1.ID,
			}).
			Do().
			ExpectStatus(http.StatusUnauthorized)
	})

	t.Run("Facility admin can export attendance from own facility", func(t *testing.T) {
		resp := NewRequest[any](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/attendance-export", student1.ID), nil).
			WithTestClaims(&handlers.Claims{
				Role:       models.FacilityAdmin,
				FacilityID: facility1.ID,
			}).
			AsRaw().
			Do().
			ExpectStatus(http.StatusOK)

		require.Contains(t, resp.resp.Header.Get("Content-Type"), "text/csv")
	})

	t.Run("Export for student with no attendance records returns CSV with headers only", func(t *testing.T) {
		studentNoAttendance, err := env.CreateTestUser("studentnone", models.Student, facility1.ID, "STU003")
		require.NoError(t, err)

		resp := NewRequest[any](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/attendance-export", studentNoAttendance.ID), nil).
			WithTestClaims(&handlers.Claims{
				Role:       models.SystemAdmin,
				FacilityID: facility1.ID,
			}).
			AsRaw().
			Do().
			ExpectStatus(http.StatusOK)

		csvReader := csv.NewReader(strings.NewReader(resp.rawBody))
		records, err := csvReader.ReadAll()
		require.NoError(t, err)

		require.Equal(t, 1, len(records), "Expected only header row for student with no attendance")
		require.Equal(t, []string{"Program Name", "Class Name", "Session Date", "Attendance Status", "Note"}, records[0])
	})

	t.Run("Deactivated student can still export historical attendance", func(t *testing.T) {
		deactivatedTime := time.Now()
		student1.DeactivatedAt = &deactivatedTime
		require.NoError(t, env.DB.Save(student1).Error)

		resp := NewRequest[any](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/attendance-export", student1.ID), nil).
			WithTestClaims(&handlers.Claims{
				Role:       models.SystemAdmin,
				FacilityID: facility1.ID,
			}).
			AsRaw().
			Do().
			ExpectStatus(http.StatusOK)

		csvReader := csv.NewReader(strings.NewReader(resp.rawBody))
		records, err := csvReader.ReadAll()
		require.NoError(t, err)

		require.GreaterOrEqual(t, len(records), 4, "Deactivated student should still have attendance records")
	})

	t.Run("Only exports attendance during enrollment period", func(t *testing.T) {
		studentEnrollmentDates, err := env.CreateTestUser("studenroll", models.Student, facility1.ID, "STU004")
		require.NoError(t, err)

		instructor2, err := env.CreateTestInstructor(facility1.ID, "export2")
		require.NoError(t, err)

		class2, err := env.CreateTestClass(program, facility1, programs.Active, &instructor2.ID)
		require.NoError(t, err)

		event2, err := env.CreateTestEvent(class2.ID, "", instructor2.ID)
		require.NoError(t, err)

		enrollDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
		endDate := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)

		enrollment2 := programs.ProgramClassEnrollment{
			CohortID:          class2.ID,
			UserID:            studentEnrollmentDates.ID,
			EnrollmentStatus:  programs.Enrolled,
			EnrolledAt:        &enrollDate,
			EnrollmentEndedAt: &endDate,
		}
		require.NoError(t, env.DB.Create(&enrollment2).Error)

		beforeEnrollment := enrollDate.AddDate(0, 0, -1).Format("2006-01-02")
		duringEnrollment := enrollDate.AddDate(0, 0, 1).Format("2006-01-02")
		afterEnrollment := endDate.AddDate(0, 0, 1).Format("2006-01-02")

		attendanceBefore := programs.ProgramClassEventAttendance{
			EventID:          event2.ID,
			UserID:           studentEnrollmentDates.ID,
			Date:             beforeEnrollment,
			AttendanceStatus: programs.Present,
			Note:             models.StringPtr("Before enrollment"),
		}
		require.NoError(t, env.DB.Create(&attendanceBefore).Error)

		attendanceDuring := programs.ProgramClassEventAttendance{
			EventID:          event2.ID,
			UserID:           studentEnrollmentDates.ID,
			Date:             duringEnrollment,
			AttendanceStatus: programs.Present,
			Note:             models.StringPtr("During enrollment"),
		}
		require.NoError(t, env.DB.Create(&attendanceDuring).Error)

		attendanceAfter := programs.ProgramClassEventAttendance{
			EventID:          event2.ID,
			UserID:           studentEnrollmentDates.ID,
			Date:             afterEnrollment,
			AttendanceStatus: programs.Present,
			Note:             models.StringPtr("After enrollment"),
		}
		require.NoError(t, env.DB.Create(&attendanceAfter).Error)

		resp := NewRequest[any](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/attendance-export", studentEnrollmentDates.ID), nil).
			WithTestClaims(&handlers.Claims{
				Role:       models.SystemAdmin,
				FacilityID: facility1.ID,
			}).
			AsRaw().
			Do().
			ExpectStatus(http.StatusOK)

		csvReader := csv.NewReader(strings.NewReader(resp.rawBody))
		records, err := csvReader.ReadAll()
		require.NoError(t, err)

		require.Equal(t, 2, len(records), "Should only export attendance during enrollment period (header + 1 record)")

		if len(records) > 1 {
			require.Equal(t, "During enrollment", records[1][4], "Should only include attendance during enrollment period")
		}
	})

	t.Run("CSV rows are sorted by program, class, then date", func(t *testing.T) {
		studentSorting, err := env.CreateTestUser("studentsort", models.Student, facility1.ID, "STU005")
		require.NoError(t, err)

		program2, err := env.CreateTestProgram("Algebra Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
		require.NoError(t, err)

		err = env.SetFacilitiesToProgram(program2.ID, []uint{facility1.ID})
		require.NoError(t, err)

		instructor3, err := env.CreateTestInstructor(facility1.ID, "export3")
		require.NoError(t, err)
		instructor4, err := env.CreateTestInstructor(facility1.ID, "export4")
		require.NoError(t, err)

		class3, err := env.CreateTestClass(program2, facility1, programs.Active, &instructor3.ID)
		require.NoError(t, err)

		class4, err := env.CreateTestClass(program, facility1, programs.Active, &instructor4.ID)
		require.NoError(t, err)

		event3, err := env.CreateTestEvent(class3.ID, "", instructor3.ID)
		require.NoError(t, err)

		event4, err := env.CreateTestEvent(class4.ID, "", instructor4.ID)
		require.NoError(t, err)

		enrollDate := time.Now().AddDate(0, 0, -10)
		enrollment3 := programs.ProgramClassEnrollment{
			CohortID:         class3.ID,
			UserID:           studentSorting.ID,
			EnrollmentStatus: programs.Enrolled,
			EnrolledAt:       &enrollDate,
		}
		require.NoError(t, env.DB.Create(&enrollment3).Error)

		enrollment4 := programs.ProgramClassEnrollment{
			CohortID:         class4.ID,
			UserID:           studentSorting.ID,
			EnrollmentStatus: programs.Enrolled,
			EnrolledAt:       &enrollDate,
		}
		require.NoError(t, env.DB.Create(&enrollment4).Error)

		date1 := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
		date2 := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

		attendances := []programs.ProgramClassEventAttendance{
			{EventID: event4.ID, UserID: studentSorting.ID, Date: date2, AttendanceStatus: programs.Present, Note: models.StringPtr("Math 2")},
			{EventID: event4.ID, UserID: studentSorting.ID, Date: date1, AttendanceStatus: programs.Present, Note: models.StringPtr("Math 1")},
			{EventID: event3.ID, UserID: studentSorting.ID, Date: date2, AttendanceStatus: programs.Present, Note: models.StringPtr("Algebra 2")},
			{EventID: event3.ID, UserID: studentSorting.ID, Date: date1, AttendanceStatus: programs.Present, Note: models.StringPtr("Algebra 1")},
		}

		for _, att := range attendances {
			require.NoError(t, env.DB.Create(&att).Error)
		}

		resp := NewRequest[any](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/attendance-export", studentSorting.ID), nil).
			WithTestClaims(&handlers.Claims{
				Role:       models.SystemAdmin,
				FacilityID: facility1.ID,
			}).
			AsRaw().
			Do().
			ExpectStatus(http.StatusOK)

		csvReader := csv.NewReader(strings.NewReader(resp.rawBody))
		records, err := csvReader.ReadAll()
		require.NoError(t, err)

		require.Equal(t, 5, len(records), "Expected header + 4 attendance records")

		require.Equal(t, "Algebra Program", records[1][0], "First program should be Algebra (alphabetically)")
		require.Equal(t, "Math Program", records[3][0], "Second program should be Math (alphabetically)")

		require.True(t, records[1][2] < records[2][2], "Dates within Algebra Program should be ascending")
		require.True(t, records[3][2] < records[4][2], "Dates within Math Program should be ascending")
	})

	t.Run("Student role cannot access export endpoint", func(t *testing.T) {
		NewRequest[any](env.Client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/attendance-export", student1.ID), nil).
			WithTestClaims(&handlers.Claims{
				Role:       models.Student,
				FacilityID: facility1.ID,
				UserID:     student1.ID,
			}).
			Do().
			ExpectStatus(http.StatusUnauthorized)
	})
}
