package integration

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/learningrecord"

	"github.com/stretchr/testify/require"
)

// These tests pin what every Learning Record route answers (EN-195): the
// status and the JSON, who may call it, and the two behaviors invisible in a
// JSON response body (a soft-deleted facility is excluded from the location
// picker, and deleting an entry records who deleted it). They must pass
// unchanged after the move.
//
// Regenerate the golden files after an intended change with:
//
//	go test ./tests/integration -run 'TestLearningRecord' -update

type learningRecordWorld struct {
	north, south *models.Facility
	resident     *models.User
}

func (w learningRecordWorld) claims(u *models.User) *handlers.Claims {
	return &handlers.Claims{UserID: u.ID, Username: u.Username, Role: u.Role, FacilityID: w.north.ID, FacilityName: w.north.Name}
}

// seedLearningRecord adds two facilities and a soft-deleted third (to prove
// GetAllFacilitiesOrdered excludes it, same as it does today), plus one
// resident.
func seedLearningRecord(t *testing.T, env *TestEnv) learningRecordWorld {
	t.Helper()
	var w learningRecordWorld
	var err error
	w.north, err = env.CreateTestFacility("North")
	require.NoError(t, err)
	w.south, err = env.CreateTestFacility("South")
	require.NoError(t, err)
	retired, err := env.CreateTestFacility("Retired")
	require.NoError(t, err)
	require.NoError(t, env.DB.Delete(retired).Error)
	w.resident, err = env.CreateTestUser("jdoe", models.Student, w.north.ID, "1001")
	require.NoError(t, err)
	return w
}

func TestLearningRecordRoutes(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	w := seedLearningRecord(t, env)
	client := noRedirects(env)
	resident := w.claims(w.resident)

	tooLong := make([]byte, 121)
	for i := range tooLong {
		tooLong[i] = 'a'
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   any
		status int
	}{
		{"facilities", http.MethodGet, "/api/learning-record/facilities", nil, http.StatusOK},
		{"entries_empty", http.MethodGet, "/api/learning-record/entries", nil, http.StatusOK},
		{"entry_create_both_facility_fields", http.MethodPost, "/api/learning-record/entries",
			map[string]any{"facility_id": 1, "facility_other": "Community Center"}, http.StatusBadRequest},
		{"entry_create_bad_facility_id", http.MethodPost, "/api/learning-record/entries",
			map[string]any{"facility_id": 9999}, http.StatusBadRequest},
		{"entry_create_facility_other_too_long", http.MethodPost, "/api/learning-record/entries",
			map[string]any{"facility_other": string(tooLong)}, http.StatusBadRequest},
		{"entry_create", http.MethodPost, "/api/learning-record/entries",
			map[string]any{"client_id": "entry-1", "program_name": "GED Prep", "facility_id": w.north.ID, "completion_date": "2026-01-15", "confidence": "high", "summary": "Finished the program."},
			http.StatusCreated},
		{"entry_create_facility_other", http.MethodPost, "/api/learning-record/entries",
			map[string]any{"client_id": "entry-2", "program_name": "Peer Mentoring", "facility_other": "Community Center", "completion_date": "2026-02-01"},
			http.StatusCreated},
		{"entries_after_creates", http.MethodGet, "/api/learning-record/entries", nil, http.StatusOK},
		{"entry_update", http.MethodPut, "/api/learning-record/entries/1",
			map[string]any{"program_name": "GED Prep (Advanced)", "facility_other": "New Location", "completion_date": "2026-01-20"},
			http.StatusOK},
		{"entry_update_bad_id", http.MethodPut, "/api/learning-record/entries/abc", map[string]any{}, http.StatusBadRequest},
		{"entry_delete", http.MethodDelete, "/api/learning-record/entries/1", nil, http.StatusOK},
		{"entries_after_delete", http.MethodGet, "/api/learning-record/entries", nil, http.StatusOK},
		{"draft_get_none", http.MethodGet, "/api/learning-record/draft", nil, http.StatusOK},
		// draft_upsert (PUT /api/learning-record/draft) is a pre-existing,
		// pinned-as-broken route; see README.md's Tests section.
		{"draft_upsert", http.MethodPut, "/api/learning-record/draft",
			map[string]any{"client_id": "draft-upsert", "program_name": "Welding Basics"}, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := NewRequest[any](client, t, tt.method, tt.path, tt.body).
				WithTestClaims(resident).AsRaw().Do().
				ExpectStatus(tt.status)
			expectGolden(t, "learning_record", tt.name, resp.rawBody)
		})
	}

	// The GET and DELETE draft routes don't go through UpsertDraft, so a
	// draft is seeded directly to cover them independently of that route.
	draft := learningrecord.LearningRecordEntry{UserID: w.resident.ID, ClientID: "draft-1", IsDraft: true, ProgramName: "Welding Basics", StepIndex: 1, UiPhase: "survey"}
	require.NoError(t, env.DB.Create(&draft).Error)
	for _, tt := range []struct {
		name   string
		method string
		path   string
		status int
	}{
		{"draft_get_after_seed", http.MethodGet, "/api/learning-record/draft", http.StatusOK},
		{"draft_delete_missing_client_id", http.MethodDelete, "/api/learning-record/draft", http.StatusBadRequest},
		{"draft_delete", http.MethodDelete, "/api/learning-record/draft?client_id=draft-1", http.StatusOK},
		{"draft_get_after_delete", http.MethodGet, "/api/learning-record/draft", http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp := NewRequest[any](client, t, tt.method, tt.path, nil).
				WithTestClaims(resident).AsRaw().Do().
				ExpectStatus(tt.status)
			expectGolden(t, "learning_record", tt.name, resp.rawBody)
		})
	}

	// The HTTP response never shows who recorded the deletion; the audit
	// trail (written through Store.WithUserContext, which records the actor
	// from the request's identity claims) does.
	t.Run("entry_delete_records_audit_history", func(t *testing.T) {
		var history struct {
			Action           string
			AdminID          *uint
			AttendanceStatus *string
		}
		err := env.DB.Table("user_account_history").
			Select("action, admin_id, attendance_status").
			Where("user_id = ? AND action = ?", w.resident.ID, "learning_record_deleted").
			Order("created_at DESC").Limit(1).Scan(&history).Error
		require.NoError(t, err)
		require.Equal(t, "learning_record_deleted", history.Action)
		require.NotNil(t, history.AdminID)
		require.Equal(t, w.resident.ID, *history.AdminID)
		// Core wrote this column's zero value before the move; a module struct
		// that drops the field writes NULL instead, changing the account-history
		// response body.
		require.NotNil(t, history.AttendanceStatus)
		require.Equal(t, "", *history.AttendanceStatus)
	})

	// The assertion above reads the row directly; this reads the endpoint
	// that serves it to an admin, the real consumer of attendance_status, so
	// both boundaries are pinned (the same two-level pin programs' test uses).
	t.Run("account_history_endpoint", func(t *testing.T) {
		admin, err := env.CreateTestUser("lradmin", models.FacilityAdmin, w.north.ID, "")
		require.NoError(t, err)
		resp := NewRequest[any](client, t, http.MethodGet,
			fmt.Sprintf("/api/users/%d/account-history", w.resident.ID), nil).
			WithTestClaims(w.claims(admin)).AsRaw().Do().
			ExpectStatus(http.StatusOK)
		expectGolden(t, "learning_record", "account_history", resp.rawBody)
	})
}

// TestLearningRecordRouteAccess pins each route's access level and feature
// flag against every route this module registers.
func TestLearningRecordRouteAccess(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()
	w := seedLearningRecord(t, env)
	client := noRedirects(env)

	routes := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/learning-record/facilities"},
		{http.MethodGet, "/api/learning-record/entries"},
		{http.MethodPost, "/api/learning-record/entries"},
		{http.MethodPut, "/api/learning-record/entries/1"},
		{http.MethodDelete, "/api/learning-record/entries/1"},
		{http.MethodGet, "/api/learning-record/draft"},
		{http.MethodPut, "/api/learning-record/draft"},
		{http.MethodDelete, "/api/learning-record/draft"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			// Every route requires the learning_record feature.
			NewRequest[any](client, t, rt.method, rt.path, map[string]any{}).
				WithTestClaims(withFeatures(w.claims(w.resident), models.LearningRecordAccess)).Do().
				ExpectStatus(http.StatusUnauthorized).ExpectMessage("Feature not enabled")

			// Every route is anyUser: a resident with the feature gets past
			// auth. The body isn't valid JSON; a route that reads one
			// (POST/PUT) rejects it before doing anything else. DELETE never
			// reads a body, so it still runs its normal delete here.
			resp := NewRequest[any](client, t, rt.method, rt.path, "not an object").
				WithTestClaims(w.claims(w.resident)).AsRaw().Do()
			require.NotEqual(t, http.StatusUnauthorized, resp.resp.StatusCode, resp.rawBody)
		})
	}
}
