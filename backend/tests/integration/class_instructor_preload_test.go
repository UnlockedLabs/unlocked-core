package integration

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

// The Instructor column on /classes and on the program-detail Classes tab is NOT a column
// on the cohort -- the frontend derives it from the cohort's events via
// getInstructorName(cls.events), which reads event.instructor_ref. That field is only
// populated by a GORM Preload, so dropping the preload renders the column blank with no
// error anywhere: the query succeeds, the JSON is well-formed, the field is simply absent.
// Both list queries shipped without it. These tests pin it down.
func TestClassListPopulatesInstructorRef(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Preload Test Facility")
	require.NoError(t, err)

	admin, err := env.CreateTestUser("preloadadmin", models.FacilityAdmin, facility.ID, "")
	require.NoError(t, err)

	program, err := env.CreateTestProgram("Preload Program", programs.FundingType(programs.FederalGrants),
		[]programs.ProgramType{}, []programs.ProgramCreditType{}, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(program.ID, []uint{facility.ID}))

	instructor, err := env.CreateTestInstructor(facility.ID, "preload")
	require.NoError(t, err)

	cohort, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)

	_, err = env.CreateTestEvent(cohort.ID, "", instructor.ID)
	require.NoError(t, err)

	claims := &handlers.Claims{Role: models.FacilityAdmin, UserID: admin.ID, FacilityID: facility.ID}

	t.Run("GET /api/program-classes preloads the event instructor", func(t *testing.T) {
		resp := NewRequest[[]programs.ProgramClassCohort](env.Client, t, http.MethodGet,
			"/api/program-classes", nil).
			WithTestClaims(claims).
			Do().
			ExpectStatus(http.StatusOK)

		classes := resp.GetData()
		require.NotEmpty(t, classes, "expected the seeded cohort in the list")

		found := false
		for _, cls := range classes {
			if cls.ID != cohort.ID {
				continue
			}
			found = true
			require.NotEmpty(t, cls.Events, "cohort must carry its events, the Instructor column reads them")
			ev := cls.Events[0]
			require.NotNil(t, ev.Instructor,
				"event.instructor_ref is nil -- Preload(\"Events.Instructor\") is missing from GetClasses, so the Instructor column on /classes renders blank")
			require.Equal(t, instructor.NameFirst, ev.Instructor.NameFirst)
			require.Equal(t, instructor.NameLast, ev.Instructor.NameLast)
		}
		require.True(t, found, "cohort %d not present in the class list", cohort.ID)
	})

	// This one calls the query directly rather than through GET /api/programs/{id}/classes.
	// That handler also computes attendance rates via a raw query using Postgres `::numeric`
	// casts (events_attendance_store.go), which SQLite cannot parse -- it fails with
	// "unrecognized token: :" and the endpoint 500s under the test suite regardless of this
	// fix. So the endpoint itself is untestable here; the preload still is.
	t.Run("GetProgramClassDetailsByID preloads the event instructor", func(t *testing.T) {
		details, err := env.Programs.GetProgramClassDetailsByID(int(program.ID), &programs.QueryContext{
			Ctx:        env.Context,
			Timezone:   "UTC",
			FacilityID: facility.ID,
			PerPage:    10,
			Page:       1,
			Params:     url.Values{},
		})
		require.NoError(t, err)
		require.NotEmpty(t, details, "expected the seeded cohort in the program's class list")

		found := false
		for _, d := range details {
			if d.ID != cohort.ID {
				continue
			}
			found = true
			require.NotEmpty(t, d.Events, "cohort must carry its events")
			require.NotNil(t, d.Events[0].Instructor,
				"event.instructor_ref is nil -- Preload(\"Instructor\") is missing from GetProgramClassDetailsByID, so the program-detail Classes tab shows \"—\"")
			require.Equal(t, instructor.NameLast, d.Events[0].Instructor.NameLast)
		}
		require.True(t, found, "cohort %d not present in the program's class list", cohort.ID)
	})
}
