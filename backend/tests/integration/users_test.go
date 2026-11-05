package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/stretchr/testify/require"
)

func TestCreateUserHandler(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	t.Run("Create user succeeds", func(t *testing.T) {
		facility, err := env.CreateTestFacility("Test Facility")
		require.NoError(t, err)

		var form struct {
			User      models.User `json:"user"`
			Providers []int       `json:"provider_platforms"`
		}

		form.User = models.User{
			Username:   "testuser",
			NameFirst:  "Test",
			NameLast:   "User",
			Role:       models.Student,
			Email:      "testuser@example.com",
			DocID:      models.StringPtr("123456789"),
			FacilityID: facility.ID,
		}

		resp := NewRequest[struct {
			User models.User `json:"user"`
		}](env.Client, t, http.MethodPost, "/api/users", form).
			WithTestClaims(&handlers.Claims{Role: models.SystemAdmin}).
			Do().
			ExpectStatus(http.StatusCreated)

		got := resp.GetData().User

		require.NotZero(t, got.ID)
		require.Equal(t, form.User.Username, got.Username)
		require.Equal(t, form.User.NameFirst, got.NameFirst)
		require.Equal(t, form.User.NameLast, got.NameLast)
		require.Equal(t, form.User.Role, got.Role)
		require.Equal(t, form.User.Email, got.Email)
		require.Equal(t, form.User.DocID, got.DocID)
		require.Equal(t, form.User.FacilityID, got.FacilityID)
	})

	t.Run("switch-capable admin must specify a facility", func(t *testing.T) {
		var form struct {
			User      models.User `json:"user"`
			Providers []int       `json:"provider_platforms"`
		}

		form.User = models.User{
			Username:  "nofacuser",
			NameFirst: "No",
			NameLast:  "Facility",
			Role:      models.Student,
			Email:     "nofacuser@example.com",
			DocID:     models.StringPtr("987654321"),
			// FacilityID intentionally omitted: a statewide admin has no ambient
			// facility, so creation must be rejected rather than defaulting.
		}

		NewRequest[struct {
			User models.User `json:"user"`
		}](env.Client, t, http.MethodPost, "/api/users", form).
			WithTestClaims(&handlers.Claims{Role: models.DepartmentAdmin}).
			Do().
			ExpectStatus(http.StatusBadRequest)
	})

	// Residents must carry a resident ID (ID-835); admins share models.User and
	// legitimately have none, so the rule is scoped by role.
	t.Run("resident ID requirement", func(t *testing.T) {
		facility, err := env.CreateTestFacility("Resident ID Facility")
		require.NoError(t, err)

		tests := []struct {
			name       string
			username   string
			role       models.UserRole
			docID      string
			wantStatus int
		}{
			{"resident without a resident ID is rejected", "residnone", models.Student, "", http.StatusBadRequest},
			{"whitespace-only resident ID is rejected", "residblank", models.Student, "   ", http.StatusBadRequest},
			{"over-long resident ID is rejected", "residlong", models.Student, strings.Repeat("9", 33), http.StatusBadRequest},
			{"admin without a resident ID is allowed", "residadmin", models.FacilityAdmin, "", http.StatusCreated},
			{"resident with a resident ID is allowed", "residok", models.Student, "RID-0001", http.StatusCreated},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var form struct {
					User      models.User `json:"user"`
					Providers []int       `json:"provider_platforms"`
				}
				form.User = models.User{
					Username:   tt.username,
					NameFirst:  "Test",
					NameLast:   "User",
					Role:       tt.role,
					Email:      tt.username + "@example.com",
					DocID:      models.NilIfBlank(tt.docID),
					FacilityID: facility.ID,
				}

				NewRequest[struct {
					User models.User `json:"user"`
				}](env.Client, t, http.MethodPost, "/api/users", form).
					WithTestClaims(&handlers.Claims{Role: models.SystemAdmin}).
					Do().
					ExpectStatus(tt.wantStatus)
			})
		}
	})

	// The provider-sync imports (Provider Platforms' user import and matching,
	// through module.Users.Create) build residents with no resident ID and go
	// straight to the DB layer. This pins that escape hatch so the check isn't
	// "helpfully" moved into db.CreateUser, which would break those flows.
	t.Run("db layer still accepts a resident with no resident ID", func(t *testing.T) {
		facility, err := env.CreateTestFacility("Provider Import Facility")
		require.NoError(t, err)

		_, err = env.CreateTestUser("importedresident", models.Student, facility.ID, "")
		require.NoError(t, err)
	})

	t.Run("resident ID already in use", func(t *testing.T) {
		facility, err := env.CreateTestFacility("Duplicate ID Facility")
		require.NoError(t, err)
		_, err = env.CreateTestUser("crtowner", models.Student, facility.ID, "RID-4000")
		require.NoError(t, err)

		tests := []struct {
			name     string
			username string
			docID    string
		}{
			{"same ID", "crtsame", "RID-4000"},
			{"different case and spaces", "crtcase", "  rid-4000 "},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				var form struct {
					User      models.User `json:"user"`
					Providers []int       `json:"provider_platforms"`
				}
				form.User = models.User{
					Username:   tt.username,
					NameFirst:  "Test",
					NameLast:   "User",
					Role:       models.Student,
					Email:      tt.username + "@example.com",
					DocID:      models.StringPtr(tt.docID),
					FacilityID: facility.ID,
				}

				NewRequest[struct {
					User models.User `json:"user"`
				}](env.Client, t, http.MethodPost, "/api/users", form).
					WithTestClaims(&handlers.Claims{Role: models.SystemAdmin}).
					Do().
					ExpectStatus(http.StatusConflict).
					ExpectMessage("This resident ID is already in use")
			})
		}
	})
}

func TestUpdateUserHandler(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility, err := env.CreateTestFacility("Update User Facility")
	require.NoError(t, err)

	patch := func(t *testing.T, id uint, body map[string]any) *Response[models.User] {
		t.Helper()
		return NewRequest[models.User](env.Client, t, http.MethodPatch, fmt.Sprintf("/api/users/%d", id), body).
			WithTestClaims(&handlers.Claims{Role: models.SystemAdmin}).
			Do()
	}

	// The edit dialog PATCHes name + doc_id only, so the decoded role is always
	// empty — the resident-ID check has to read the role off the stored record
	// or it silently skips (ID-835).
	t.Run("resident keeps its stored resident ID when the patch omits it", func(t *testing.T) {
		user, err := env.CreateTestUser("updkeepsid", models.Student, facility.ID, "RID-1000")
		require.NoError(t, err)

		got := patch(t, user.ID, map[string]any{"name_first": "Renamed"}).
			ExpectStatus(http.StatusOK).
			GetData()

		require.Equal(t, "Renamed", got.NameFirst)
		require.Equal(t, "RID-1000", models.FormatNullableString(got.DocID))
	})

	t.Run("legacy resident with a blank resident ID must supply one", func(t *testing.T) {
		user, err := env.CreateTestUser("updlegacy", models.Student, facility.ID, "")
		require.NoError(t, err)

		patch(t, user.ID, map[string]any{"name_first": "Renamed"}).
			ExpectStatus(http.StatusBadRequest)

		got := patch(t, user.ID, map[string]any{"name_first": "Renamed", "doc_id": "RID-2000"}).
			ExpectStatus(http.StatusOK).
			GetData()

		require.Equal(t, "RID-2000", models.FormatNullableString(got.DocID))
	})

	t.Run("admin with no resident ID can still be updated", func(t *testing.T) {
		admin, err := env.CreateTestUser("updadmin", models.FacilityAdmin, facility.ID, "")
		require.NoError(t, err)

		got := patch(t, admin.ID, map[string]any{"name_first": "Renamed"}).
			ExpectStatus(http.StatusOK).
			GetData()

		require.Equal(t, "Renamed", got.NameFirst)
	})

	// A resident ID belongs to one user across every facility, ignoring case
	// and surrounding spaces.
	t.Run("resident ID already in use", func(t *testing.T) {
		otherFacility, err := env.CreateTestFacility("Other Facility")
		require.NoError(t, err)
		_, err = env.CreateTestUser("updowner", models.Student, otherFacility.ID, "RID-3000")
		require.NoError(t, err)

		tests := []struct {
			name       string
			username   string
			ownID      string
			newID      string
			wantStatus int
		}{
			{"another resident's ID", "updtaken", "RID-3001", "RID-3000", http.StatusConflict},
			{"another resident's ID in different case", "updtakencase", "RID-3002", "rid-3000", http.StatusConflict},
			{"another resident's ID with spaces", "updtakenspace", "RID-3003", "  RID-3000 ", http.StatusConflict},
			{"own ID unchanged", "updown", "RID-3004", "RID-3004", http.StatusOK},
			{"own ID in different case", "updowncase", "RID-3005", "rid-3005", http.StatusOK},
			{"an unused ID", "updunused", "RID-3006", "RID-3999", http.StatusOK},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				user, err := env.CreateTestUser(tt.username, models.Student, facility.ID, tt.ownID)
				require.NoError(t, err)

				resp := patch(t, user.ID, map[string]any{"name_first": "Renamed", "doc_id": tt.newID}).
					ExpectStatus(tt.wantStatus)
				if tt.wantStatus == http.StatusConflict {
					resp.ExpectMessage("This resident ID is already in use")
					stored, err := env.DB.GetUserByID(user.ID)
					require.NoError(t, err)
					require.Equal(t, tt.ownID, models.FormatNullableString(stored.DocID))
				}
			})
		}
	})

	// Sites may already have two residents sharing an ID. Editing one without
	// changing the ID must still work.
	t.Run("resident already sharing an ID can be renamed", func(t *testing.T) {
		_, err := env.CreateTestUser("updshareda", models.Student, facility.ID, "RID-3100")
		require.NoError(t, err)
		user, err := env.CreateTestUser("updsharedb", models.Student, facility.ID, "RID-3100")
		require.NoError(t, err)

		got := patch(t, user.ID, map[string]any{"name_first": "Renamed", "doc_id": "RID-3100"}).
			ExpectStatus(http.StatusOK).
			GetData()

		require.Equal(t, "Renamed", got.NameFirst)
	})
}
