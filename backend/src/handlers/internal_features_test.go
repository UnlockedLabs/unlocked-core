package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleInternalFeatures(t *testing.T) {
	srv := newTestingServer()
	// Statewide defaults as a fresh install has them: learning_record and
	// ai_tutor off, everything else on.
	statewide := []models.FeatureAccess{
		models.OpenContentAccess, models.RequestContentAccess, models.HelpfulLinksAccess, models.UploadVideoAccess,
		models.ProviderAccess,
		models.ProgramAccess, models.ResidentProgramsAccess,
	}
	setStatewideDefaults(t, srv, statewide...)
	srv.registerInternalRoutes()

	tests := []struct {
		name       string
		overrides  map[models.FeatureAccess]bool
		query      string // {id} is replaced with the facility created for the case
		wantStatus int
		want       []models.FeatureAccess
	}{
		{
			name:       "no overrides returns the statewide defaults",
			query:      "facility_id={id}",
			wantStatus: http.StatusOK,
			want:       statewide,
		},
		{
			name:       "an override turns on a feature that is off statewide",
			overrides:  map[models.FeatureAccess]bool{models.LearningRecordAccess: true},
			query:      "facility_id={id}",
			wantStatus: http.StatusOK,
			want:       append([]models.FeatureAccess{models.LearningRecordAccess}, statewide...),
		},
		{
			name:       "turning off a parent turns off its sub-features",
			overrides:  map[models.FeatureAccess]bool{models.OpenContentAccess: false},
			query:      "facility_id={id}",
			wantStatus: http.StatusOK,
			want:       []models.FeatureAccess{models.ProviderAccess, models.ProgramAccess, models.ResidentProgramsAccess},
		},
		{
			// Core reports a missing record as 400 (database.NewDBError).
			name:       "an unknown facility is rejected",
			query:      "facility_id=999999",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "a non-numeric facility id is rejected",
			query:      "facility_id=abc",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "a missing facility id is rejected",
			query:      "",
			wantStatus: http.StatusBadRequest,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facility := models.Facility{Name: "Internal Features " + strconv.Itoa(i), Timezone: "America/Chicago"}
			require.NoError(t, srv.Db.Create(&facility).Error)
			args := models.QueryContext{Ctx: context.Background()}
			for feature, enabled := range tt.overrides {
				require.NoError(t, srv.Db.UpsertFacilityFeatureFlag(&args, facility.ID, feature, enabled))
			}

			query := strings.ReplaceAll(tt.query, "{id}", strconv.Itoa(int(facility.ID)))
			w := httptest.NewRecorder()
			srv.Mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/v1/features?"+query, nil))

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			if tt.wantStatus != http.StatusOK {
				return
			}
			var resp models.Resource[internalFeaturesResponse]
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, facility.ID, resp.Data.FacilityID)
			assert.ElementsMatch(t, tt.want, resp.Data.Features)
		})
	}
}

func TestHandleInternalRegisterFeature(t *testing.T) {
	srv := newTestingServer()
	srv.registerInternalRoutes()
	put := func(name, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.Mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/internal/v1/features/"+name, strings.NewReader(body)))
		return w
	}
	find := func(name models.FeatureAccess) (models.CatalogFeature, bool) {
		catalog, err := srv.Db.FeatureCatalog()
		require.NoError(t, err)
		return catalog.Find(name)
	}
	facility := models.Facility{Name: "Register Feature", Timezone: "America/Chicago"}
	require.NoError(t, srv.Db.Create(&facility).Error)

	t.Run("a new feature is created and starts off", func(t *testing.T) {
		w := put("my_tool", `{"title":" My Tool ","description":"  "}`)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

		f, ok := find("my_tool")
		require.True(t, ok)
		assert.Equal(t, "My Tool", f.Title)
		assert.Nil(t, f.Description, "a blank description is stored as NULL")
		assert.False(t, f.DefaultEnabled)
		assert.Empty(t, f.Parent)
		effective, err := srv.Db.GetFacilityFeatureAccess(facility.ID)
		require.NoError(t, err)
		assert.NotContains(t, effective, models.FeatureAccess("my_tool"))
	})

	t.Run("registering again changes only the text", func(t *testing.T) {
		args := models.QueryContext{Ctx: context.Background()}
		require.NoError(t, srv.Db.UpsertFacilityFeatureFlag(&args, facility.ID, "my_tool", true))
		require.NoError(t, srv.Db.Model(&models.FeatureFlags{}).Where("name = ?", "my_tool").Update("default_enabled", true).Error)

		w := put("my_tool", `{"title":"My Tool 2","description":"Does things"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		f, ok := find("my_tool")
		require.True(t, ok)
		assert.Equal(t, "My Tool 2", f.Title)
		require.NotNil(t, f.Description)
		assert.Equal(t, "Does things", *f.Description)
		assert.True(t, f.DefaultEnabled, "registering never changes the default")
		effective, err := srv.Db.GetFacilityFeatureAccess(facility.ID)
		require.NoError(t, err)
		assert.Contains(t, effective, models.FeatureAccess("my_tool"), "registering never changes a facility's setting")
	})

	t.Run("a core feature's default is left alone too", func(t *testing.T) {
		w := put(string(models.AiTutorAccess), `{"title":"AI Tutor"}`)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		f, ok := find(models.AiTutorAccess)
		require.True(t, ok)
		assert.False(t, f.DefaultEnabled)
	})

	tests := []struct {
		name, feature, body string
		wantStatus          int
	}{
		{"longest name", strings.Repeat("a", 64), `{"title":"Long"}`, http.StatusCreated},
		{"longest title", "long_title", `{"title":"` + strings.Repeat("t", 100) + `"}`, http.StatusCreated},
		{"uppercase", "My_Tool", `{"title":"x"}`, http.StatusBadRequest},
		{"dash", "my-tool", `{"title":"x"}`, http.StatusBadRequest},
		{"leading underscore", "_tool", `{"title":"x"}`, http.StatusBadRequest},
		{"leading digit", "1tool", `{"title":"x"}`, http.StatusBadRequest},
		{"name too long", strings.Repeat("a", 65), `{"title":"x"}`, http.StatusBadRequest},
		{"no title", "no_title", `{}`, http.StatusBadRequest},
		{"blank title", "blank_title", `{"title":"   "}`, http.StatusBadRequest},
		{"title too long", "big_title", `{"title":"` + strings.Repeat("t", 101) + `"}`, http.StatusBadRequest},
		{"not json", "bad_body", `title`, http.StatusBadRequest},
		{"body too large", "huge_body", `{"title":"x","description":"` + strings.Repeat("d", 17*1024) + `"}`, http.StatusBadRequest},
		{"a sub-feature's name", string(models.UploadVideoAccess), `{"title":"x"}`, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, existed := find(models.FeatureAccess(tt.feature))
			w := put(tt.feature, tt.body)
			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			after, exists := find(models.FeatureAccess(tt.feature))
			if tt.wantStatus == http.StatusCreated {
				assert.True(t, exists)
				return
			}
			assert.Equal(t, existed, exists, "a refused request adds nothing")
			assert.Equal(t, before, after, "a refused request changes nothing")
		})
	}
}
