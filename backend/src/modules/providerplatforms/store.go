package providerplatforms

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// store holds Provider Platforms' database queries. The queries moved from
// core's database package unchanged (EN-193). A method that takes ctx records
// the caller as the create or update user; one without writes no user.
type store struct{ db *gorm.DB }

// Provider platforms

func (s *store) getAllProviderPlatforms(args *queryContext) ([]ProviderPlatform, error) {
	var platforms []ProviderPlatform
	if err := s.db.Model(&ProviderPlatform{}).Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "provider_platforms")
	}
	if err := s.db.Model(&ProviderPlatform{}).Preload("OidcClient").
		Offset(args.CalcOffset()).Limit(args.PerPage).Find(&platforms).Error; err != nil {
		return nil, newGetRecordsDBError(err, "provider_platforms")
	}
	for i := range platforms {
		if platforms[i].OidcClient != nil {
			platforms[i].OidcID = platforms[i].OidcClient.ID
		}
	}
	return platforms, nil
}

func (s *store) getAllActiveProviderPlatforms() ([]ProviderPlatform, error) {
	var platforms []ProviderPlatform
	if err := s.db.Model(ProviderPlatform{}).Preload("OidcClient").
		Find(&platforms, "state = ?", "enabled").Error; err != nil {
		return nil, newGetRecordsDBError(err, "provider_platforms")
	}
	for i := range platforms {
		if platforms[i].OidcClient != nil {
			platforms[i].OidcID = platforms[i].OidcClient.ID
		}
	}
	return platforms, nil
}

// getProviderPlatformByID returns the zero ProviderPlatform, not an error,
// when there is no such provider.
func (s *store) getProviderPlatformByID(id int) (*ProviderPlatform, error) {
	var platform ProviderPlatform
	if err := s.db.Model(ProviderPlatform{}).Preload("OidcClient").
		Find(&platform, "id = ?", id).Error; err != nil {
		return nil, newNotFoundDBError(err, "provider_platforms")
	}
	if platform.OidcClient != nil {
		platform.OidcID = platform.OidcClient.ID
	}
	return &platform, nil
}

func (s *store) createProviderPlatform(ctx context.Context, platform *ProviderPlatform) error {
	if key, err := EncryptAccessKey(platform.AccessKey); err == nil {
		platform.AccessKey = key
	}
	if err := s.db.WithContext(ctx).Create(platform).Error; err != nil {
		return newCreateDBError(err, "provider_platforms")
	}
	return nil
}

func (s *store) updateProviderPlatform(ctx context.Context, platform *ProviderPlatform, id uint) (*ProviderPlatform, error) {
	log.Printf("Updating provider platform with ID: %d", id)
	db := s.db.WithContext(ctx)
	var existingPlatform ProviderPlatform
	if err := db.First(&existingPlatform, id).Error; err != nil {
		return nil, newUpdateDBError(err, "provider_platforms")
	}
	// at this point, they are both decrypted
	if platform.AccessKey != "" && existingPlatform.AccessKey != platform.AccessKey {
		if key, err := EncryptAccessKey(platform.AccessKey); err == nil {
			platform.AccessKey = key
		}
	}
	updateStruct(&existingPlatform, platform)
	if err := db.Save(&existingPlatform).Error; err != nil {
		return nil, newUpdateDBError(err, "provider_platforms")
	}
	return &existingPlatform, nil
}

func (s *store) deleteProviderPlatform(id int) error {
	log.Printf("Deleting provider platform with ID: %d", id)
	if err := s.db.Delete(&ProviderPlatform{}, fmt.Sprintf("%d", id)).Error; err != nil {
		return newDeleteDBError(err, "provider_platforms")
	}
	return nil
}

func (s *store) findKolibriInstance() (*ProviderPlatform, error) {
	kolibri := ProviderPlatform{}
	if err := s.db.First(&kolibri, "type = ?", "kolibri").Error; err != nil {
		log.Error("error getting kolibri provider platform")
		return nil, newNotFoundDBError(err, "provider_platforms")
	}
	return &kolibri, nil
}

// updateProviderAccountID saves the Kolibri facility a provider found on its
// first user.
func (s *store) updateProviderAccountID(provider *ProviderPlatform, accountID string) error {
	return s.db.Model(provider).Update("account_id", accountID).Error
}

// Provider user mappings

func (s *store) createProviderUserMapping(providerUserMapping *ProviderUserMapping) error {
	if err := s.db.Create(providerUserMapping).Error; err != nil {
		return newCreateDBError(err, "provider_user_mappings")
	}
	return nil
}

func (s *store) getProviderUserMapping(userID, providerID int) (*ProviderUserMapping, error) {
	var providerUserMapping ProviderUserMapping
	if err := s.db.Model(&ProviderUserMapping{}).
		First(&providerUserMapping, "user_id = ? AND provider_platform_id = ?", userID, providerID).
		Error; err != nil {
		return nil, newNotFoundDBError(err, "provider_user_mappings")
	}
	return &providerUserMapping, nil
}

func (s *store) updateProviderUserMapping(providerUserMapping *ProviderUserMapping) error {
	result := s.db.Model(&ProviderUserMapping{}).Where("id = ?", providerUserMapping.ID).Updates(providerUserMapping)
	if result.Error != nil {
		return newUpdateDBError(result.Error, "provider_user_mappings")
	}
	if result.RowsAffected == 0 {
		return newUpdateDBError(gorm.ErrRecordNotFound, "provider_user_mappings")
	}
	return nil
}

func (s *store) getAllUnmappedUsers(providerID int, facilityID uint) ([]user, error) {
	var users []user
	tx := s.db.Model(&user{}).
		Where(
			"role = ? AND id NOT IN (?)",
			"student",
			s.db.Model(&ProviderUserMapping{}).
				Select("user_id").
				Where("provider_platform_id = ?", providerID),
		)
	if facilityID != 0 {
		tx = tx.Where("facility_id = ?", facilityID)
	}
	if err := tx.Find(&users).Error; err != nil {
		return nil, newDBError(err, "error getting all unmapped users")
	}
	return users, nil
}

func (s *store) getAllProviderMappingsForUser(userID int) ([]ProviderUserMapping, error) {
	var providerUserMappings []ProviderUserMapping
	if err := s.db.Model(&ProviderUserMapping{}).Find(&providerUserMappings, "user_id = ?", userID).Error; err != nil {
		return nil, newGetRecordsDBError(err, "provider_user_mappings")
	}
	return providerUserMappings, nil
}

func (s *store) getMappedUsers(args *queryContext, providerID int) ([]user, error) {
	var users []user
	tx := s.db.Model(&user{}).
		Where("id IN (?)",
			s.db.Model(&ProviderUserMapping{}).Select("user_id").Where("provider_platform_id = ?", providerID),
		)
	if args.FacilityID != 0 {
		tx = tx.Where("facility_id = ?", args.FacilityID)
	}
	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newDBError(err, "error counting mapped users")
	}
	if err := tx.Offset(args.CalcOffset()).Limit(args.PerPage).Find(&users).Error; err != nil {
		return nil, newDBError(err, "error getting mapped users")
	}
	return users, nil
}

// getMappedUsersExternalIDs returns a map of user_id → external_user_id for all
// users mapped to the given provider platform. Used to join with live Canvas data.
func (s *store) getMappedUsersExternalIDs(providerID int) (map[uint]string, error) {
	var rows []struct {
		UserID         uint   `gorm:"column:user_id"`
		ExternalUserID string `gorm:"column:external_user_id"`
	}
	if err := s.db.Model(&ProviderUserMapping{}).
		Select("user_id, external_user_id").
		Where("provider_platform_id = ?", providerID).
		Find(&rows).Error; err != nil {
		return nil, newDBError(err, "error getting mapped user external IDs")
	}
	result := make(map[uint]string, len(rows))
	for _, row := range rows {
		result[row.UserID] = row.ExternalUserID
	}
	return result, nil
}

func (s *store) deleteProviderUserMappingByUserID(userID, providerID int) error {
	result := s.db.Model(&ProviderUserMapping{}).Where("user_id = ? AND provider_platform_id = ?", userID, providerID).Delete(&ProviderUserMapping{})
	if result.Error != nil {
		return newDeleteDBError(result.Error, "provider_user_mappings")
	}
	if result.RowsAffected == 0 {
		return newDeleteDBError(gorm.ErrRecordNotFound, "provider_user_mappings")
	}
	return nil
}

// OIDC clients

func (s *store) getAllRegisteredClients() ([]OidcClient, error) {
	var clients []OidcClient

	if err := s.db.Find(&clients).Error; err != nil {
		return clients, newGetRecordsDBError(err, "oidc_clients")
	}
	return clients, nil
}

func (s *store) registerClient(client *OidcClient) error {
	if err := s.db.Create(client).Error; err != nil {
		return newCreateDBError(err, "oidc_clients")
	}
	return nil
}

func (s *store) getOidcClientById(id string) (*OidcClient, error) {
	client := &OidcClient{}
	if err := s.db.Find(client, "id = ?", id).Error; err != nil {
		return client, newNotFoundDBError(err, "oidc_clients")
	}
	return client, nil
}

func (s *store) deleteOidcClient(id uint) error {
	if err := s.db.Delete(&OidcClient{}, id).Error; err != nil {
		return newDeleteDBError(err, "oidc_clients")
	}
	return nil
}

// Courses, outcomes and activity

func (s *store) getCourseByID(id int) (*Course, error) {
	content := &Course{}
	if err := s.db.First(content, id).Error; err != nil {
		return nil, err
	}
	return content, nil
}

func (s *store) getCourses(args *queryContext) ([]Course, error) {
	content := make([]Course, 0, args.PerPage)
	total := int64(0)
	if args.Search != "" {
		search := args.SearchQuery()
		tx := s.db.Model(&Course{}).
			Where("LOWER(courses.name) LIKE ?", search).
			Or("LOWER(courses.alt_name) LIKE ?", search).
			Or("LOWER(provider_platforms.name) LIKE ?", search).
			Joins("LEFT JOIN provider_platforms ON courses.provider_platform_id = provider_platforms.id")

		if err := tx.Count(&args.Total).Error; err != nil {
			return nil, newNotFoundDBError(err, "courses")
		}

		if err := tx.Limit(args.PerPage).Offset(args.CalcOffset()).Find(&content).Error; err != nil {
			return nil, newNotFoundDBError(err, "courses")
		}
	} else {
		tx := s.db.Model(&Course{})
		if err := tx.Count(&total).Error; err != nil {
			return nil, newNotFoundDBError(err, "courses")
		}
		if err := tx.Limit(args.PerPage).Offset(args.CalcOffset()).Find(&content).Error; err != nil {
			return nil, err
		}
	}
	return content, nil
}

func (s *store) getOutcomesForUser(args *queryContext, outcomeType OutcomeType) ([]Outcome, error) {
	var outcomes []Outcome
	fields := []string{"created_at", "type"}
	if !slices.Contains(fields, args.OrderBy) {
		args.OrderBy = "created_at"
	}

	query := s.db.WithContext(args.Ctx).Model(&Outcome{}).Where("user_id = ?", args.UserID)

	if outcomeType != "" {
		query = query.Where("type = ?", outcomeType)
	}

	if err := query.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "outcomes")
	}
	return outcomes, query.Order(args.OrderClause("outcomes.created_at DESC")).Offset(args.CalcOffset()).Limit(args.PerPage).Find(&outcomes).Error
}

func (s *store) getDailyActivityByUserID(userID int, startDate time.Time, endDate time.Time) ([]DailyActivity, error) {
	days := int(math.Ceil(endDate.Sub(startDate).Hours() / 24))
	activities := make([]Activity, 0, days)
	if err := s.db.Where("user_id = ? AND created_at BETWEEN ? AND ?", userID, startDate, endDate).Find(&activities).Error; err != nil {
		return nil, newGetRecordsDBError(err, "activities")
	}
	// Combine activities based on date
	dailyActivities := make(map[time.Time]DailyActivity, days)
	for _, activity := range activities {
		date := activity.CreatedAt.Truncate(24 * time.Hour)
		if dailyActivity, ok := dailyActivities[date]; ok {
			dailyActivity.TotalTime += uint(activity.TimeDelta)
			dailyActivity.Activities = append(dailyActivity.Activities, activity)
			dailyActivities[date] = dailyActivity
		} else {
			dailyActivities[date] = DailyActivity{
				Date:       date,
				TotalTime:  uint(activity.TimeDelta),
				Activities: []Activity{activity},
			}
		}
	}

	// Convert map to slice
	var dailyActivityList []DailyActivity
	for _, dailyActivity := range dailyActivities {
		dailyActivityList = append(dailyActivityList, dailyActivity)
	}

	// Sort daily activity list by total time
	sort.Slice(dailyActivityList, func(i, j int) bool {
		return dailyActivityList[i].TotalTime < dailyActivityList[j].TotalTime
	})

	// Calculate quartiles for each day's activities
	n := len(dailyActivityList)
	for i := range dailyActivityList {
		switch {
		case i < n/4:
			dailyActivityList[i].Quartile = 1
		case i < n/2:
			dailyActivityList[i].Quartile = 2
		case i < 3*n/4:
			dailyActivityList[i].Quartile = 3
		default:
			dailyActivityList[i].Quartile = 4
		}
	}

	// sort by date
	sort.Slice(dailyActivityList, func(i, j int) bool {
		return dailyActivityList[i].Date.Before(dailyActivityList[j].Date)
	})

	return dailyActivityList, nil
}

func (s *store) getActivityByCourseID(page, perPage, courseID int) (int64, []Activity, error) {
	activities := make([]Activity, 0, perPage)
	var count int64
	tx := s.db.Model(&Activity{}).Where("course_id = ?", courseID)
	if err := tx.Count(&count).Error; err != nil {
		return count, nil, newGetRecordsDBError(err, "activities")
	}
	if err := tx.Offset(calcOffset(page, perPage)).Limit(perPage).Find(&activities).Error; err != nil {
		return count, nil, newGetRecordsDBError(err, "activities")
	}
	return count, activities, nil
}

// A user's course catalog and courses

type UserCatalogJoin struct {
	CourseID     uint       `json:"course_id"`
	ThumbnailURL string     `json:"thumbnail_url"`
	CourseName   string     `json:"course_name"`
	ProviderName string     `json:"provider_name"`
	ExternalURL  string     `json:"external_url"`
	CourseType   string     `json:"course_type"`
	Description  string     `json:"description"`
	OutcomeTypes string     `json:"outcome_types"`
	StartDt      *time.Time `json:"start_dt"`
	EndDt        *time.Time `json:"end_dt"`
}

func (s *store) getUserCatalog(userId int, tags []string, search, order string) ([]UserCatalogJoin, error) {
	catalog := []UserCatalogJoin{}
	tx := s.db.Table("courses c").
		Select(`c.id as course_id, c.thumbnail_url, c.name as course_name,
		pp.name as provider_name, c.external_url, c.type as course_type, c.description, c.outcome_types, c.start_dt, c.end_dt`).
		Joins("LEFT JOIN provider_platforms pp ON c.provider_platform_id = pp.id").
		Where("c.deleted_at IS NULL").
		Where("pp.deleted_at IS NULL")
	for i, tag := range tags {
		if i == 0 {
			tx.Where("LOWER(c.outcome_types) LIKE ?", "%"+strings.ToLower(tag)+"%")
		} else {
			tx.Or("LOWER(c.outcome_types) LIKE ?", "%"+strings.ToLower(tag)+"%")
		}
		tx.Or("LOWER(c.type) LIKE ?", "%"+strings.ToLower(tag)+"%")
	}
	if search != "" {
		tx.Where("LOWER(c.name) LIKE ?", "%"+search+"%")
	}
	tx.Order(fmt.Sprintf("c.name %s", validOrder(order)))
	err := tx.Scan(&catalog).Error
	if err != nil {
		return nil, newDBError(err, "error getting user catalog info")
	}
	return catalog, nil
}

type UserCoursesInfo struct {
	Courses    []UserCourses `json:"courses"`
	Completed  uint          `json:"num_completed"`
	InProgress uint          `json:"num_in_progress"`
	TotalTime  uint          `json:"total_time"`
}

type UserCourses struct {
	ID             uint       `json:"id"`
	ThumbnailURL   string     `json:"thumbnail_url"`
	CourseName     string     `json:"course_name"`
	Description    string     `json:"description"`
	ProviderName   string     `json:"provider_platform_name"`
	ExternalURL    string     `json:"external_url"`
	CourseProgress float64    `json:"course_progress"`
	TotalTime      uint       `json:"total_time"`
	StartDt        *time.Time `json:"start_dt"`
	EndDt          *time.Time `json:"end_dt"`
}

func validOrder(str string) string {
	if slices.Contains([]string{"asc", "desc"}, strings.ToLower(str)) {
		return strings.ToLower(str)
	}
	return "desc"
}

func (s *store) getUserCourses(qCtx *queryContext) (UserCoursesInfo, error) {
	var courseInfo UserCoursesInfo
	courses := []UserCourses{}
	fieldMap := map[string]string{
		"course_name":     "c.name",
		"provider_name":   "pp.name",
		"course_progress": "course_progress",
		"total_time":      "total_time",
		"start_dt":        "c.start_dt",
		"end_dt":          "c.end_dt",
		"recent_activity": "a.last_ts",
	}
	dbField, ok := fieldMap[qCtx.OrderBy]
	if !ok {
		dbField = "c.name"
	}
	orderStr := dbField + " " + validOrder(qCtx.Order)
	tx := s.db.Table("courses c").
		Select(`c.id, c.thumbnail_url,
		c.name as course_name, c.description, pp.name as provider_name, c.external_url, c.start_dt, c.end_dt,
		progress.course_progress, a.total_time as total_time`).
		Joins("JOIN provider_platforms pp ON c.provider_platform_id = pp.id").
		Joins("JOIN milestones as m ON m.course_id = c.id and m.user_id = ?", qCtx.UserID).
		Joins(`JOIN (Select cc.id,
				CASE WHEN EXISTS (SELECT 1 FROM outcomes o WHERE o.course_id = cc.id AND o.user_id = ?) THEN 100
					WHEN cc.total_progress_milestones = 0 THEN 0
				ELSE
				CASE WHEN (SELECT COUNT(m.id) * 100.0 / cc.total_progress_milestones
						FROM milestones m
						WHERE m.course_id = cc.id AND m.user_id = ?) = 100 THEN 99.999
				ELSE (SELECT COUNT(m.id) * 100.0 / cc.total_progress_milestones
					FROM milestones m
					WHERE m.course_id = cc.id AND m.user_id = ?
				) END
				END as course_progress
				from courses cc
				where cc.deleted_at IS NULL
				) progress on progress.id = c.id`, qCtx.UserID, qCtx.UserID, qCtx.UserID).
		Joins(`LEFT JOIN user_course_activity_totals a on a.course_id = c.id and a.user_id = m.user_id`).
		Joins("LEFT JOIN outcomes o ON o.course_id = c.id AND o.user_id = m.user_id").
		Where("c.deleted_at IS NULL").
		Where("pp.deleted_at IS NULL")

	tx = tx.Order(orderStr)

	if qCtx.Search != "" {
		tx = tx.Where("LOWER(c.name) LIKE ?", "%"+qCtx.Search+"%")
	}
	for i, tag := range qCtx.Tags {
		var query string
		switch tag {
		case "completed":
			query = "o.type IS NOT NULL"
		case "in_progress":
			query = "o.type IS NULL"
		}
		if i == 0 {
			tx.Where(query)
		} else {
			tx.Or(query)
		}
	}
	tx.Group("c.id, c.name, c.description, c.start_dt, c.end_dt, a.total_time, c.thumbnail_url, pp.name, c.external_url, progress.course_progress, a.last_ts")
	err := tx.Scan(&courses).Error
	if err != nil {
		return courseInfo, newDBError(err, "error getting user programs")
	}
	var (
		totalTime     uint
		numCompleted  int64
		numInProgress int64
	)
	for _, course := range courses {
		totalTime += course.TotalTime
		if int(course.CourseProgress) == 100 {
			numCompleted++
		} else {
			numInProgress++
		}
	}
	courseInfo.Completed = uint(numCompleted)
	courseInfo.InProgress = uint(numInProgress)
	courseInfo.TotalTime = totalTime
	courseInfo.Courses = courses
	return courseInfo, nil
}

// LMS job bookkeeping

// getOrCreateTask returns the provider's runnable_tasks row for job's
// cron_jobs row, creating either one the first time. cj is the cron_jobs row
// to create; an existing row with its name is used as it is.
func (s *store) getOrCreateTask(ctx context.Context, cj cronJob, providerID uint) (*runnableTask, error) {
	var job cronJob
	if err := s.db.WithContext(ctx).Where("name = ?", cj.Name).Attrs(cj).FirstOrCreate(&job).Error; err != nil {
		return nil, newGetRecordsDBError(err, "cron_jobs")
	}
	var task runnableTask
	if err := s.db.WithContext(ctx).
		Where("job_id = ? AND provider_platform_id = ?", job.ID, providerID).
		Attrs(runnableTask{JobID: job.ID, ProviderPlatformID: &providerID, Status: taskPending}).
		FirstOrCreate(&task).Error; err != nil {
		return nil, newGetRecordsDBError(err, "runnable_tasks")
	}
	return &task, nil
}

func (s *store) setTaskStatus(ctx context.Context, taskID uint, status taskStatus) error {
	if err := s.db.WithContext(ctx).Model(&runnableTask{}).Where("id = ?", taskID).Update("status", status).Error; err != nil {
		return newUpdateDBError(err, "runnable_tasks")
	}
	return nil
}
