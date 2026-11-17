package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const orphanID = "d02a3551-1111-2222-3333-444444444444"

func newRecoveryFixture(t *testing.T) (*Server, *kratosFake, *models.User) {
	t.Helper()
	srv := newTestingServer()
	k := fakeKratos(t, srv, http.StatusCreated)
	north := createTestFacility(t, srv, "North")
	user := createTestUser(t, srv, "calisio", models.FacilityAdmin, north)
	return srv, k, user
}

// An identity nobody owns is set aside, not deleted, so the new user can have
// the username and the old account stays on record in Kratos.
func TestCreateKratosQuarantinesUnownedIdentity(t *testing.T) {
	srv, k, user := newRecoveryFixture(t)
	k.addIdentity(orphanID, "calisio")

	require.NoError(t, srv.HandleCreateUserKratosRecovering(user.Username, "Temp-pass-1"))

	orphan := k.identity(orphanID)
	require.NotNil(t, orphan, "the orphan must stay in Kratos")
	assert.Equal(t, "inactive", orphan["state"])
	assert.Equal(t, "calisio.orphaned.d02a3551", orphan["traits"].(map[string]any)["username"])
	assert.Equal(t, "Temp-pass-1", k.password(t))
	stored, err := srv.Db.GetUserByID(user.ID)
	require.NoError(t, err)
	assert.Equal(t, "kratos-1", models.FormatNullableString(stored.KratosID))
	assert.Equal(t, []string{
		"POST /admin/identities",
		"GET /admin/identities",
		"PUT /admin/identities/" + orphanID,
		"POST /admin/identities",
	}, k.calls[:4], "quarantine happens before the second create")
}

// An identity a live user holds must never be touched.
func TestCreateKratosLeavesOwnedIdentityAlone(t *testing.T) {
	srv, k, user := newRecoveryFixture(t)
	k.addIdentity(orphanID, "calisio")
	owner := createTestUser(t, srv, "someone.else", models.Student, user.FacilityID)
	owner.KratosID = models.StringPtr(orphanID)
	require.NoError(t, srv.Db.UpdateUser(owner))

	err := srv.HandleCreateUserKratosRecovering(user.Username, "Temp-pass-1")

	assert.ErrorIs(t, err, errKratosIdentityOwned)
	assert.Equal(t, "active", k.identity(orphanID)["state"])
	for _, call := range k.calls {
		assert.NotContains(t, call, "PUT", "no identity may be modified")
	}
}

// A 409 with no identity to explain it returns the create error unchanged.
func TestCreateKratosConflictWithNoIdentityFound(t *testing.T) {
	srv, k, user := newRecoveryFixture(t)
	k.addIdentity(orphanID, "calisio")
	k.hideFromList = true

	err := srv.HandleCreateUserKratosRecovering(user.Username, "Temp-pass-1")

	assert.Error(t, err)
	assert.NotErrorIs(t, err, errKratosIdentityOwned)
	for _, call := range k.calls {
		assert.NotContains(t, call, "PUT")
	}
}

// Startup seeding and the other plain callers keep their behavior: a conflict
// is an error, and nothing in Kratos is changed.
func TestCreateKratosWithoutRecoveryDoesNotQuarantine(t *testing.T) {
	srv, k, user := newRecoveryFixture(t)
	k.addIdentity(orphanID, "calisio")

	assert.Error(t, srv.HandleCreateUserKratos(user.Username, "Temp-pass-1"))

	assert.Equal(t, "active", k.identity(orphanID)["state"])
	assert.Equal(t, []string{"POST /admin/identities"}, k.calls)
}

// A user whose Kratos identity was never stored (the demo calisio case) can be
// reset: reset quarantines the orphan and creates the identity.
func TestResetPasswordRecoversUserWithNoKratosID(t *testing.T) {
	srv, k, user := newRecoveryFixture(t)
	k.addIdentity(orphanID, "calisio")
	admin := createTestUser(t, srv, "root", models.SystemAdmin, user.FacilityID)
	req := httptest.NewRequest(http.MethodPost, "/api/users/1/student-password", nil)
	req.SetPathValue("id", strconv.Itoa(int(user.ID)))
	req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{UserID: admin.ID}))
	w := httptest.NewRecorder()

	require.NoError(t, srv.handleResetStudentPassword(w, req, sLog{f: logrus.Fields{}}))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "inactive", k.identity(orphanID)["state"])
	stored, err := srv.Db.GetUserByID(user.ID)
	require.NoError(t, err)
	assert.Equal(t, "kratos-1", models.FormatNullableString(stored.KratosID))
	var resp struct {
		Data map[string]string `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, resp.Data["temp_password"], k.password(t))
}

// Bulk reset shares the recovery, so a user with no stored identity is reset
// instead of reported as failed.
func TestBulkResetRecoversUserWithNoKratosID(t *testing.T) {
	srv, k, user := newRecoveryFixture(t)
	k.addIdentity(orphanID, "calisio")
	admin := createTestUser(t, srv, "root", models.SystemAdmin, user.FacilityID)
	body := fmt.Sprintf(`{"user_ids":[%d]}`, user.ID)
	req := httptest.NewRequest(http.MethodPost, "/api/users/student-password/bulk", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{UserID: admin.ID, Role: models.SystemAdmin}))
	w := httptest.NewRecorder()

	require.NoError(t, srv.handleBulkResetPassword(w, req, sLog{f: logrus.Fields{}}))

	assert.Equal(t, "inactive", k.identity(orphanID)["state"])
	stored, err := srv.Db.GetUserByID(user.ID)
	require.NoError(t, err)
	assert.Equal(t, "kratos-1", models.FormatNullableString(stored.KratosID))
}
