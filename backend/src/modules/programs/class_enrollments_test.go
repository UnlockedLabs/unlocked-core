package programs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TimePtr(t time.Time) *time.Time {
	return &t
}

func TestHandleEnrollUsersInClass_Validation(t *testing.T) {
	srv := newTestServer(t)

	// Setup Common Data
	facility := Facility{Name: "Test Facility"}
	if err := srv.store.Create(&facility).Error; err != nil {
		t.Fatal(err)
	}

	program := Program{Name: "Test Program"}
	if err := srv.store.Create(&program).Error; err != nil {
		t.Fatal(err)
	}

	activeUser := User{Username: "active_user", Email: "active_user@example.com", NameFirst: "Active", NameLast: "User", FacilityID: facility.ID}
	if err := srv.store.Create(&activeUser).Error; err != nil {
		t.Fatal(err)
	}

	activeUser2 := User{Username: "active_user_2", Email: "active_user_2@example.com", NameFirst: "Active", NameLast: "User2", FacilityID: facility.ID}
	if err := srv.store.Create(&activeUser2).Error; err != nil {
		t.Fatal(err)
	}

	deactivatedUser := User{Username: "deactivated_user", Email: "deactivated_user@example.com", NameFirst: "Deactivated", NameLast: "User", FacilityID: facility.ID, DeactivatedAt: TimePtr(time.Now())}
	if err := srv.store.Create(&deactivatedUser).Error; err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name           string
		classStatus    ClassStatus
		isArchived     bool
		targetUsers    []User
		capacity       int64
		setupClass     func(*ProgramClassCohort) // Optional extra setup
		expectedStatus int
		expectedMsg    string
	}{
		{
			name:           "Enroll in Active Class (Success)",
			classStatus:    Active,
			targetUsers:    []User{activeUser},
			capacity:       10,
			expectedStatus: http.StatusCreated,
			expectedMsg:    "users enrolled",
		},
		{
			name:           "Enroll in Scheduled Class (Success)",
			classStatus:    Scheduled,
			targetUsers:    []User{activeUser},
			capacity:       10,
			expectedStatus: http.StatusCreated,
			expectedMsg:    "users enrolled",
		},
		{
			name:           "Enroll in Completed Class (Failure)",
			classStatus:    Completed,
			targetUsers:    []User{activeUser},
			capacity:       10,
			expectedStatus: http.StatusBadRequest,
			expectedMsg:    "cannot perform action on class that is completed cancelled or archived",
		},
		{
			name:           "Enroll in Cancelled Class (Failure)",
			classStatus:    Cancelled,
			targetUsers:    []User{activeUser},
			capacity:       10,
			expectedStatus: http.StatusBadRequest,
			expectedMsg:    "cannot perform action on class that is completed cancelled or archived",
		},
		{
			name:           "Enroll in Archived Class (Failure)",
			classStatus:    Active,
			isArchived:     true,
			targetUsers:    []User{activeUser},
			capacity:       10,
			expectedStatus: http.StatusBadRequest,
			expectedMsg:    "cannot perform action on class that is completed cancelled or archived",
		},
		{
			name:           "Enroll Deactivated User (Failure)",
			classStatus:    Active,
			targetUsers:    []User{deactivatedUser},
			capacity:       10,
			expectedStatus: http.StatusBadRequest,
			expectedMsg:    "deactivated user",
		},
		{
			name:        "Enroll in Full Class (Partial/Skipped)",
			classStatus: Active,
			targetUsers: []User{activeUser, activeUser2},
			capacity:    1, // Capacity 1, trying to enroll 2 users
			setupClass: func(c *ProgramClassCohort) {
			},
			expectedStatus: http.StatusCreated,
			expectedMsg:    "1 users were enrolled, 1 were not added because capacity is full.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class := ProgramClassCohort{
				ProgramID:   program.ID,
				FacilityID:  facility.ID,
				Status:      tt.classStatus,
				Description: "Description",
				Capacity:    tt.capacity,
				StartDt:     time.Now(),
			}
			if tt.isArchived {
				class.ArchivedAt = TimePtr(time.Now())
			}
			if err := srv.store.Create(&class).Error; err != nil {
				t.Fatal(err)
			}

			if tt.setupClass != nil {
				tt.setupClass(&class)
			}

			userIDs := make([]int, len(tt.targetUsers))
			for i, u := range tt.targetUsers {
				userIDs[i] = int(u.ID)
			}

			enrollmentReq := struct {
				UserIDs []int `json:"user_ids"`
			}{
				UserIDs: userIDs,
			}
			body, _ := json.Marshal(enrollmentReq)

			req := httptest.NewRequest(http.MethodPost, "/api/program-classes/"+strconv.Itoa(int(class.ID))+"/enrollments", bytes.NewReader(body))
			req.SetPathValue("cohort_id", strconv.Itoa(int(class.ID)))

			w := httptest.NewRecorder()
			log := sLog{ctx: req.Context(), f: make(map[string]interface{})}

			err := srv.handleEnrollUsersInClass(w, req, log)

			if tt.expectedStatus >= 400 {
				assert.Error(t, err)
				svcErr, ok := err.(serviceError)
				assert.True(t, ok)
				assert.Equal(t, tt.expectedStatus, svcErr.Status)
				assert.Contains(t, svcErr.Message, tt.expectedMsg)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedStatus, w.Code)
				// For 201, the response body is a JSON object with a message
				var resp resource[any]
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				assert.NoError(t, err)
				assert.Contains(t, resp.Message, tt.expectedMsg)
			}
		})
	}
}

// A completion records the email of the admin who graduated the resident,
// read from the users table as core's claims had it.
func TestGraduationRecordsAdminEmail(t *testing.T) {
	srv := newTestServer(t)
	facility := Facility{Name: "North", Timezone: "America/Chicago"}
	require.NoError(t, srv.store.Create(&facility).Error)
	admin := User{Username: "facadmin", Email: "facadmin@example.com", Role: users.FacilityAdmin, FacilityID: facility.ID}
	require.NoError(t, srv.store.Create(&admin).Error)
	resident := User{Username: "resident", Email: "resident@example.com", Role: users.Student, FacilityID: facility.ID}
	require.NoError(t, srv.store.Create(&resident).Error)
	program := Program{Name: "GED"}
	require.NoError(t, srv.store.Create(&program).Error)
	class := ProgramClassCohort{ProgramID: program.ID, FacilityID: facility.ID, Status: Active, Description: "Description", Capacity: 10, StartDt: time.Now()}
	require.NoError(t, srv.store.Create(&class).Error)
	require.NoError(t, srv.store.Create(&ProgramClassEnrollment{CohortID: class.ID, UserID: resident.ID, EnrollmentStatus: Enrolled}).Error)

	body, err := json.Marshal(map[string]any{"enrollment_status": "Completed", "user_ids": []uint{resident.ID}})
	require.NoError(t, err)
	cohortID := strconv.Itoa(int(class.ID))
	req := httptest.NewRequest(http.MethodPatch, "/api/program-classes/"+cohortID+"/enrollments", bytes.NewReader(body))
	req.SetPathValue("cohort_id", cohortID)
	req = req.WithContext(identity.NewContext(req.Context(), &identity.Claims{UserID: admin.ID, Role: admin.Role, FacilityID: facility.ID}))
	w := httptest.NewRecorder()
	require.NoError(t, srv.handleUpdateProgramClassEnrollments(w, req, sLog{ctx: req.Context(), f: map[string]any{}}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var completion ClassCompletion
	require.NoError(t, srv.store.Where("user_id = ?", resident.ID).First(&completion).Error)
	assert.Equal(t, "facadmin@example.com", completion.AdminEmail)
}
