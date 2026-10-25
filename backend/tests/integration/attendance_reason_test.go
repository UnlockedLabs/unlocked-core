package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

func TestAttendanceReasons(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Test Facility")
	require.NoError(t, err)

	facilityAdmin, err := env.CreateTestUser("testadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	student, err := env.CreateTestUser("student", models.Student, facility.ID, "STU001")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Test Program", programs.FundingType(programs.FederalGrants), []programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facility.ID})
	require.NoError(t, err)

	instructor, err := env.CreateTestInstructor(facility.ID, "reason")
	require.NoError(t, err)

	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	event, err := env.CreateTestEvent(class.ID, "", instructor.ID)
	require.NoError(t, err)

	_, err = env.CreateTestEnrollment(class.ID, student.ID, programs.Enrolled)
	require.NoError(t, err)

	date := time.Now().Format("2006-01-02")

	t.Run("Save attendance with Medical reason", func(t *testing.T) {
		attendanceData := []programs.ProgramClassEventAttendance{
			{
				UserID:           student.ID,
				Date:             date,
				AttendanceStatus: programs.Absent_Excused,
				ReasonCategory:   models.StringPtr("Medical"),
				Note:             nil,
			},
		}

		NewRequest[any](env.Client, t, http.MethodPost,
			fmt.Sprintf("/api/program-classes/%d/events/%d/attendance", class.ID, event.ID),
			attendanceData).
			WithTestClaims(&handlers.Claims{
				Role:       models.FacilityAdmin,
				FacilityID: facilityAdmin.FacilityID,
			}).
			Do().
			ExpectStatus(http.StatusOK)

		// Verify in DB
		var stored programs.ProgramClassEventAttendance
		err := env.DB.Where("user_id = ? AND event_id = ? AND date = ?", student.ID, event.ID, date).First(&stored).Error
		require.NoError(t, err)
		require.Equal(t, "Medical", models.FormatNullableString(stored.ReasonCategory))
		require.Nil(t, stored.Note)
	})

	t.Run("Save attendance with Other reason and Note", func(t *testing.T) {
		attendanceData := []programs.ProgramClassEventAttendance{
			{
				UserID:           student.ID,
				Date:             date,
				AttendanceStatus: programs.Absent_Unexcused,
				ReasonCategory:   models.StringPtr("Other"),
				Note:             models.StringPtr("Something specific"),
			},
		}

		NewRequest[any](env.Client, t, http.MethodPost,
			fmt.Sprintf("/api/program-classes/%d/events/%d/attendance", class.ID, event.ID),
			attendanceData).
			WithTestClaims(&handlers.Claims{
				Role:       models.FacilityAdmin,
				FacilityID: facilityAdmin.FacilityID,
			}).
			Do().
			ExpectStatus(http.StatusOK)

		// Verify in DB
		var stored programs.ProgramClassEventAttendance
		err := env.DB.Where("user_id = ? AND event_id = ? AND date = ?", student.ID, event.ID, date).First(&stored).Error
		require.NoError(t, err)
		require.Equal(t, "Other", models.FormatNullableString(stored.ReasonCategory))
		require.Equal(t, "Something specific", models.FormatNullableString(stored.Note))
	})

	t.Run("Update to Present clears reason and note", func(t *testing.T) {
		// First set it to something else (should be covered by previous test, but good to be explicit if run in isolation)
		// But here we just update the existing record from previous step
		attendanceData := []programs.ProgramClassEventAttendance{
			{
				UserID:           student.ID,
				Date:             date,
				AttendanceStatus: programs.Present,
				ReasonCategory:   models.StringPtr(""), // Frontend sends empty string
				Note:             models.StringPtr(""),
			},
		}

		NewRequest[any](env.Client, t, http.MethodPost,
			fmt.Sprintf("/api/program-classes/%d/events/%d/attendance", class.ID, event.ID),
			attendanceData).
			WithTestClaims(&handlers.Claims{
				Role:       models.FacilityAdmin,
				FacilityID: facilityAdmin.FacilityID,
			}).
			Do().
			ExpectStatus(http.StatusOK)

		// Verify in DB
		var stored programs.ProgramClassEventAttendance
		err := env.DB.Where("user_id = ? AND event_id = ? AND date = ?", student.ID, event.ID, date).First(&stored).Error
		require.NoError(t, err)
		require.Equal(t, programs.Present, stored.AttendanceStatus)
		// Note: Depending on how the update works, it might not clear fields if they are not specified,
		// but the handler receives the whole struct.
		// The `LogUserAttendance` uses `clause.OnConflict` with `DoUpdates`.
		// Empty strings are stored as NULL.
		require.Nil(t, stored.ReasonCategory)
		require.Nil(t, stored.Note)
	})

	t.Run("Update from Present back to Absent saves reason", func(t *testing.T) {
		attendanceData := []programs.ProgramClassEventAttendance{
			{
				UserID:           student.ID,
				Date:             date,
				AttendanceStatus: programs.Absent_Excused,
				ReasonCategory:   models.StringPtr("Disciplinary"),
				Note:             nil,
			},
		}

		NewRequest[any](env.Client, t, http.MethodPost,
			fmt.Sprintf("/api/program-classes/%d/events/%d/attendance", class.ID, event.ID),
			attendanceData).
			WithTestClaims(&handlers.Claims{
				Role:       models.FacilityAdmin,
				FacilityID: facilityAdmin.FacilityID,
			}).
			Do().
			ExpectStatus(http.StatusOK)

		// Verify in DB
		var stored programs.ProgramClassEventAttendance
		err := env.DB.Where("user_id = ? AND event_id = ? AND date = ?", student.ID, event.ID, date).First(&stored).Error
		require.NoError(t, err)
		require.Equal(t, programs.Absent_Excused, stored.AttendanceStatus)
		require.Equal(t, "Disciplinary", models.FormatNullableString(stored.ReasonCategory))
	})
}
