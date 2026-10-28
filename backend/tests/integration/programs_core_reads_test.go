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

// These tests pin the core routes that read or write Programs' tables
// (EN-200): the facility list's counts and can_delete, the facility delete
// check and delete, a resident's transfer, and the account history's join on
// program_classes_history. They must pass unchanged once core asks Programs
// for those numbers and the tables move into the programs schema.
//
// Regenerate the golden files after an intended change with:
//
//	go test ./tests/integration -run 'TestProgramsCoreReads' -update

// The seeded account history row is an hour newer than the
// program_classes_history row it points at.
var (
	classHistoryAt   = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	accountHistoryAt = classHistoryAt.Add(time.Hour)
)

type programsCoreWorld struct {
	north, south, classHall, programWing, roomAnnex, empty *models.Facility
	resident, deptAdmin                                    *models.User
	northClass                                             *programs.ProgramClassCohort
}

// seedProgramsCore adds facilities that each hold one kind of Programs data,
// so each golden shows which kinds block deleting a facility:
//   - North has the resident, an active program and class, and a room;
//   - South offers the same program, and is where the resident transfers;
//   - Class Hall has only a class;
//   - Program Wing only offers a program;
//   - Room Annex has only a room;
//   - Empty Wing has nothing.
func seedProgramsCore(t *testing.T, env *TestEnv) programsCoreWorld {
	t.Helper()
	var w programsCoreWorld
	for _, f := range []struct {
		dst  **models.Facility
		name string
	}{
		{&w.north, "North"}, {&w.south, "South"}, {&w.classHall, "Class Hall"},
		{&w.programWing, "Program Wing"}, {&w.roomAnnex, "Room Annex"}, {&w.empty, "Empty Wing"},
	} {
		facility, err := env.CreateTestFacility(f.name)
		require.NoError(t, err)
		*f.dst = facility
	}
	var err error
	w.resident, err = env.CreateTestUser("jdoe", models.Student, w.north.ID, "1001")
	require.NoError(t, err)
	w.deptAdmin, err = env.CreateTestUser("deptadmin", models.DepartmentAdmin, w.north.ID, "")
	require.NoError(t, err)

	reading, err := env.CreateTestProgram("Reading", programs.Other, nil, nil, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(reading.ID, []uint{w.north.ID, w.south.ID, w.programWing.ID}))
	w.northClass, err = env.CreateTestClass(reading, w.north, programs.Active, nil)
	require.NoError(t, err)
	_, err = env.CreateTestClass(reading, w.classHall, programs.Active, nil)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(w.northClass.ID, w.resident.ID, programs.Enrolled)
	require.NoError(t, err)
	// Welding is offered only at North, so it shows as a transfer conflict.
	welding, err := env.CreateTestProgram("Welding", programs.Other, nil, nil, true, nil)
	require.NoError(t, err)
	require.NoError(t, env.SetFacilitiesToProgram(welding.ID, []uint{w.north.ID}))
	weldingClass, err := env.CreateTestClass(welding, w.north, programs.Active, nil)
	require.NoError(t, err)
	_, err = env.CreateTestEnrollment(weldingClass.ID, w.resident.ID, programs.Enrolled)
	require.NoError(t, err)
	for _, f := range []*models.Facility{w.north, w.roomAnnex} {
		require.NoError(t, env.DB.Create(&programs.Room{FacilityID: f.ID, Name: f.Name + " Room"}).Error)
	}
	// Test classes start now; a fixed start keeps the goldens stable.
	start := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	require.NoError(t, env.DB.Model(&programs.ProgramClassCohort{}).
		Where("id IN ?", []uint{w.northClass.ID, weldingClass.ID}).Update("start_dt", start).Error)

	// An enrollment change logged the way the program_classes_history trigger
	// logs one, with an account history row pointing at it.
	historyID := env.InsertReturningID(t,
		`INSERT INTO program_classes_history (parent_ref_id, table_name, before_update, after_update, created_at)
			VALUES (?, 'program_class_cohorts', '{"status":"Scheduled"}', '{"status":"Active"}', ?)`,
		w.northClass.ID, classHistoryAt)
	require.NoError(t, env.DB.Exec(
		`INSERT INTO user_account_history (user_id, admin_id, action, program_classes_history_id, facility_id, created_at)
			VALUES (?, ?, 'progclass_history', ?, ?, ?)`,
		w.resident.ID, w.deptAdmin.ID, historyID, w.north.ID, accountHistoryAt).Error)
	return w
}

func TestProgramsCoreReads(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	w := seedProgramsCore(t, env)
	deptAdmin := &handlers.Claims{UserID: w.deptAdmin.ID, Username: w.deptAdmin.Username, Role: w.deptAdmin.Role, FacilityID: w.north.ID, FacilityName: w.north.Name}
	facility := func(format string, f *models.Facility) string { return fmt.Sprintf(format, f.ID) }
	history := fmt.Sprintf("/api/users/%d/account-history", w.resident.ID)

	type routeCase struct {
		name   string
		method string
		path   string
		status int
	}
	run := func(t *testing.T, cases []routeCase) {
		t.Helper()
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				resp := NewRequest[any](env.Client, t, tt.method, tt.path, nil).
					WithTestClaims(deptAdmin).AsRaw().Do().
					ExpectStatus(tt.status)
				expectGolden(t, "programs_core_reads", tt.name, resp.rawBody)
			})
		}
	}

	// The cases run in order against one database, so the reads after the
	// deletes and the transfer show them.
	run(t, []routeCase{
		{"facilities", http.MethodGet, "/api/facilities", http.StatusOK},
		{"delete_check_north", http.MethodGet, facility("/api/facilities/%d/delete-check", w.north), http.StatusOK},
		{"delete_check_class", http.MethodGet, facility("/api/facilities/%d/delete-check", w.classHall), http.StatusOK},
		{"delete_check_program", http.MethodGet, facility("/api/facilities/%d/delete-check", w.programWing), http.StatusOK},
		{"delete_check_room", http.MethodGet, facility("/api/facilities/%d/delete-check", w.roomAnnex), http.StatusOK},
		{"delete_check_empty", http.MethodGet, facility("/api/facilities/%d/delete-check", w.empty), http.StatusOK},
		{"delete_class", http.MethodDelete, facility("/api/facilities/%d", w.classHall), http.StatusConflict},
		{"delete_program", http.MethodDelete, facility("/api/facilities/%d", w.programWing), http.StatusConflict},
		{"delete_room", http.MethodDelete, facility("/api/facilities/%d", w.roomAnnex), http.StatusConflict},
		{"delete_empty", http.MethodDelete, facility("/api/facilities/%d", w.empty), http.StatusNoContent},
		{"account_history", http.MethodGet, history, http.StatusOK},
		{"transfer_notes", http.MethodGet, fmt.Sprintf("/api/users/resident-verify?facility_id=%d&user_id=%d&doc_id=1001", w.south.ID, w.resident.ID), http.StatusOK},
	})

	// The golden masks times, so check which row's created_at the history
	// join returns: today, psh.* replaces the account row's own with the
	// program_classes_history row's.
	t.Run("account_history_created_at", func(t *testing.T) {
		resp := NewRequest[[]struct {
			CreatedAt time.Time `json:"created_at"`
		}](env.Client, t, http.MethodGet, history, nil).
			WithTestClaims(deptAdmin).Do().
			ExpectStatus(http.StatusOK)
		rows := resp.GetData()
		require.Len(t, rows, 1)
		require.True(t, classHistoryAt.Equal(rows[0].CreatedAt), "created_at %s", rows[0].CreatedAt)
	})

	// The transfer route reads the user on a second connection while its
	// transaction is open, which SQLite's single connection can't serve, so
	// the transfer itself runs at the database layer.
	t.Run("transfer", func(t *testing.T) {
		args := &models.QueryContext{Ctx: t.Context(), UserID: w.deptAdmin.ID}
		tx, err := env.DB.TransferResident(args, int(w.resident.ID), int(w.north.ID), int(w.south.ID))
		require.NoError(t, err)
		require.NoError(t, tx.Commit().Error)
	})

	run(t, []routeCase{
		{"enrollments_after_transfer", http.MethodGet, fmt.Sprintf("/api/program-classes/%d/enrollments?status=all", w.northClass.ID), http.StatusOK},
		{"facilities_after", http.MethodGet, "/api/facilities", http.StatusOK},
	})
}
