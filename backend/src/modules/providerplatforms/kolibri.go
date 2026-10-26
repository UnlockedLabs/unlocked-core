package providerplatforms

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

/***
* Our service struct which will have the methods to interact with
* the Kolibri API and the neccessary fields, headers, cookies to make requests
***/
type KolibriService struct {
	ProviderPlatformID uint
	BaseURL            string
	Client             *http.Client
	AccountID          string
	db                 *gorm.DB
	JobParams          map[string]any
}

/**
* Initializes a new KolibriService struct with the base URL of the Kolibri server
* Pulls the login info from ENV variables. In production, these should be set
* in /etc/environment
**/
func NewKolibriService(provider *ProviderPlatform, params map[string]any) *KolibriService {
	host := os.Getenv("DB_HOST")
	port := os.Getenv("DB_PORT")
	password := os.Getenv("KOLIBRI_DB_PASSWORD")
	dsn := fmt.Sprintf("host=%s user=kolibri password=%s dbname=kolibri port=%s sslmode=prefer TimeZone=UTC", host, password, port)
	conn, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Errorln("error connecting to db in NewKolibriService")
		return nil
	}
	log.Info("Connected to Kolibri's database")
	return &KolibriService{
		ProviderPlatformID: provider.ID,
		AccountID:          provider.AccountID,
		db:                 conn,
		BaseURL:            provider.BaseUrl,
		JobParams:          params,
	}
}

func (ks *KolibriService) GetJobParams() map[string]any {
	return ks.JobParams
}

/**
* Method to list all users in a Kolibri facility
* @info - GET /api/auth/facilityuser?member_of=<facilityID>
* @return - List of KolibriUser objects, representing all users in the facility
**/
func (ks *KolibriService) GetUsers(db *gorm.DB) ([]ImportUser, error) {
	// query kolibri database directly for users
	query := `SELECT full_name, username, id FROM kolibriauth_facilityuser WHERE facility_id = ?`
	var users []map[string]interface{}
	if err := ks.db.Raw(query, ks.AccountID).Scan(&users).Error; err != nil {
		log.Errorln("error querying kolibri database for users")
		return nil, err
	}
	var importUsers []ImportUser
	for _, user := range users {
		if db.Where("provider_platform_id = ? AND external_user_id = ?", ks.ProviderPlatformID, user["id"]).First(&ProviderUserMapping{}).Error == nil {
			continue
		}
		split := strings.Split(user["full_name"].(string), " ")
		var first string
		var last string
		if len(split) > 1 { //make sure before separation
			first, last = split[0], split[1]
		} else {
			first, last = split[0], ""
		}
		importUsers = append(importUsers, ImportUser{
			NameFirst:      first,
			NameLast:       last,
			Username:       user["username"].(string),
			Email:          user["username"].(string) + "@unlocked.v2",
			ExternalUserID: user["id"].(string),
		})
	}
	return importUsers, nil
}

/**
* @info - GET /api/content/channel?available=true
* @return - List of maps, each containing the details of a Content object
**/
func (ks *KolibriService) ImportCourses(db *gorm.DB) error { //add more to this:::
	log.Println("Importing channel and classroom courses from Kolibri")
	var courses []map[string]interface{} //modified to left join as quizzes only activate when teachers click 'START QUIZ'
	sql := `SELECT id, name, description, thumbnail, total_resource_count, root_id, 'channel' as course_type FROM content_channelmetadata
			UNION 
			SELECT col.id, name, '' as description, '' as thumbnail, lesson.resource_count+COALESCE(exam.resource_count,0) as total_resource_count, col.id as root_id, 'class' as course_type 
			FROM kolibriauth_collection col
			inner join (SELECT col.id, count(col.id) as resource_count 
        		FROM kolibriauth_collection col
        		inner join lessons_lesson lesson on col.id = lesson.collection_id 
        		where kind = 'classroom'
        		group by col.id
			) lesson on col.id = lesson.id
 			left join (SELECT exam.collection_id, count(exam.collection_id) as resource_count 
        			FROM kolibriauth_collection col
        			inner join exams_exam exam on col.id = exam.collection_id
        			where kind = 'classroom'
        			group by exam.collection_id
			) exam on col.id = exam.collection_id
			where kind = 'classroom'`
	if err := ks.db.Raw(sql).Scan(&courses).Error; err != nil {
		log.Errorln("error querying kolibri database for courses")
		return err
	}
	for _, course := range courses {
		id := course["id"].(string)
		if db.Where("provider_platform_id = ? AND external_id = ?", ks.ProviderPlatformID, id).First(&Course{}).Error == nil {
			if course["course_type"].(string) == "class" {
				updateTotalProgress(db, ks.ProviderPlatformID, course)
			}
			continue
		}
		prog := ks.IntoCourse(course)
		if err := db.Create(&prog).Error; err != nil {
			log.Errorln("error creating course in db")
			continue
		}
	}
	return nil
}

func updateTotalProgress(db *gorm.DB, providerPlatformID uint, course map[string]any) {
	id := course["id"].(string)
	totalResourceCount := course["total_resource_count"].(int64)
	if db.Where("provider_platform_id = ? AND external_id = ? AND total_progress_milestones = ?", providerPlatformID, id, totalResourceCount).First(&Course{}).Error != nil {
		if err := db.Table("courses as c").Where("provider_platform_id = ? AND external_id = ?", providerPlatformID, id).Update("total_progress_milestones", totalResourceCount).Error; err != nil {
			log.Errorln("error updating course total_progress_milestones in db, error is: ", err) //just logging the error if it occurs, logic will continue
		}
	}
}

type milestonePO struct {
	course          map[string]any
	usersMap        map[string]uint
	externalUserIds []string
}

func (ks *KolibriService) ImportMilestones(course map[string]any, users []map[string]any, db *gorm.DB, lastRun time.Time) error {
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
	externalUserIds := []string{}
	for externalId := range usersMap {
		if !IsNumber(externalId) {
			externalUserIds = append(externalUserIds, externalId)
		}
	}
	paramObj := milestonePO{
		course:          course,
		usersMap:        usersMap,
		externalUserIds: externalUserIds,
	}
	err := importEnrollmentMilestones(ks, paramObj, db)
	if err != nil {
		log.Errorln("error importing enrollment milestones, error is ", err)
	}
	err = importAssignmentQuizGradeMilestones(ks, paramObj, db)
	if err != nil {
		log.Errorln("error importing enrollment milestones, error is ", err)
	}
	return err
}

func importEnrollmentMilestones(ks *KolibriService, paramObj milestonePO, db *gorm.DB) error {
	var enrollments []map[string]interface{}
	sql := `select meta.name, meta.id as course_id, sess.milestone_ex_id, kf.id as user_id 
		from kolibriauth_facilityuser kf
		inner join (select id as milestone_ex_id, channel_id, user_id, start_timestamp, 
			row_number() over (PARTITION BY channel_id, user_id ORDER BY start_timestamp) AS RN 
        	from logger_contentsessionlog
			) sess on kf.id = sess.user_id and sess.RN = 1
		inner join content_channelmetadata meta on sess.channel_id = meta.id
		where meta.id = ?
			and kf.id IN (?)
		UNION
		select classrooms.name, classrooms.course_id, classrooms.milestone_ex_id, kf.id as user_id 
		from kolibriauth_facilityuser kf
		inner join (select col.name, mem.user_id, mem.id as milestone_ex_id, col.id as course_id, 
        	row_number() over (PARTITION BY col.id, mem.user_id ORDER BY lesson.date_created) AS RN 
        	from kolibriauth_membership mem 
        	inner join kolibriauth_collection col on mem.collection_id = col.id
                and col.kind = 'classroom'
        	inner join lessons_lessonassignment assign on col.id = assign.collection_id
        	inner join lessons_lesson lesson on assign.lesson_id = lesson.id
                and lesson.is_active = 'true' 
			) as classrooms on kf.id = classrooms.user_id
        		and classrooms.RN = 1
		where classrooms.course_id = ?
			and kf.id IN (?)
		`
	course := paramObj.course
	externalUserIds := paramObj.externalUserIds
	usersMap := paramObj.usersMap
	externalCourseId := course["external_course_id"].(string)
	if err := ks.db.Raw(sql, externalCourseId, externalUserIds, externalCourseId, externalUserIds).Scan(&enrollments).Error; err != nil {
		log.Errorln("error querying kolibri database for enrollment milestones")
		return err
	}
	courseId := uint(course["course_id"].(int64))
	for _, enrollment := range enrollments {
		userID := usersMap[enrollment["user_id"].(string)]
		if db.Model(&UserEnrollment{}).First(&UserEnrollment{}, "user_id = ? AND course_id = ?", userID, courseId).RowsAffected == 0 {
			if err := db.Create(&UserEnrollment{UserID: userID, CourseID: uint(courseId)}).Error; err != nil {
				log.WithFields(log.Fields{"userId": userID, "course_id": courseId, "error": err}).Error("Failed to create enrollment")
				continue
			}
		}
	}
	return nil
}

func importAssignmentQuizGradeMilestones(ks *KolibriService, paramObj milestonePO, db *gorm.DB) error {
	var aqgMilestones []map[string]any
	sql := `select col.name, prog.id as milestone_ex_id, prog.user_id, col.id as course_id, prog.notification_object as submission_type, 
				prog.notification_event as is_complete, quiz_num_correct, quiz_num_answered
		from notifications_learnerprogressnotification prog  
		inner join kolibriauth_collection col on prog.classroom_id = col.id
        	and col.kind = 'classroom'
		where col.id = ?
        	and prog.notification_event = 'Completed'
        	and prog.notification_object IN ('Quiz','Lesson')
        	and prog.user_id IN (?)
		`
	course := paramObj.course
	externalUserIds := paramObj.externalUserIds
	usersMap := paramObj.usersMap
	externalCourseId := course["external_course_id"].(string)
	if err := ks.db.Raw(sql, externalCourseId, externalUserIds).Scan(&aqgMilestones).Error; err != nil {
		log.Errorln("error querying kolibri database for assignment submissions, quiz submissions, and graded milestones")
		return err
	}
	courseId := uint(course["course_id"].(int64))
	for _, aqgMilestone := range aqgMilestones {
		userId := usersMap[aqgMilestone["user_id"].(string)]
		id := strconv.Itoa(int(aqgMilestone["milestone_ex_id"].(int32)))
		//concatenated the user id to external id for graded milestones because these are actual quiz submissions and need a way to distinguish graded milestones
		if db.Where("user_id = ? AND course_id = ? AND (external_id = ? OR external_id = ?)", userId, courseId, id, strconv.Itoa(int(userId))+"-"+id).First(&Milestone{}).Error == nil {
			continue
		}
		submissionType := aqgMilestone["submission_type"].(string)
		milestone := Milestone{
			UserID:      usersMap[aqgMilestone["user_id"].(string)],
			CourseID:    courseId,
			IsCompleted: true, //check
			ExternalID:  id,
		}
		if submissionType == "Quiz" {
			milestone.Type = QuizSubmission
		} else {
			milestone.Type = AssignmentSubmission
		}
		if err := db.Create(&milestone).Error; err != nil {
			log.Errorln("error creating milestone in db")
			continue
		}
		if submissionType == "Quiz" {
			milestone.ID = 0
			milestone.Type = GradeReceived
			milestone.ExternalID = strconv.Itoa(int(userId)) + "-" + milestone.ExternalID
			if err := db.Create(&milestone).Error; err != nil {
				log.Errorln("error creating milestone in db")
				continue
			}
		}
	}
	return nil
}

type KolibriActivity struct {
	ID                  string `json:"id"`
	UserId              string `json:"user_id"`
	TimeSpent           string `json:"time_spent"`
	CompletionTimestamp string `json:"completion_timestamp"`
	ContentId           string `json:"content_id"`
	Progress            string `json:"progress"`
	Kind                string `json:"kind"`
}

func (ks *KolibriService) ImportActivityForCourse(courseIdPair map[string]any, db *gorm.DB) error {
	courseId := int(courseIdPair["course_id"].(int64))
	externalId := courseIdPair["external_course_id"].(string)
	sql := `SELECT id, user_id, time_spent, completion_timestamp, content_id, progress, kind FROM logger_contentsummarylog WHERE channel_id = ?`
	var activities []KolibriActivity
	if err := ks.db.Raw(sql, externalId).Find(&activities).Error; err != nil {
		log.Errorln("error querying kolibri database for course activities")
		return err
	}
	for _, activity := range activities {
		var user_id uint
		if err := db.Model(&ProviderUserMapping{}).Select("user_id").First(&user_id, "external_user_id = ?", activity.UserId).Error; err != nil {
			log.Errorln("error finding user by external id in ImportActivityForCourse")
			continue
		}
		kinds := map[string]ActivityType{
			"video":    ContentInteraction,
			"exercise": CourseInteraction,
			"html5":    ContentInteraction,
			"h5p":      ContentInteraction,
			"topic":    CourseInteraction,
		}
		timeSpent, err := strconv.ParseFloat(activity.TimeSpent, 64)
		if err != nil {
			log.Errorln("error parsing time spent in ImportActivityForCourse")
			continue
		}
		kind, ok := kinds[activity.Kind]
		if !ok {
			kind = ContentInteraction
		}
		//checking if activity record was already inserted
		var acts []map[string]any
		if db.Raw("select external_id from activities where course_id = ? AND external_id = ?", courseId, activity.ID).First(&acts).Error == nil {
			continue
		}
		if err := db.Exec("CALL insert_daily_activity_kolibri(?, ?, ?, ?, ?, ?)", user_id, courseId, kind, timeSpent, activity.ID, time.Now()).Error; err != nil {
			log.WithFields(log.Fields{"userId": user_id, "course_id": courseId, "error": err}).Error("Failed to create activity")
			continue
		}
	}
	return nil
}

func IsNumber(u string) bool {
	_, err := strconv.Atoi(u)
	return err == nil
}
