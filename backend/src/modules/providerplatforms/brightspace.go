package providerplatforms

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	TokenEndpoint       = "https://auth.brightspace.com/core/connect/token"
	DataSetsEndpoint    = "https://unlocked.brightspacedemo.com/d2l/api/lp/%s/dataExport/bds/list"
	DataDownloadEnpoint = "https://unlocked.brightspacedemo.com/d2l/api/lp/%s/dataExport/bds/download/%s"
)

var CsvDownloadPath = os.Getenv("BRIGHTSPACE_TEMP_DIR")

type BrightspaceService struct {
	ProviderPlatformID uint
	Client             *http.Client
	BaseURL            string
	ClientID           string
	ClientSecret       string
	RefreshToken       string
	Scope              string
	AccessToken        string
	BaseHeaders        map[string]string
	JobParams          map[string]any
	IsDownloaded       bool //flag to let process know that bulk data has been downloaded
	CsvFileMap         map[string]string
}

func newBrightspaceService(provider *ProviderPlatform, db *gorm.DB, params map[string]any) (*BrightspaceService, error) {
	keysSplit := strings.Split(provider.AccessKey, ";")
	if len(keysSplit) < 2 {
		return nil, errors.New("unable to find refresh token, unable to intialize BrightspaceService")
	}
	if _, err := os.Stat(CsvDownloadPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("unable to find csv download directory path %s, unable to intialize BrightspaceService", CsvDownloadPath)
	}
	brightspaceService := BrightspaceService{
		ProviderPlatformID: provider.ID,
		Client:             &http.Client{},
		BaseURL:            provider.BaseUrl,
		ClientID:           provider.AccountID,
		ClientSecret:       keysSplit[0],
		RefreshToken:       keysSplit[1],
		Scope:              BrightspaceScopes,
		JobParams:          params,
	}
	data := url.Values{}
	data.Add("grant_type", "refresh_token")
	data.Add("refresh_token", brightspaceService.RefreshToken)
	data.Add("client_id", brightspaceService.ClientID)
	data.Add("client_secret", brightspaceService.ClientSecret)
	data.Add("scope", brightspaceService.Scope)
	log.Infof("refreshing token using endpoint url %v", TokenEndpoint)
	resp, err := brightspaceService.SendPostRequest(TokenEndpoint, data)
	if err != nil {
		log.Errorf("error sending post request to url %v", TokenEndpoint)
		return nil, err
	}
	var tokenMap map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&tokenMap); err != nil {
		log.Errorf("error decoding to response from url %v, error is: %v", TokenEndpoint, err)
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		errType, okError := tokenMap["error"].(string)
		errMsg, okDesc := tokenMap["error_description"].(string)
		msg := "unable to request new refresh token from brightspace"
		if okError && okDesc {
			msg = fmt.Sprintf("unable to request new refresh token from brightspace, response error message is: %s: %s", errType, errMsg)
			return nil, errors.New(msg)
		}
		return nil, errors.New(msg)
	}
	brightspaceService.AccessToken = tokenMap["access_token"].(string)
	brightspaceService.RefreshToken = tokenMap["refresh_token"].(string)
	provider.AccessKey = brightspaceService.ClientSecret + ";" + brightspaceService.RefreshToken
	if err := db.Save(&provider).Error; err != nil {
		log.Errorf("error trying to update provider access_key with new refresh token, error is %v", err)
		return nil, err
	}
	log.Info("refresh token updated successfully on the provider_platform")
	headers := make(map[string]string)
	headers["Authorization"] = "Bearer " + brightspaceService.AccessToken
	headers["Accept"] = "application/json"
	brightspaceService.BaseHeaders = headers
	brightspaceService.CsvFileMap = make(map[string]string)
	return &brightspaceService, nil
}

func (srv *BrightspaceService) SendPostRequest(url string, data url.Values) (*http.Response, error) {
	encodedUrl := data.Encode()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(encodedUrl))
	if err != nil {
		log.Errorf("error creating new POST request to url %v and error is: %v", url, err)
		return nil, err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded") //standard header for url.Values (encoded)
	resp, err := srv.Client.Do(req)
	if err != nil {
		log.Errorf("error executing POST request to url %v and error is: %v", url, err)
		return nil, err
	}
	return resp, nil
}

func (srv *BrightspaceService) SendRequest(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Errorf("error creating new GET request to url %v and error is: %v", url, err)
		return nil, err
	}
	for key, value := range srv.BaseHeaders {
		req.Header.Add(key, value)
	}
	resp, err := srv.Client.Do(req)
	if err != nil {
		log.Errorf("error executing GET request to url %v and error is: %v", url, err)
		return nil, err
	}
	return resp, nil
}

func (srv *BrightspaceService) getBrightspaceBulkData(pluginName, zipFileName string) (string, error) {
	pluginId, err := srv.getPluginId(pluginName)
	if err != nil {
		log.Errorf("error attempting to get plugin id for %s, error is: %v", pluginName, err)
		return "", err
	}
	log.Infof("successfully retrieved plugin id %s for downloading csv file for %s", pluginId, pluginName)
	downloadUrl := fmt.Sprintf(DataDownloadEnpoint, BrightspaceApiVersion, pluginId)
	csvFile, err := srv.downloadAndUnzipFile(zipFileName, downloadUrl)
	if err != nil {
		log.Errorf("error attempting to download zip file for %s using plugin id %s, error is: %v", pluginName, pluginId, err)
		return "", err
	}
	log.Infof("successfully downloaded %s and unzipped %s", zipFileName, csvFile)
	return csvFile, err
}

func (srv *BrightspaceService) GetUsers(db *gorm.DB) ([]ImportUser, error) {
	csvFile, err := srv.getBrightspaceBulkData("Users", "users/Users.zip")
	if err != nil {
		log.Errorf("error attempting to get bulk data for Brightspace courses, error is: %v", err)
		return nil, err
	}
	bsUsers := []BrightspaceUser{}
	importUsers := []ImportUser{}
	readCSV(&bsUsers, csvFile)
	cleanUpFiles(csvFile)
	fields := log.Fields{"provider": srv.ProviderPlatformID, "Function": "GetUsers", "csvFile": csvFile}
	log.WithFields(fields).Info("importing users from provider using csv file")
	for _, bsUser := range bsUsers {
		if strings.ToUpper(bsUser.IsActive) == "TRUE" {
			if db.Where("provider_platform_id = ? AND external_user_id = ?", srv.ProviderPlatformID, bsUser.UserId).First(&ProviderUserMapping{}).RowsAffected > 0 {
				continue
			}
			user := srv.IntoImportUser(bsUser)
			importUsers = append(importUsers, *user)
		}
	}
	log.Info("successfully imported users from provider")
	return importUsers, nil
}

func (srv *BrightspaceService) ImportCourses(db *gorm.DB) error {
	contentObjCsvFile, err := srv.getBrightspaceBulkData("Content Objects", "courses/ContentObjects.zip")
	if err != nil {
		log.Errorf("error attempting to get bulk data for Brightspace content objects for importing activities, error is: %v", err)
		return err
	}
	organizationalCsvFile, err := srv.getBrightspaceBulkData("Organizational Units", "courses/OrganizationalUnits.zip")
	if err != nil {
		log.Errorf("error attempting to get bulk data for Brightspace courses, error is: %v", err)
		return err
	}
	bsCourses := []BrightspaceCourse{}
	readCSV(&bsCourses, organizationalCsvFile)
	contentObjects := []BrightspaceContentObject{}
	readCSV(&contentObjects, contentObjCsvFile)
	totalProgressMilestonesMap := make(map[string]int)
	for _, obj := range contentObjects {
		if value, ok := totalProgressMilestonesMap[obj.OrgUnitId]; ok {
			totalProgressMilestonesMap[obj.OrgUnitId] = value + 1
		} else {
			totalProgressMilestonesMap[obj.OrgUnitId] = 1
		}
	}
	cleanUpFiles(organizationalCsvFile, contentObjCsvFile)
	fields := log.Fields{"provider": srv.ProviderPlatformID, "Function": "ImportCourses", "organizationalCsvFile": organizationalCsvFile, "contentObjCsvFile": contentObjCsvFile}
	log.WithFields(fields).Info("importing courses from provider using csv file")
	for _, bsCourse := range bsCourses {
		if strings.ToUpper(bsCourse.IsActive) == "TRUE" && strings.ToUpper(bsCourse.IsDeleted) == "FALSE" && bsCourse.Type == "Course Offering" && totalProgressMilestonesMap[bsCourse.OrgUnitId] > 0 {
			bsCourse.TotalContentCount = totalProgressMilestonesMap[bsCourse.OrgUnitId]
			if db.Where("provider_platform_id = ? AND external_id = ?", srv.ProviderPlatformID, bsCourse.OrgUnitId).First(&Course{}).RowsAffected > 0 {
				srv.updateTotalProgress(db, bsCourse.TotalContentCount, bsCourse.OrgUnitId)
				continue
			}
			log.Infof("importing course named %v with external id %v", bsCourse.Name, bsCourse.OrgUnitId)
			course := srv.IntoCourse(bsCourse)
			if err := db.Create(&course).Error; err != nil {
				log.Errorf("error creating course in db, error is: %v", err)
				continue
			}
		}
	}
	return nil
}

func (srv *BrightspaceService) updateTotalProgress(db *gorm.DB, totalProgressMilestones int, externalId string) {
	if db.Where("provider_platform_id = ? AND external_id = ? AND total_progress_milestones = ?", srv.ProviderPlatformID, externalId, totalProgressMilestones).First(&Course{}).RowsAffected == 0 {
		if err := db.Model(&Course{}).Where("provider_platform_id = ? AND external_id = ?", srv.ProviderPlatformID, externalId).Update("total_progress_milestones", totalProgressMilestones).Error; err != nil {
			log.Errorln("error updating course total_progress_milestones in db, error is: ", err) //just logging the error if it occurs, logic will continue
		}
	}
}

func (srv *BrightspaceService) ImportMilestones(course map[string]interface{}, users []map[string]interface{}, db *gorm.DB, lastRun time.Time) error {
	usersMap := make(map[string]uint)
	for _, user := range users {
		userId, ok := dbUint(user, "user_id")
		if !ok {
			log.WithFields(log.Fields{"task": "ImportMilestones", "user_id": user["user_id"]}).
				Warnf("skipping user mapping with unexpected user_id type %T", user["user_id"])
			continue
		}
		usersMap[user["external_user_id"].(string)] = userId
	}
	paramObj := milestonePO{
		course:   course,
		usersMap: usersMap,
	}
	if !srv.IsDownloaded {
		cleanUpCsvFiles("milestones")
	}
	err := importBSEnrollmentMilestones(srv, paramObj, db)
	if err != nil {
		log.Errorln("error importing enrollment milestones, error is ", err)
	}
	err = importBSAssignmentSubmissionMilestones(srv, paramObj, db)
	if err != nil {
		log.Errorln("error importing assignment submission milestones, error is ", err)
	}
	err = importBSQuizSubmissionMilestones(srv, paramObj, db)
	if err != nil {
		log.Errorln("error importing quiz submission milestones, error is ", err)
	}
	srv.IsDownloaded = true
	return nil
}

func importBSEnrollmentMilestones(srv *BrightspaceService, po milestonePO, db *gorm.DB) error {
	var (
		csvFile string
		err     error
	)
	switch srv.IsDownloaded {
	case true:
		csvFile = srv.CsvFileMap["enrollments"]
	case false:
		csvFile, err = srv.getBrightspaceBulkData("User Enrollments", "milestones/UserEnrollments.zip")
		if err != nil {
			log.Errorf("error attempting to get bulk data for Brightspace enrollments, error is: %v", err)
			return err
		}
		srv.CsvFileMap["enrollments"] = csvFile
		log.Infof("successfully downloaded and unzipped %v for importing enrollment milestones", csvFile)
	}
	bsEnrollments := []BrightspaceEnrollment{}
	readCSV(&bsEnrollments, csvFile)
	fields := log.Fields{"provider": srv.ProviderPlatformID, "Function": "ImportMilestones.importBSEnrollmentMilestones", "csvFile": csvFile}
	log.WithFields(fields).Info("importing enrollment milestones from provider using csv file")
	course := po.course
	usersMap := po.usersMap
	courseId := uint(course["course_id"].(int64))
	externalCourseId := course["external_course_id"].(string)
	filteredBSEnrollments := findSlice(bsEnrollments, func(bsEnrollment BrightspaceEnrollment) bool {
		return bsEnrollment.OrgUnitId == externalCourseId && usersMap[bsEnrollment.UserId] != 0
	})
	var (
		id     string
		userId uint
	)
	for _, bsEnrollment := range filteredBSEnrollments {
		id = bsEnrollment.getCompositeKeyId()
		userId = usersMap[bsEnrollment.UserId]
		if db.Model(&UserEnrollment{}).First(&UserEnrollment{}, "user_id = ? AND course_id = ? AND external_id = ?", userId, courseId, id).RowsAffected == 0 {
			if err := db.Create(&UserEnrollment{UserID: userId, CourseID: uint(courseId)}).Error; err != nil {
				log.WithFields(log.Fields{"userId": userId, "course_id": courseId, "error": err}).Error("Failed to create enrollment")
				continue
			}
		}
	}
	return nil
}

func importBSAssignmentSubmissionMilestones(srv *BrightspaceService, po milestonePO, db *gorm.DB) error {
	var (
		csvFile string
		err     error
	)
	switch srv.IsDownloaded {
	case true:
		csvFile = srv.CsvFileMap["assignments"]
	case false:
		csvFile, err = srv.getBrightspaceBulkData("Assignment Submissions", "milestones/AssignmentSubmissions.zip")
		if err != nil {
			log.Errorf("error attempting to get bulk data for Brightspace assignment submissions, error is: %v", err)
			return err
		}
		srv.CsvFileMap["assignments"] = csvFile
		log.Infof("successfully downloaded and unzipped %v for importing assignment submission milestones", csvFile)
	}
	bsAssignments := []BrightspaceAssignmentSubmission{}
	readCSV(&bsAssignments, csvFile)
	fields := log.Fields{"provider": srv.ProviderPlatformID, "Function": "ImportMilestones.importBSAssignmentSubmissionMilestones", "csvFile": csvFile}
	log.WithFields(fields).Info("importing assignment submission/graded milestones from provider using csv file")
	course := po.course
	usersMap := po.usersMap
	courseId := uint(course["course_id"].(int64))
	externalCourseId := course["external_course_id"].(string)
	filteredBSAssignments := findSlice(bsAssignments, func(bsAssignment BrightspaceAssignmentSubmission) bool {
		return bsAssignment.OrgUnitId == externalCourseId && usersMap[bsAssignment.UserId] != 0
	})
	var (
		id       string
		userId   uint
		gradedId string
	)
	for _, bsAssignment := range filteredBSAssignments {
		id = bsAssignment.getCompositeKeyId() //CREATE COMPOSITE FOR UNIQUENESS
		gradedId = strconv.Itoa(int(userId)) + bsAssignment.getGradedCompositeKeyId()
		userId = usersMap[bsAssignment.UserId]
		switch strings.ToUpper(bsAssignment.IsGraded) {
		case "TRUE":
			if db.Where("user_id = ? AND course_id = ? AND external_id = ?", userId, courseId, gradedId).First(&Milestone{}).RowsAffected > 0 {
				addAssignmentMilestoneIfNotExists(db, userId, courseId, bsAssignment)
				continue
			}
		case "FALSE":
			if db.Where("user_id = ? AND course_id = ? AND external_id = ?", userId, courseId, id).First(&Milestone{}).RowsAffected > 0 {
				continue
			}
		}
		milestone := Milestone{
			UserID:      userId,
			CourseID:    courseId,
			IsCompleted: true, //check
		}
		switch strings.ToUpper(bsAssignment.IsGraded) {
		case "TRUE":
			milestone.Type = GradeReceived
			milestone.ExternalID = gradedId

		case "FALSE":
			milestone.Type = AssignmentSubmission
			milestone.ExternalID = id
		}
		if err = db.Create(&milestone).Error; err != nil {
			log.Errorln("error creating milestone in db")
			continue
		}
	}
	return nil
}

func importBSQuizSubmissionMilestones(srv *BrightspaceService, po milestonePO, db *gorm.DB) error {
	var (
		csvFile string
		err     error
	)
	switch srv.IsDownloaded {
	case true:
		csvFile = srv.CsvFileMap["quizzes"]
	case false:
		csvFile, err = srv.getBrightspaceBulkData("Quiz Attempts", "milestones/QuizAttempts.zip")
		if err != nil {
			log.Errorf("error attempting to get bulk data for Brightspace quiz attempts, error is: %v", err)
			return err
		}
		srv.CsvFileMap["quizzes"] = csvFile
		log.Infof("successfully downloaded and unzipped %v for importing quiz submissions", csvFile)
	}
	bsQuizes := []BrightspaceQuizSubmission{}
	readCSV(&bsQuizes, csvFile)
	fields := log.Fields{"provider": srv.ProviderPlatformID, "Function": "ImportMilestones.importBSQuizSubmissionMilestones", "csvFile": csvFile}
	log.WithFields(fields).Info("importing quiz submission/graded milestones from provider using csv file")
	course := po.course
	usersMap := po.usersMap
	courseId := uint(course["course_id"].(int64))
	externalCourseId := course["external_course_id"].(string)
	filteredBSQuizes := findSlice(bsQuizes, func(bsQuiz BrightspaceQuizSubmission) bool {
		return bsQuiz.OrgUnitId == externalCourseId && strings.ToUpper(bsQuiz.IsDeleted) == "FALSE" && usersMap[bsQuiz.UserId] != 0
	})
	var (
		id       string
		userId   uint
		gradedId string
	)
	for _, bsQuiz := range filteredBSQuizes {
		id = bsQuiz.AttemptId
		gradedId = strconv.Itoa(int(userId)) + bsQuiz.getGradedCompositeKeyId()
		userId = usersMap[bsQuiz.UserId]
		switch strings.ToUpper(bsQuiz.IsGraded) {
		case "TRUE":
			if db.Where("user_id = ? AND course_id = ? AND external_id = ?", userId, courseId, gradedId).First(&Milestone{}).RowsAffected > 0 {
				addQuizMilestoneIfNotExists(db, userId, courseId, bsQuiz)
				continue
			}
		case "FALSE":
			if db.Where("user_id = ? AND course_id = ? AND external_id = ?", userId, courseId, id).First(&Milestone{}).RowsAffected > 0 {
				continue
			}
		}
		milestone := Milestone{
			UserID:      userId,
			CourseID:    courseId,
			IsCompleted: true,
		}
		switch strings.ToUpper(bsQuiz.IsGraded) {
		case "TRUE":
			milestone.Type = GradeReceived
			milestone.ExternalID = gradedId
		case "FALSE":
			milestone.Type = QuizSubmission
			milestone.ExternalID = id
		}
		if err = db.Create(&milestone).Error; err != nil {
			log.Errorln("error creating milestone in db")
			continue
		}
	}
	return nil
}

func addAssignmentMilestoneIfNotExists(db *gorm.DB, userId, courseId uint, bsAssignment BrightspaceAssignmentSubmission) {
	if db.Where("user_id = ? AND course_id = ? AND external_id = ?", userId, courseId, bsAssignment.getCompositeKeyId()).First(&Milestone{}).RowsAffected == 0 {
		milestone := Milestone{
			UserID:      userId,
			CourseID:    courseId,
			IsCompleted: true,
			Type:        AssignmentSubmission,
			ExternalID:  bsAssignment.getCompositeKeyId(),
		}
		if err := db.Create(&milestone).Error; err != nil {
			log.Errorln("error creating assignment submission milestone in db, error is ", err)
		}
	}
}

func addQuizMilestoneIfNotExists(db *gorm.DB, userId, courseId uint, bsQuiz BrightspaceQuizSubmission) {
	if db.Where("user_id = ? AND course_id = ? AND external_id = ?", userId, courseId, bsQuiz.AttemptId).First(&Milestone{}).RowsAffected == 0 {
		milestone := Milestone{
			UserID:      userId,
			CourseID:    courseId,
			IsCompleted: true,
			Type:        QuizSubmission,
			ExternalID:  bsQuiz.AttemptId,
		}
		if err := db.Create(&milestone).Error; err != nil {
			log.Errorln("error creating quiz submission milestone in db, error is ", err)
		}
	}
}

func (srv *BrightspaceService) ImportActivityForCourse(course map[string]any, db *gorm.DB) error {
	if !srv.IsDownloaded {
		cleanUpCsvFiles("activities")
	}
	var (
		contentObjCsvFile   string
		userProgressCsvFile string
		err                 error
	)
	switch srv.IsDownloaded {
	case true:
		contentObjCsvFile = srv.CsvFileMap["contentobjs"]
		userProgressCsvFile = srv.CsvFileMap["contentprogress"]
	case false:
		contentObjCsvFile, err = srv.getBrightspaceBulkData("Content Objects", "activities/ContentObjects.zip")
		if err != nil {
			log.Errorf("error attempting to get bulk data for Brightspace content objects for importing activities, error is: %v", err)
			return err
		}
		userProgressCsvFile, err = srv.getBrightspaceBulkData("Content User Progress", "activities/ContentUserProgress.zip")
		if err != nil {
			log.Errorf("error attempting to get bulk data for Brightspace content user progress for importing activities, error is: %v", err)
			return err
		}
		srv.CsvFileMap["contentobjs"] = contentObjCsvFile
		srv.CsvFileMap["contentprogress"] = userProgressCsvFile
		srv.IsDownloaded = true
	}
	courseId := uint(course["course_id"].(int64))
	externalCourseId := course["external_course_id"].(string)
	bsContentDtoMap := srv.getUsersActivity(db, externalCourseId, contentObjCsvFile, userProgressCsvFile)
	fields := log.Fields{"provider": srv.ProviderPlatformID, "Function": "ImportActivityForCourse", "contentObjCsvFile": contentObjCsvFile, "userProgressCsvFile": userProgressCsvFile}
	log.WithFields(fields).Info("importing activities from provider using csv file")
	for userId, bsUserContentDtos := range bsContentDtoMap {
		for _, bsDto := range bsUserContentDtos {
			var acts []map[string]any
			if db.Raw("select external_id from activities where course_id = ? AND external_id = ?", courseId, bsDto.ExternalId).First(&acts).RowsAffected > 0 {
				continue
			}
			// TODO: check if there is a timestamp we can use to get the exact time /date of the activity being entered
			if err = db.Exec("CALL insert_daily_activity_brightspace(?, ?, ?, ?, ?, ?)",
				userId, courseId, ContentInteraction, bsDto.TotalTime, bsDto.ExternalId, time.Now()).Error; err != nil {
				log.WithFields(log.Fields{"user_id": userId, "course_id": courseId, "error": err}).Error("Failed to create activity using brightspace data")
				continue
			}
		}
	}
	return nil
}

func (srv *BrightspaceService) getUsersActivity(db *gorm.DB, externalCourseId, contentObjCsvFile, userProgressCsvFile string) map[uint][]BrightspaceUserContentDto {
	bsContentDtoMap := make(map[uint][]BrightspaceUserContentDto)
	bsContentObjects := []BrightspaceContentObject{}
	readCSV(&bsContentObjects, contentObjCsvFile)
	filteredContentObjs := findSlice(bsContentObjects, func(bsContentObj BrightspaceContentObject) bool { //get content objects for the course being iterate
		return bsContentObj.OrgUnitId == externalCourseId && bsContentObj.IsDeleted == "0"
	})
	if len(filteredContentObjs) < 1 {
		log.Infof("no content objects found for the course with external id of %s", externalCourseId)
		return bsContentDtoMap
	}
	bsContentUserProgress := []BrightspaceContentUserProgress{}
	readCSV(&bsContentUserProgress, userProgressCsvFile)
	for _, contentUser := range bsContentUserProgress { //separate by user
		var userId uint //filter out the users
		if err := db.Model(&ProviderUserMapping{}).Select("user_id").First(&userId, "external_user_id = ? and provider_platform_id = ?", contentUser.UserId, srv.ProviderPlatformID).Error; err != nil {
			log.Errorln("error finding brightspace user by external id in ImportActivityForCourse")
			continue
		}
		contentObjsByUser := findSlice(filteredContentObjs, func(bsContentObj BrightspaceContentObject) bool { //get content objects for the course being iterated
			return bsContentObj.ContentObjectId == contentUser.ContentObjectId
		})
		if _, ok := bsContentDtoMap[userId]; !ok && len(contentObjsByUser) > 0 {
			bsContentDtoMap[userId] = []BrightspaceUserContentDto{}
		}
		for _, contentObj := range contentObjsByUser {
			dto := BrightspaceUserContentDto{
				OrgUnitId:         contentObj.OrgUnitId,
				ContentObjectId:   contentObj.ContentObjectId,
				TotalTime:         contentUser.TotalTime,
				Title:             contentObj.Title,
				ContentObjectType: contentObj.ContentObjectType,
				ExternalId:        contentUser.getCompositeKeyId(),
			}
			bsContentDtoMap[userId] = append(bsContentDtoMap[userId], dto)
		}
	}
	return bsContentDtoMap
}

func (srv *BrightspaceService) GetJobParams() map[string]interface{} {
	return srv.JobParams
}
