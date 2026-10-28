package providerplatforms

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type CanvasService struct {
	ProviderPlatformID uint
	Client             *http.Client
	BaseURL            string
	Token              string
	AccountID          string
	BaseHeaders        map[string]string
	ClientID           string
	RedirectURI        string
	JobParams          map[string]any
}

func newCanvasService(provider *ProviderPlatform, params map[string]any) *CanvasService {
	token := strings.TrimSpace(provider.AccessKey)
	log.Infof("Canvas access key length: %d, contains newline: %v", len(token), strings.ContainsAny(provider.AccessKey, "\r\n"))
	headers := make(map[string]string)
	headers["Authorization"] = "Bearer " + token
	headers["Accept"] = "application/json"
	return &CanvasService{
		ProviderPlatformID: provider.ID,
		Client:             &http.Client{},
		BaseURL:            provider.BaseUrl,
		Token:              token,
		AccountID:          provider.AccountID,
		BaseHeaders:        headers,
		JobParams:          params,
	}
}

func (srv *CanvasService) SendRequest(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range srv.BaseHeaders {
		req.Header.Add(key, value)
	}
	resp, err := srv.Client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (cs *CanvasService) GetJobParams() map[string]any {
	return cs.JobParams
}

// fetchCanvasUsers retrieves and parses all users from the Canvas API.
// It does not apply any mapping filter — callers decide what to do with the full list.
func (srv *CanvasService) fetchCanvasUsers() ([]ImportUser, error) {
	enrollmentTypes := []string{"student"}
	baseURL := srv.BaseURL + "/api/v1/accounts/" + srv.AccountID + "/users?per_page=1000"
	for _, t := range enrollmentTypes {
		baseURL += "&enrollment_type=" + t
	}
	log.Printf("fetchCanvasUsers url: %v", baseURL)
	resp, err := srv.SendRequest(baseURL)
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		return nil, err
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.Error("Failed to close response body")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Errorf("Canvas users endpoint returned %d: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("canvas API returned status %d", resp.StatusCode)
	}
	raw := make([]map[string]interface{}, 0)
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		log.Errorf("Failed to decode Canvas users: %v", err)
		return nil, err
	}
	result := make([]ImportUser, 0, len(raw))
	for _, user := range raw {
		loginId, ok := user["login_id"].(string)
		if !ok {
			continue
		}
		name := strings.Split(user["name"].(string), " ")
		sortableName := strings.Split(user["sortable_name"].(string), ",")
		nameFirst, nameLast := "", ""
		if len(sortableName) > 1 {
			nameFirst = strings.TrimSpace(sortableName[1])
			nameLast = strings.TrimSpace(sortableName[0])
		} else if len(name) > 1 {
			nameFirst = name[0]
			nameLast = name[1]
		} else {
			nameFirst = user["short_name"].(string)
			nameLast = name[0]
		}
		userId, _ := user["id"].(float64)
		result = append(result, ImportUser{
			ExternalUserID:   fmt.Sprintf("%d", int(userId)),
			ExternalUsername: loginId,
			NameFirst:        nameFirst,
			NameLast:         nameLast,
			Email:            loginId,
			Username:         nameLast + nameFirst,
		})
	}
	return result, nil
}

// GetUsers returns only Canvas users that have not yet been mapped to an UnlockEd user.
func (srv *CanvasService) GetUsers(st *store) ([]ImportUser, error) {
	all, err := srv.fetchCanvasUsers()
	if err != nil {
		return nil, err
	}
	unmapped := make([]ImportUser, 0, len(all))
	for _, u := range all {
		mapped, err := st.isUserMapped(srv.ProviderPlatformID, u.ExternalUserID)
		if err != nil {
			log.Errorf("Error counting provider_user_mappings: %v", err)
			continue
		}
		if mapped {
			log.Printf("User %s already mapped, skipping", u.ExternalUserID)
			continue
		}
		unmapped = append(unmapped, u)
	}
	log.Printf("returning %d unmapped Canvas users", len(unmapped))
	return unmapped, nil
}

// GetAllUsers returns all Canvas users regardless of whether they are already mapped.
// Used by the mapped-users endpoint to resolve canvas display names.
func (srv *CanvasService) GetAllUsers() ([]ImportUser, error) {
	users, err := srv.fetchCanvasUsers()
	if err != nil {
		return nil, err
	}
	log.Printf("returning %d total Canvas users (including mapped)", len(users))
	return users, nil
}

func (srv *CanvasService) ImportCourses(st *store) error {
	url := srv.BaseURL + "/api/v1/accounts/" + srv.AccountID + "/courses?include[]=course_image&include[]=public_description"
	resp, err := srv.SendRequest(url)
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		return err
	}
	fields := log.Fields{"provider": srv.ProviderPlatformID, "Function": "ImportCourses"}
	log.WithFields(fields).Info("importing courses from provider")
	defer func() {
		if resp.Body.Close() != nil {
			log.Error("Failed to close response body")
		}
	}()
	courses := make([]map[string]any, 0)
	err = json.NewDecoder(resp.Body).Decode(&courses)
	if err != nil {
		log.Printf("Failed to decode response: %v", err)
		return err
	}
	for _, course := range courses {
		id := int(course["id"].(float64))
		log.Infof("importing course %d", id)
		exists, err := st.courseExists(srv.ProviderPlatformID, fmt.Sprintf("%d", id))
		if err != nil {
			log.Error("error getting count of provider courses")
			continue
		}
		if exists {
			log.Debug("skipping course id: ", id)
			continue
		}
		totalMilestones := 0
		assignments, err := srv.getCountAssignmentsForCourse(id)
		if err != nil {
			log.Printf("Failed to get assignments for course: %v", err)
		} else {
			log.Printf("total assignments: %d", assignments)
			totalMilestones += assignments
		}
		thumbnailURL := ""
		if course["image_download_url"] != nil {
			thumbnailURL = course["image_download_url"].(string)
		}
		description := course["course_code"].(string)
		progType := "fixed_enrollment"
		is_pub, ok := course["is_public"].(bool)
		if !ok {
			is_pub = false
		}
		if is_pub {
			progType = "open_enrollment"
		}
		unlockedCourse := Course{
			ProviderPlatformID:      srv.ProviderPlatformID,
			Name:                    course["name"].(string),
			AltName:                 nilIfBlank(course["course_code"].(string)),
			ExternalID:              fmt.Sprintf("%d", id),
			ExternalURL:             stringPtr(srv.BaseURL + "/courses/" + fmt.Sprintf("%d", id)),
			Type:                    CourseType(progType),
			OutcomeTypes:            stringPtr("grade, college_credit"),
			Description:             nilIfBlank(description),
			ThumbnailURL:            nilIfBlank(thumbnailURL),
			TotalProgressMilestones: uint(totalMilestones),
		}
		datePattern := "2006-01-02T15:04:05Z"
		if date, ok := course["start_at"].(string); ok {
			unlockedCourse.StartDt = parseDate(date, datePattern)
		}
		if date, ok := course["end_at"].(string); ok {
			unlockedCourse.EndDt = parseDate(date, datePattern)
		}
		if err = st.createCourse(&unlockedCourse); err != nil {
			log.Printf("Failed to create course: %v", err)
			continue
		}
	}
	return nil
}

func getNodesLength(data map[string]interface{}) int {
	if courseData, ok := data["data"].(map[string]interface{}); ok {
		if course, ok := courseData["course"].(map[string]interface{}); ok {
			if assignmentsConnection, ok := course["assignmentsConnection"].(map[string]interface{}); ok {
				if nodes, ok := assignmentsConnection["nodes"].([]interface{}); ok {
					log.Traceln("got length of assignments for course")
					return len(nodes)
				}
			}
			log.Warnln("failed to decode 'assignmentsConnection' from getNodesLength")
		}
		log.Warnln("failed to decode 'course' from getNodesLength")
	}
	log.Warnln("failed to decode 'data' from getNodesLength")
	return 0
}

func (srv *CanvasService) getCountAssignmentsForCourse(courseId int) (int, error) {
	// graphQL query to canvas to get just the count
	query := fmt.Sprintf(`{
		"query": "query { course(id: \"%d\") { assignmentsConnection { nodes { _id } } } }"
	}`, courseId)
	url := srv.BaseURL + "/api/graphql"
	req, err := http.NewRequest("POST", url, strings.NewReader(query))
	if err != nil {
		log.Errorln("error creating graphql request for canvas")
		return 0, err
	}
	log.Debug("Getting count of assignments for course: ", courseId)
	for key, value := range srv.BaseHeaders {
		req.Header.Add(key, value)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client.Do(req)
	if err != nil {
		log.Errorln("error sending graphql request for canvas:", err)
		return 0, err
	}
	if resp.StatusCode != 200 {
		log.Errorln("response from canvas assignments for course failed with code: ", resp.Status)
		return 0, err
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.Error("Failed to close response body")
		}
	}()
	assignments := make(map[string]interface{})
	err = json.NewDecoder(resp.Body).Decode(&assignments)
	if err != nil {
		log.Errorln("Error decoding json body from request: ", resp.Body)
		return 0, err
	}
	return getNodesLength(assignments), nil
}

func (srv *CanvasService) getUsersSubmissionsForCourse(courseId string, queryString url.Values, lastRun time.Time) ([]map[string]interface{}, error) {
	fields := log.Fields{"handler": "getUserSubmissionForCourse"}
	queryString.Add("submitted_since", lastRun.Format(time.RFC3339))
	queryString.Add("per_page", "100")
	url := srv.BaseURL + "/api/v1/courses/" + courseId + "/students/submissions"
	url += "?" + queryString.Encode()
	log.WithFields(fields).Printf("url: %v", url)
	resp, err := srv.SendRequest(url)
	if err != nil {
		log.WithFields(fields).Printf("getUserSubmissionForCourse failed to send request: %v", err)
		return nil, err
	}
	if resp.StatusCode != 200 {
		log.WithFields(fields).Errorln("canvas responded with code: ", resp.Status)
		return nil, errors.New("canvas responded with code: " + resp.Status)
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.Error("Failed to close response body")
		}
	}()
	var submission []map[string]any
	err = json.NewDecoder(resp.Body).Decode(&submission)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, fmt.Errorf("submission not found")
	}
	return submission, nil
}

/**
* First, we get all the assignments for the course
*  /api/v1/courses/:course_id/assignments
*
* Then we get the submissions for the assignment
*  /api/v1/courses/:course_id/assignments/:assignment_id/submissions/:user_id
*
* Quizzes are included in assignments so we don't need to get them separately
* */
func (srv *CanvasService) ImportMilestones(courseIdPair map[string]any, mapping []map[string]any, st *store, lastRun time.Time) error {
	courseId, ok := dbUint(courseIdPair, "course_id")
	if !ok {
		return fmt.Errorf("unexpected type %T for course_id in course mapping", courseIdPair["course_id"])
	}
	externalCourseId := courseIdPair["external_course_id"].(string)
	values := url.Values{}
	reversed := make(map[string]uint)
	for _, userMap := range mapping {
		userId, ok := dbUint(userMap, "user_id")
		if !ok {
			log.WithFields(log.Fields{"task": "ImportMilestones", "user_id": userMap["user_id"]}).
				Warnf("skipping user mapping with unexpected user_id type %T", userMap["user_id"])
			continue
		}
		externalUserId := userMap["external_user_id"].(string)
		reversed[externalUserId] = userId
		values.Add("user_ids[]", externalUserId)
	}
	fields := log.Fields{"task": "ImportMilestones", "course_id": courseId, "external_id": externalCourseId}
	submissions, err := srv.getUsersSubmissionsForCourse(externalCourseId, values, lastRun)
	if err != nil {
		fields["error"] = err.Error()
		log.Printf("Failed to get submission for assignment: %v", err)
		return err
	}
	for _, submission := range submissions {
		externalUserID := fmt.Sprintf("%d", int(submission["user_id"].(float64)))
		// The submissions endpoint does not honour the user filter we send, so it
		// returns users we have no mapping for. Writing those gives UserID 0, which
		// the users foreign key rejects -- skip them rather than log a failure per
		// submission and then try the grade write for the same phantom user.
		userID, mapped := reversed[externalUserID]
		if !mapped {
			log.WithFields(fields).Debugf("no user mapping for external user %s, skipping submission", externalUserID)
			continue
		}
		milestone := Milestone{
			UserID:      userID,
			ExternalID:  fmt.Sprintf("%d", int(submission["id"].(float64))),
			CourseID:    courseId,
			Type:        "assignment_submission",
			IsCompleted: submission["workflow_state"] == "complete" || submission["workflow_state"] == "graded",
		}
		if createErr := st.createMilestone(&milestone); createErr != nil {
			log.WithFields(fields).Errorln("failed to create assignment_submission milestone: ", createErr)
		}
		_, ok := submission["grade"].(string)
		if !ok {
			log.WithFields(fields).Traceln("no grade found for quiz submission")
			continue
		}
		if checkTimespanOfSubmission(lastRun, submission) {
			anonId := submission["anonymous_id"].(string)
			gradeReceived := Milestone{
				CourseID:    uint(courseId),
				UserID:      userID,
				ExternalID:  anonId,
				Type:        "grade_received",
				IsCompleted: true,
			}
			if createErr := st.createMilestone(&gradeReceived); createErr != nil {
				log.WithFields(fields).Errorln("failed to create grade_received milestone: ", createErr)
				continue
			}
		}
	}
	return nil
}

func checkTimespanOfSubmission(lastRun time.Time, submission map[string]any) bool {
	if submission["submitted_at"] == nil {
		// assignment not submitted, so we don't care about it
		return false
	}
	submittedAt, err := time.Parse(time.RFC3339, submission["submitted_at"].(string))
	if err != nil {
		// failed to parse time, so we don't care about it
		return false
	}
	if submittedAt.After(lastRun) {
		// assignment was submitted after last run, so this is valid
		return true
	} else {
		// if not, we check if it was graded after last run
		if submission["graded_at"] == nil {
			return false
		}
		gradedAt, err := time.Parse(time.RFC3339, submission["graded_at"].(string))
		if err != nil {
			return false
		}
		if gradedAt.After(lastRun) {
			return true
		}
	}
	return false
}

func (srv *CanvasService) getEnrollmentsForCourse(courseId string) ([]map[string]any, error) {
	url := srv.BaseURL + "/api/v1/courses/" + courseId + "/enrollments?state[]=active&state[]=invited&type[]=StudentEnrollment"
	resp, err := srv.SendRequest(url)
	if err != nil {
		log.Printf("Failed to send request: %v", err)
		return nil, err
	}
	defer func() {
		if resp.Body.Close() != nil {
			log.Error("Failed to close response body")
		}
	}()
	enrollments := make([]map[string]any, 0)
	err = json.NewDecoder(resp.Body).Decode(&enrollments)
	if err != nil {
		return nil, err
	}
	return enrollments, nil
}

func (srv *CanvasService) ImportActivityForCourse(coursePair map[string]any, st *store) error {
	courseId, ok := dbUint(coursePair, "course_id")
	if !ok {
		return fmt.Errorf("unexpected type %T for course_id in course mapping", coursePair["course_id"])
	}
	externalId := coursePair["external_course_id"].(string)
	enrollments, err := srv.getEnrollmentsForCourse(externalId)
	if err != nil {
		log.Printf("Failed to get enrollments for course: %v", err)
		return err
	}
	for _, enrollment := range enrollments {
		userId := fmt.Sprintf("%d", int(enrollment["user_id"].(float64)))
		userID, err := st.getMappedUserID(srv.ProviderPlatformID, userId)
		if err != nil {
			log.Printf("Failed to get user: %v", err)
			continue
		}
		enrolled, err := st.enrollmentExists(userID, courseId)
		if err != nil {
			log.WithFields(log.Fields{"userId": userID, "course_id": courseId, "error": err}).Error("Failed to check enrollment")
			continue
		}
		if !enrolled {
			if err := st.createEnrollment(userID, courseId); err != nil {
				log.WithFields(log.Fields{"userId": userID, "course_id": courseId, "error": err}).Error("Failed to create enrollment")
				continue
			}
		}
		totalTime := uint(enrollment["total_activity_time"].(float64))
		// NOTE: this is calling a stored procedure to calculate the time delta
		if err := st.insertDailyActivity("insert_daily_activity_canvas", userID, courseId, "course_interaction", totalTime, externalId); err != nil {
			log.WithFields(log.Fields{"userId": userID, "course_id": courseId, "error": err}).Error("Failed to create activity")
			continue
		}
	}
	return nil
}
