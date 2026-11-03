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
