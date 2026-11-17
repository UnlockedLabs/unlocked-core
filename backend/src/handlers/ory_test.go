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

	ory "github.com/ory/kratos-client-go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const orphanID = "d02a3551-1111-2222-3333-444444444444"

func newKratosServer(t *testing.T) (*Server, *kratosFake, uint) {
	t.Helper()
	srv := newTestingServer()
	k := fakeKratos(t, srv, http.StatusCreated)
	return srv, k, createTestFacility(t, srv, "North")
}

func newRecoveryFixture(t *testing.T) (*Server, *kratosFake, *models.User) {
	t.Helper()
	srv, k, north := newKratosServer(t)
	return srv, k, createTestUser(t, srv, "calisio", models.FacilityAdmin, north)
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

func createUserRequest(t *testing.T, srv *Server, facilityID uint, username string) (*httptest.ResponseRecorder, error) {
	t.Helper()
	admin := createTestUser(t, srv, "root", models.SystemAdmin, facilityID)
	body := fmt.Sprintf(`{"user":{"username":%q,"name_first":"Cal","name_last":"Isio","email":"c@example.com","role":"facility_admin","facility_id":%d}}`, username, facilityID)
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{UserID: admin.ID, Role: models.SystemAdmin}))
	w := httptest.NewRecorder()
	return w, srv.handleCreateUser(w, req, sLog{f: logrus.Fields{}})
}

// An identity a live user holds blocks the username before anything is
// inserted, so no row and no unusable temp password come out of it.
func TestCreateUserRefusesUsernameHeldByLiveIdentity(t *testing.T) {
	srv, k, facilityID := newKratosServer(t)
	k.addIdentity(orphanID, "calisio")
	owner := createTestUser(t, srv, "someone.else", models.Student, facilityID)
	owner.KratosID = models.StringPtr(orphanID)
	require.NoError(t, srv.Db.UpdateUser(owner))

	w, err := createUserRequest(t, srv, facilityID, "calisio")

	var svcErr serviceError
	require.ErrorAs(t, err, &svcErr)
	assert.Equal(t, http.StatusBadRequest, svcErr.Status)
	assert.Equal(t, "That username is unavailable. Please choose a different username.", svcErr.Message)
	assert.NotContains(t, strings.ToLower(svcErr.Message), "kratos")
	assert.Empty(t, w.Body.String(), "no temp password")
	_, err = srv.Db.GetUserByUsername("calisio")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Equal(t, []string{"GET /admin/identities"}, k.calls)
}

// An orphan does not block creation: the admin gets a working temp password.
func TestCreateUserRecoversFromOrphanedIdentity(t *testing.T) {
	srv, k, facilityID := newKratosServer(t)
	k.addIdentity(orphanID, "calisio")

	w, err := createUserRequest(t, srv, facilityID, "calisio")

	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, w.Code)
	var resp struct {
		Data struct {
			TempPassword     string `json:"temp_password"`
			KratosRegistered bool   `json:"kratos_registered"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.True(t, resp.Data.KratosRegistered)
	assert.Equal(t, resp.Data.TempPassword, k.password(t))
	assert.Equal(t, "inactive", k.identity(orphanID)["state"])
}

func resetRequest(t *testing.T, srv *Server, user *models.User) error {
	t.Helper()
	admin := createTestUser(t, srv, "root", models.SystemAdmin, user.FacilityID)
	req := httptest.NewRequest(http.MethodPost, "/api/users/1/student-password", nil)
	req.SetPathValue("id", strconv.Itoa(int(user.ID)))
	req = req.WithContext(context.WithValue(req.Context(), ClaimsKey, &Claims{UserID: admin.ID}))
	return srv.handleResetStudentPassword(httptest.NewRecorder(), req, sLog{f: logrus.Fields{}})
}

// Every way a reset can fail shows the admin the same plain message.
func TestResetPasswordFailureMessage(t *testing.T) {
	want := "Unable to reset password at this time. Please try again later."
	assertPlain := func(t *testing.T, err error) {
		t.Helper()
		var svcErr serviceError
		require.ErrorAs(t, err, &svcErr)
		assert.Equal(t, want, svcErr.Message)
	}

	t.Run("kratos refuses to create the identity", func(t *testing.T) {
		srv, _, user := newRecoveryFixture(t)
		fakeKratos(t, srv, http.StatusInternalServerError)
		assertPlain(t, resetRequest(t, srv, user))
	})
	t.Run("a live user holds the identity", func(t *testing.T) {
		srv, k, user := newRecoveryFixture(t)
		k.addIdentity(orphanID, "calisio")
		owner := createTestUser(t, srv, "someone.else", models.Student, user.FacilityID)
		owner.KratosID = models.StringPtr(orphanID)
		require.NoError(t, srv.Db.UpdateUser(owner))
		assertPlain(t, resetRequest(t, srv, user))
	})
	t.Run("kratos is unreachable for a registered user", func(t *testing.T) {
		srv, _, user := newRecoveryFixture(t)
		user.KratosID = models.StringPtr("kratos-1")
		require.NoError(t, srv.Db.UpdateUser(user))
		cfg := ory.NewConfiguration()
		cfg.Servers = ory.ServerConfigurations{{URL: "http://127.0.0.1:1"}}
		srv.OryClient = ory.NewAPIClient(cfg)
		assertPlain(t, resetRequest(t, srv, user))
	})
}
