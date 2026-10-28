package database

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/registry"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func calcOffset(page, perPage int) int {
	return (page - 1) * perPage
}

func (db *DB) GetCurrentUsers(args *models.QueryContext, role string) ([]models.User, error) {
	tx := db.WithContext(args.Ctx).Model(models.User{}).
		Preload("LoginMetrics").
		Preload("Facility")

	if !args.IncludeDeactivated {
		tx = tx.Where("deactivated_at IS NULL")
	}

	switch role {
	case "system_admin":
		tx = tx.Where("role IN ('system_admin', 'department_admin', 'facility_admin')")
		if args.FacilityID > 0 {
			tx = tx.Where("facility_id = ?", args.FacilityID)
		}
	case "department_admin":
		tx = tx.Where("role IN ('department_admin', 'facility_admin')")
		if args.FacilityID > 0 {
			tx = tx.Where("facility_id = ?", args.FacilityID)
		}
	case "facility_admin":
		tx = tx.Where("facility_id = ? AND role = 'facility_admin'", args.FacilityID)
	case "student":
		tx = tx.Where("role = 'student'")
		if args.FacilityID > 0 {
			tx = tx.Where("facility_id = ?", args.FacilityID)
		}
	default:
		if args.FacilityID > 0 {
			tx = tx.Where("facility_id = ?", args.FacilityID)
		}
	}
	if args.Search != "" {
		tx = fuzzySearchUsers(tx, args)
	}
	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}
	users := make([]models.User, 0, args.PerPage)
	validUserOrderByFields := []string{"name_last", "created_at", "last_login", "doc_id", "username", "role", "facility_name"}
	if !slices.Contains(validUserOrderByFields, args.OrderBy) {
		args.OrderBy = "name_last"
	}
	orderClause := adjustUserOrderBy(args.OrderClause("users.name_last desc"))
	if args.OrderBy == "last_login" {
		tx = tx.Joins("LEFT JOIN login_metrics ON login_metrics.user_id = users.id")
		orderClause = adjustLastLoginOrderBy(orderClause)
	}
	if args.OrderBy == "facility_name" {
		tx = tx.Joins("LEFT JOIN facilities ON facilities.id = users.facility_id")
		orderClause = adjustFacilityNameOrderBy(orderClause)
	}
	if err := tx.Order(orderClause).
		Offset(args.CalcOffset()).
		Limit(args.PerPage).
		Find(&users).
		Error; err != nil {
		log.Errorf("Error fetching users: %v", err)
		return nil, newGetRecordsDBError(err, "users")
	}
	return users, nil
}

type UserStats struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	Inactive int64 `json:"inactive"`
}

func (db *DB) GetUserStats(ctx context.Context, facilityID *uint) (*UserStats, error) {
	var stats UserStats
	baseTx := db.WithContext(ctx).Model(&models.User{}).Where("role = ?", models.Student)
	if facilityID != nil {
		baseTx = baseTx.Where("facility_id = ?", *facilityID)
	}
	if err := baseTx.Count(&stats.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}
	daysAgo := time.Now().AddDate(0, 0, -30)
	activeTx := db.WithContext(ctx).Model(&models.User{}).
		Where("role = ?", models.Student).
		Joins("JOIN login_metrics ON users.id = login_metrics.user_id AND login_metrics.last_login > ?", daysAgo)
	if facilityID != nil {
		activeTx = activeTx.Where("facility_id = ?", *facilityID)
	}
	if err := activeTx.Count(&stats.Active).Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}
	stats.Inactive = stats.Total - stats.Active
	return &stats, nil
}

func (db *DB) GetUserByDocIDAndID(ctx context.Context, docID string, userID int) (*models.User, error) {
	user := models.User{}
	if err := db.WithContext(ctx).First(&user, "LOWER(doc_id) = ? and id = ?", strings.ToLower(docID), userID).Error; err != nil {
		return nil, newNotFoundDBError(err, "users")
	}
	return &user, nil
}

func (db *DB) GetUsersByIDs(ctx context.Context, userIDs []uint, facID uint, roles ...models.UserRole) ([]models.User, error) {
	users := make([]models.User, 0, len(userIDs))
	tx := db.WithContext(ctx).Where("id IN ?", userIDs)
	if len(roles) > 0 {
		tx = tx.Where("role IN ?", roles)
	}
	if facID != 0 {
		tx = tx.Where("facility_id = ?", facID)
	}
	if err := tx.Find(&users).Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}
	return users, nil
}

func (db *DB) TransferResident(ctx *models.QueryContext, userID int, currFacilityID int, transFacilityID int) (*gorm.DB, error) {
	trans := db.Begin()
	if trans.Error != nil {
		return nil, NewDBError(trans.Error, "unable to start DB transaction")
	}
	if err := registry.Programs(trans).EndEnrollmentsForTransfer(trans.Statement.Context, uint(userID), uint(currFacilityID)); err != nil {
		trans.Rollback()
		return nil, newUpdateDBError(err, "program_class_enrollments")
	}
	if err := trans.Model(&models.User{}).
		Where("id = ?", userID).
		Update("facility_id", transFacilityID).Error; err != nil {
		trans.Rollback()
		return nil, newUpdateDBError(err, "users")
	}
	return trans, nil
}

// GetResidentsByIDs is a page of the residents with the given IDs, searched
// and ordered as the users list is, by defaultOrder when the request has none.
func (db *DB) GetResidentsByIDs(args *models.QueryContext, ids []uint, defaultOrder string) ([]models.User, error) {
	tx := db.WithContext(args.Ctx).Model(&models.User{}).
		Where("users.id IN ?", ids).
		Where("users.role = 'student'")

	if args.SearchQuery() != "" {
		tx = fuzzySearchUsers(tx, args)
	}
	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}
	users := make([]models.User, 0, args.PerPage)
	if err := tx.Order(adjustUserOrderBy(args.OrderClause(defaultOrder))).
		Offset(args.CalcOffset()).
		Limit(args.PerPage).
		Find(&users).
		Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}
	return users, nil
}

// This function takes an existing transaction and applies proper searching through first, last, username + doc_id
func fuzzySearchUsers(tx *gorm.DB, ctx *models.QueryContext) *gorm.DB {
	likeSearch := ctx.SearchQuery()
	_, err := strconv.Atoi(ctx.Search)
	if err == nil {
		// optimization if a number is entered, they are searching for their DOC#
		tx = tx.Where("LOWER(doc_id) LIKE ?", likeSearch)
	} else {
		tx = tx.Where(`(? <% concat_ws(' ', name_first, name_last, username)
				OR concat_ws(' ', name_first, name_last, username, doc_id) ILIKE ?)`,
			likeSearch, likeSearch)
	}
	return tx
}

func (db *DB) GetUserRoleByID(ctx context.Context, id string) (models.UserRole, error) {
	var role string
	if err := db.WithContext(ctx).Model(&models.User{}).Select("role").Where("id = ?", id).First(&role).Error; err != nil {
		return "", newNotFoundDBError(err, "users")
	}
	return models.UserRole(role), nil
}

func (db *DB) GetUserByID(id uint) (*models.User, error) {
	user := models.User{}
	if err := db.Preload("Facility").Preload("LoginMetrics").First(&user, id).Error; err != nil {
		return nil, newNotFoundDBError(err, "users")
	}
	return &user, nil
}

func (db *DB) GetSystemAdmin(ctx context.Context) (*models.User, error) {
	user := models.User{}
	if err := db.WithContext(ctx).First(&user, "role = 'system_admin'").Error; err != nil {
		return nil, newNotFoundDBError(err, "system admin")
	}
	return &user, nil
}

func (db *DB) CreateUser(user *models.User) error {
	err := Validate().Struct(user)
	if err != nil {
		return NewDBError(err, "user")
	}
	error := db.Create(user).Error
	if error != nil {
		return newCreateDBError(error, "users")
	}
	return nil
}

func (db *DB) DeleteUser(id int) error {
	return db.Transaction(func(tx *gorm.DB) error {
		updates := db.softDeleteMap()
		result := tx.Model(&models.User{}).
			Where("id = ? AND deleted_at IS NULL", id).
			Updates(updates)
		if result.Error != nil {
			return newDeleteDBError(result.Error, "users")
		}
		if result.RowsAffected == 0 {
			return newDeleteDBError(gorm.ErrRecordNotFound, "users")
		}
		if err := registry.ProviderPlatforms(tx).DeleteUserMappings(context.Background(), uint(id)); err != nil {
			return newDeleteDBError(err, "provider_user_mappings")
		}
		return nil
	})
}

// Usernames are unique case-insensitively (see UserIdentityExists), so login
// matches on the folded value — a user stored as "Calisio" can sign in as
// "calisio" (ID-849). Backed by idx_users_username_lower (migration 00076);
// a plain equality index cannot serve a LOWER() predicate.
//
// First() emits ORDER BY id LIMIT 1, so if legacy data does contain a
// case-duplicate pair the lowest id wins deterministically rather than
// resolving by physical row order.
func (db *DB) GetUserByUsername(username string) (*models.User, error) {
	var user models.User
	if err := db.Model(models.User{}).First(&user, "LOWER(username) = ?", strings.ToLower(username)).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (db *DB) UserIdentityExists(username string, doc string) (bool, bool) {
	var usernameExists bool
	var docIDExists bool
	if err := db.Model(&models.User{}).
		Select("1").
		Where("LOWER(username) = ? OR email = ?", strings.ToLower(username), username+"@unlocked.v2").
		Limit(1).
		Find(&usernameExists).Error; err != nil {
		log.Error("Error checking username: ", err)
	}
	if doc != "" {
		if err := db.Model(&models.User{}).
			Select("1").
			Where("doc_id = ?", doc).
			Limit(1).
			Find(&docIDExists).Error; err != nil {
			log.Error("Error checking doc_id: ", err)
		}
	}
	return usernameExists, docIDExists
}

func (db *DB) UpdateUser(user *models.User) error {
	if user.ID == 0 {
		return newUpdateDBError(errors.New("invalid user ID"), "users")
	}
	err := db.Model(&models.User{}).Where("id = ?", user.ID).Updates(user).Error
	if err != nil {
		return newUpdateDBError(err, "users")
	}
	return nil
}

func (db *DB) IncrementUserLogin(user *models.User) (int64, error) {
	var total int64
	if err := db.Raw(
		`INSERT INTO login_metrics (user_id, total, last_login)
		 VALUES (?, 1, CURRENT_TIMESTAMP)
		 ON CONFLICT (user_id) DO UPDATE
		 SET total = login_metrics.total + 1, last_login = CURRENT_TIMESTAMP
		 RETURNING total`,
		user.ID).Scan(&total).Error; err != nil {
		log.Errorf("Error incrementing login count: %v", err)
		return 0, newUpdateDBError(err, "login_metrics")
	}
	if !user.IsAdmin() {
		now := time.Now()
		rounded := now.Truncate(time.Hour)
		if err := db.Exec(
			`INSERT INTO login_activity (time_interval, facility_id, total_logins)
			 VALUES (?, ?, 1)
			 ON CONFLICT (time_interval, facility_id)
			 DO UPDATE SET total_logins = login_activity.total_logins + 1`,
			rounded, user.FacilityID).Error; err != nil {
			log.Errorf("Error incrementing login activity: %v", err)
			return 0, newUpdateDBError(err, "login_activity")
		}
	}
	return total, nil
}

func (db *DB) LogUserSessionStarted(userID uint, sessionID string) {
	if db.Where("user_id = ? and session_id = ?", userID, sessionID).First(&models.UserSessionTracking{}).RowsAffected > 0 {
		log.Warn("The record already exists skipping the log in activity")
		return
	}

	userTracking := models.UserSessionTracking{
		UserID:         userID,
		SessionStartTS: time.Now(),
		SessionID:      sessionID,
	}
	if err := db.Create(&userTracking).Error; err != nil {
		log.Warnf("Unable to insert user for session tracking: %v", err)
	}
}

func (db *DB) LogUserSessionEnded(userID uint, sessionID string) {
	var userSessionTracking models.UserSessionTracking
	if err := db.Where("user_id = ? and session_id = ?", userID, sessionID).Order("session_start_ts desc").First(&userSessionTracking).Error; err != nil {
		log.Warnf("Unable to find user record to update user for session tracking: %v", err)
	}

	if err := db.Model(&userSessionTracking).Update("session_end_ts", time.Now()).Error; err != nil {
		log.Warnf("Unable to update user for session tracking: %v", err)
	}
}

func (db *DB) GetNumberOfActiveUsersForTimePeriod(args *models.QueryContext, active bool, start, end *time.Time, facilityId *uint) (int64, int, error) {
	var count int64
	var tx *gorm.DB
	join := "JOIN login_metrics on users.id = login_metrics.user_id"
	tx = db.WithContext(args.Ctx).Model(&models.User{})
	var totalDays int
	if start == nil || end == nil {
		tx = tx.Joins(join)
		sysAdmin, err := db.GetSystemAdmin(args.Ctx)
		if err != nil {
			return 0, 0, err
		}
		totalDays = int(time.Since(sysAdmin.CreatedAt).Hours() / 24)
	} else {
		if active {
			tx = tx.Joins(join+" AND login_metrics.last_login >= ? AND login_metrics.last_login < ?", *start, *end)
		} else {
			tx = tx.Joins(join+" AND login_metrics.last_login < ?", *start)
		}
		totalDays = int(end.Sub(*start).Hours() / 24)
	}
	if facilityId != nil {
		tx = tx.Where("facility_id = ?", *facilityId)
	}
	tx = tx.Where("role = 'student'")
	if err := tx.Count(&count).Error; err != nil {
		return 0, totalDays, newGetRecordsDBError(err, "users")
	}
	if totalDays <= 0 {
		totalDays = 1
	}
	return count, totalDays, nil
}

func (db *DB) NewUsersInTimePeriod(args *models.QueryContext, start, end *time.Time, facilityId *uint) (int64, error) {
	var resident_count int64
	tx := db.WithContext(args.Ctx).Model(&models.User{}).Where("role = 'student'")
	if start != nil && end != nil {
		tx = tx.Where("created_at >= ? AND created_at < ?", *start, *end)
	}
	if facilityId != nil {
		tx = tx.Where("facility_id = ?", *facilityId)
	}
	if err := tx.Count(&resident_count).Error; err != nil {
		return 0, newGetRecordsDBError(err, "users")
	}
	return resident_count, nil
}

func (db *DB) GetTotalLogins(args *models.QueryContext, start, end *time.Time, facilityId *uint) (int64, error) {
	var total sql.NullInt64
	tx := db.WithContext(args.Ctx).Model(&models.LoginActivity{}).Select("SUM(COALESCE(total_logins, 0))")
	if start != nil && end != nil {
		tx = tx.Where("time_interval >= ? AND time_interval < ?", *start, *end)
	}
	if facilityId != nil {
		tx = tx.Where("facility_id = ?", *facilityId)
	}
	if err := tx.Scan(&total).Error; err != nil {
		return 0, newGetRecordsDBError(err, "login_activity")
	}
	if !total.Valid {
		return 0, nil
	}
	return total.Int64, nil
}

func (db *DB) GetTotalUsers(args *models.QueryContext, facilityId *uint) (int64, error) {
	var totalResidents int64
	tx := db.WithContext(args.Ctx).Model(&models.User{}).Where("role = 'student'")
	if facilityId != nil {
		tx = tx.Where("facility_id = ?", *facilityId)
	}
	if err := tx.Count(&totalResidents).Error; err != nil {
		return 0, newGetRecordsDBError(err, "users")
	}
	return totalResidents, nil
}

func (db *DB) GetTotalAdmins(args *models.QueryContext, facilityId *uint) (int64, error) {
	var totalAdmins int64
	staffRoles := []models.UserRole{models.FacilityAdmin, models.DepartmentAdmin}
	tx := db.WithContext(args.Ctx).Model(&models.User{}).Where("role IN ?", staffRoles)
	if facilityId != nil {
		tx = tx.Where("facility_id = ?", *facilityId)
	}
	if err := tx.Count(&totalAdmins).Error; err != nil {
		return 0, newGetRecordsDBError(err, "users")
	}
	return totalAdmins, nil
}

func (db *DB) GetLoginActivity(args *models.QueryContext, start, end *time.Time, facilityID *uint) ([]models.LoginActivity, error) {
	acitvity := make([]models.LoginActivity, 0, 3)
	sql := `SELECT time_interval, total_logins
			FROM login_activity`
	conds := make([]string, 0, 2)
	params := make([]any, 0, 3)
	if facilityID != nil {
		conds = append(conds, "facility_id = ?")
		params = append(params, *facilityID)
	}
	if start != nil && end != nil {
		conds = append(conds, "time_interval >= ? AND time_interval < ?")
		params = append(params, *start, *end)
	}
	if len(conds) > 0 {
		sql += " WHERE " + strings.Join(conds, " AND ")
	}
	sql += " ORDER BY total_logins DESC LIMIT 3;"
	if err := db.WithContext(args.Ctx).Raw(sql, params...).Scan(&acitvity).Error; err != nil {
		return nil, newGetRecordsDBError(err, "login_activity")
	}
	return acitvity, nil
}

func (db *DB) GetDailyLoginActivity(args *models.QueryContext, start, end *time.Time, facilityID *uint) ([]models.DailyLoginCount, error) {
	activities := make([]models.LoginActivity, 0)
	tx := db.WithContext(args.Ctx).Model(&models.LoginActivity{}).Select("time_interval, total_logins")
	if facilityID != nil {
		tx = tx.Where("facility_id = ?", *facilityID)
	}
	if start != nil && end != nil {
		tx = tx.Where("time_interval >= ? AND time_interval < ?", *start, *end)
	}
	if err := tx.Find(&activities).Error; err != nil {
		return nil, newGetRecordsDBError(err, "login_activity")
	}
	return bucketLoginsByDay(activities), nil
}

func bucketLoginsByDay(activities []models.LoginActivity) []models.DailyLoginCount {
	totals := make(map[string]int64)
	days := make([]string, 0)
	for _, activity := range activities {
		day := activity.TimeInterval.UTC().Format(time.DateOnly)
		if _, seen := totals[day]; !seen {
			days = append(days, day)
		}
		totals[day] += activity.TotalLogins
	}
	slices.Sort(days)
	trend := make([]models.DailyLoginCount, 0, len(days))
	for _, day := range days {
		trend = append(trend, models.DailyLoginCount{Date: day, TotalLogins: totals[day]})
	}
	return trend
}

func (db *DB) GetFacilityEngagementComparison(args *models.QueryContext, start, end *time.Time) ([]models.FacilityEngagement, error) {
	facilities := make([]models.Facility, 0)
	if err := db.WithContext(args.Ctx).Order("name ASC").Find(&facilities).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}

	type facilityCount struct {
		FacilityID uint
		Count      int64
	}
	toMap := func(rows []facilityCount) map[uint]int64 {
		m := make(map[uint]int64, len(rows))
		for _, row := range rows {
			m[row.FacilityID] = row.Count
		}
		return m
	}

	registered := make([]facilityCount, 0, len(facilities))
	if err := db.WithContext(args.Ctx).Model(&models.User{}).
		Select("facility_id, count(*) as count").
		Where("role = ?", models.Student).
		Group("facility_id").
		Find(&registered).Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}

	active := make([]facilityCount, 0, len(facilities))
	activeTx := db.WithContext(args.Ctx).Model(&models.User{}).
		Select("users.facility_id, count(*) as count").
		Joins("JOIN login_metrics ON login_metrics.user_id = users.id").
		Where("users.role = ?", models.Student)
	if start != nil && end != nil {
		activeTx = activeTx.Where("login_metrics.last_login >= ? AND login_metrics.last_login < ?", *start, *end)
	}
	if err := activeTx.Group("users.facility_id").Find(&active).Error; err != nil {
		return nil, newGetRecordsDBError(err, "users")
	}

	logins := make([]facilityCount, 0, len(facilities))
	loginsTx := db.WithContext(args.Ctx).Model(&models.LoginActivity{}).
		Select("facility_id, coalesce(sum(total_logins), 0) as count")
	if start != nil && end != nil {
		loginsTx = loginsTx.Where("time_interval >= ? AND time_interval < ?", *start, *end)
	}
	if err := loginsTx.Group("facility_id").Find(&logins).Error; err != nil {
		return nil, newGetRecordsDBError(err, "login_activity")
	}

	registeredByFacility := toMap(registered)
	activeByFacility := toMap(active)
	loginsByFacility := toMap(logins)

	comparison := make([]models.FacilityEngagement, 0, len(facilities))
	for i := range facilities {
		facilityID := facilities[i].ID
		comparison = append(comparison, models.FacilityEngagement{
			FacilityID:   facilityID,
			FacilityName: facilities[i].Name,
			Registered:   registeredByFacility[facilityID],
			Active:       activeByFacility[facilityID],
			Logins:       loginsByFacility[facilityID],
		})
	}
	return comparison, nil
}

func (db *DB) GetUserSessionEngagement(userID int, days int) ([]models.SessionEngagement, error) {
	var (
		sessionEngagement []models.SessionEngagement
		query             *gorm.DB
	)

	if days >= 0 {
		//specific amount of days for calculating total time spent in Unlocked
		daysAgo := time.Now().AddDate(0, 0, -days)
		query = db.Table("user_session_tracking as ust").
			Select(`ust.user_id, TO_CHAR(DATE(ust.session_start_ts), 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS time_interval,
			SUM(EXTRACT(EPOCH FROM ust.session_duration) / 3600) AS total_hours,
			SUM(EXTRACT(EPOCH FROM ust.session_duration) / 60) AS total_minutes`).
			Where("ust.user_id = ? AND ust.session_start_ts >= ?", userID, daysAgo).
			Group("ust.user_id, time_interval").
			Order("ust.user_id, time_interval")
	} else {
		//wide open query for total time spent in Unlocked
		query = db.Table("user_session_tracking as ust").
			Select(`ust.user_id,
			SUM(EXTRACT(EPOCH FROM ust.session_duration) / 3600) AS total_hours,
			SUM(EXTRACT(EPOCH FROM ust.session_duration) / 60) AS total_minutes`).
			Where("ust.user_id = ?", userID).
			Group("ust.user_id")
	}

	if err := query.Find(&sessionEngagement).Error; err != nil {
		return nil, newGetRecordsDBError(err, "session_engagement")
	}

	return sessionEngagement, nil
}

// GetUserEngagement is core's part of a resident's engagement: when they
// joined and last logged in. The handler adds Open Content's reading time.
func (db *DB) GetUserEngagement(userID int) (*models.EngagementActivityMetrics, error) {
	var engagementActivityMetrics models.EngagementActivityMetrics
	query := db.Table("users u").
		Select(`u.id as user_id,
		COALESCE(l.last_login, NULL) AS last_active_date,
		u.created_at as joined`).
		Joins("LEFT JOIN login_metrics l ON l.user_id = u.id").
		Where("u.id = ?", userID)

	if err := query.Find(&engagementActivityMetrics).Error; err != nil {
		return nil, NewDBError(err, "error getting engagement_activity")
	}
	return &engagementActivityMetrics, nil
}

func (db *DB) IncrementUserFAQClick(args *models.QueryContext, question string) error {
	faq := models.FAQ{Question: question}
	if err := db.WithContext(args.Ctx).Model(&models.FAQ{}).Where("LOWER(question) = ?", strings.ToLower(question)).FirstOrCreate(&faq).Error; err != nil {
		log.Errorf("failed to find or create FAQ: %v", err)
		return newUpdateDBError(err, "faqs")
	}
	if err := db.WithContext(args.Ctx).Exec(`INSERT INTO faq_click_metrics(user_id, faq_id, total) VALUES (?, ?, 1)
		 ON CONFLICT (user_id, faq_id) DO UPDATE SET total = faq_click_metrics.total + 1`,
		args.UserID, faq.ID).Error; err != nil {
		log.Errorf("Error incrementing faq clicks: %v", err)
		return newUpdateDBError(err, "faq_click_metrics")
	}
	return nil
}

func (db *DB) InsertUserAccountHistoryAction(ctx context.Context, accountHistory *models.UserAccountHistory) error {
	if err := db.WithContext(ctx).Create(&accountHistory).Error; err != nil {
		log.Errorf("Error inserting user account history action: %v", err)
		return newCreateDBError(err, "user_account_history")
	}
	return nil
}

func (db *DB) GetUserAccountHistory(args *models.QueryContext, userID uint, categories []string) ([]models.ActivityHistoryResponse, error) {
	history := make([]models.ActivityHistoryResponse, 0, args.PerPage)

	categoryActions := map[string][]string{
		"account":    {"account_creation", "set_password", "reset_password", "user_deactivated"},
		"facility":   {"facility_transfer"},
		"enrollment": {"progclass_history"},
		"attendance": {"marked_present", "marked_absent_excused", "marked_absent_unexcused", "attendance_recorded"},
	}

	tx := db.WithContext(args.Ctx).
		Table("user_account_history uah").
		Select(`uah.action, uah.created_at, uah.user_id,
				users.username AS user_username,
				admins.username AS admin_username,
				facilities.name AS facility_name,
				uah.attendance_status, uah.class_name, uah.session_date,
				uah.program_classes_history_id AS class_history_id`).
		Joins("INNER JOIN users ON uah.user_id = users.id").
		Joins("LEFT JOIN users admins ON uah.admin_id = admins.id").
		Joins("LEFT JOIN facilities ON uah.facility_id = facilities.id").
		Where("uah.user_id = ?", userID)

	if len(categories) > 0 {
		var actions []string
		for _, category := range categories {
			if categoryActions[category] != nil {
				actions = append(actions, categoryActions[category]...)
			}
		}
		if len(actions) > 0 {
			tx = tx.Where("uah.action IN ?", actions)
		}
	}

	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "user_account_history")
	}
	if err := tx.Order("uah.created_at desc").
		Offset(args.CalcOffset()).
		Limit(args.PerPage).
		Scan(&history).Error; err != nil {
		return nil, newGetRecordsDBError(err, "user_account_history")
	}
	return history, nil
}

func (db *DB) GetUserNotes(ctx context.Context, userID uint) ([]models.UserNoteResponse, error) {
	var notes []models.UserNoteResponse
	if err := db.WithContext(ctx).
		Table("user_notes un").
		Select("un.id, un.created_at AS date, un.note, CONCAT(u.name_first, ' ', u.name_last) AS admin").
		Joins("INNER JOIN users u ON u.id = un.create_user_id").
		Where("un.user_id = ?", userID).
		Order("un.created_at DESC").
		Scan(&notes).Error; err != nil {
		return nil, newGetRecordsDBError(err, "user_notes")
	}
	return notes, nil
}

func (db *DB) CreateUserNote(ctx context.Context, note *models.UserNote) error {
	if err := db.WithContext(ctx).Create(note).Error; err != nil {
		return newCreateDBError(err, "user_notes")
	}
	return nil
}

func adjustUserOrderBy(arg string) string {
	if strings.Contains(arg, "name_last") {
		return arg + ", name_first"
	}
	return arg
}

func adjustLastLoginOrderBy(arg string) string {
	if strings.Contains(arg, "last_login desc") {
		return strings.Replace(arg, "last_login desc", "login_metrics.last_login desc NULLS LAST", 1)
	}
	if strings.Contains(arg, "last_login asc") {
		return strings.Replace(arg, "last_login asc", "login_metrics.last_login asc NULLS FIRST", 1)
	}
	return arg
}

func adjustFacilityNameOrderBy(arg string) string {
	if strings.Contains(arg, "facility_name ") {
		arg = strings.Replace(arg, "facility_name", "facilities.name", 1)
	}
	if strings.Contains(arg, "facilities.name") {
		return arg + ", users.name_last, users.name_first"
	}
	return arg
}

func (db *DB) UpdateFailedLogin(userID uint) error {
	now := time.Now()
	return db.Transaction(func(tx *gorm.DB) error {
		var rec models.FailedLoginAttempts
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&rec, "user_id = ?", userID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			rec = models.FailedLoginAttempts{
				UserID:         userID,
				FirstAttemptAt: &now,
				AttemptCount:   1,
			}
			return tx.Create(&rec).Error
		} else if err != nil {
			return err
		}

		if now.Sub(*rec.FirstAttemptAt) > models.WindowDuration {
			rec.AttemptCount = 1
			rec.FirstAttemptAt = &now
			rec.LockedUntil = nil
		} else {
			rec.AttemptCount++
			rec.LastAttemptAt = now
			if rec.AttemptCount >= models.MaxFailures {
				lockExpiry := now.Add(models.LockDuration)
				rec.LockedUntil = &lockExpiry
			}
		}
		return tx.Save(&rec).Error
	})
}

type FailedLoginStatus struct {
	IsLocked     bool
	LockDuration time.Duration
	AttemptCount int
}

func (db *DB) IsAccountLocked(userID uint) (FailedLoginStatus, error) {
	var rec models.FailedLoginAttempts
	var status FailedLoginStatus
	now := time.Now()
	err := db.First(&rec, "user_id = ?", userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status.IsLocked = false
		status.LockDuration = 0
		status.AttemptCount = 0
		return status, nil
	} else if err != nil {
		return status, err
	}
	if now.Sub(*rec.FirstAttemptAt) >= models.WindowDuration {
		err := db.ResetFailedLoginAttempts(userID)
		if err != nil {
			return status, err
		}
	}
	if rec.LockedUntil != nil && rec.LockedUntil.After(time.Now()) {
		status.IsLocked = true
		status.LockDuration = time.Until(*rec.LockedUntil)
		status.AttemptCount = rec.AttemptCount
		return status, nil
	}
	status.IsLocked = false
	status.LockDuration = 0
	status.AttemptCount = rec.AttemptCount
	return status, nil
}

func (db *DB) ResetFailedLoginAttempts(userID uint) error {
	return db.Delete(&models.FailedLoginAttempts{}, "user_id = ?", userID).Error
}

func (db *DB) CreateUsersBulk(users []models.User, adminID uint) error {
	if len(users) == 0 {
		return newCreateDBError(errors.New("no users to create"), "users")
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.CreateInBatches(users, 100).Error; err != nil {
			return newCreateDBError(err, "users")
		}
		for _, user := range users {
			accountCreation := models.NewUserAccountHistory(
				user.ID,
				models.AccountCreation,
				&adminID,
				nil,
				nil,
			)
			if err := tx.Create(accountCreation).Error; err != nil {
				log.Errorf("Error creating account history for user %d: %v", user.ID, err)
				return newCreateDBError(err, "user_account_history")
			}
		}
		return nil
	})

	if err != nil {
		log.Errorf("CSV user creation transaction failed: %v", err)
		return err
	}

	log.Infof("Successfully created %d users with account history", len(users))
	return nil
}

func (db *DB) DeactivateUser(ctx context.Context, userID uint, adminID *uint) error {
	tx := db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return NewDBError(tx.Error, "unable to start DB transaction")
	}
	defer tx.Rollback()

	now := time.Now()
	if err := tx.Model(&models.User{}).Where("id = ?", userID).Update("deactivated_at", now).Error; err != nil {
		return newUpdateDBError(err, "users")
	}
	if err := registry.Programs(tx).WithdrawEnrollments(ctx, userID); err != nil {
		return newUpdateDBError(err, "program_class_enrollments")
	}
	history := models.NewUserAccountHistory(userID, models.UserDeactivated, adminID, nil, nil)
	if err := tx.Create(&history).Error; err != nil {
		return newCreateDBError(err, "user_account_history")
	}

	if err := tx.Commit().Error; err != nil {
		return NewDBError(err, "committing transaction after deactivating user")
	}
	return nil
}

func (db *DB) DeactivatedUsersPresent(userIDs []int) (bool, error) {
	if len(userIDs) == 0 {
		return false, nil
	}
	var count int64
	if err := db.Model(&models.User{}).Where("id IN (?) AND deactivated_at IS NOT NULL", userIDs).Count(&count).Error; err != nil {
		log.Errorf("Error checking deactivated users: %v", err)
		return false, newGetRecordsDBError(err, "users")
	}
	return count > 0, nil
}

// GetUnmappedUsers is the residents not linked to an account at a provider:
// every resident whose ID isn't in mappedUserIDs. A provider has at most tens
// of thousands of linked users, well under Postgres's 65,535-parameter limit.
func (db *DB) GetUnmappedUsers(args *models.QueryContext, mappedUserIDs []uint, userSearch []string) ([]models.User, error) {
	var users []models.User
	tx := db.Model(&models.User{}).Where("role = ?", "student")
	if len(mappedUserIDs) > 0 {
		tx = tx.Where("id NOT IN ?", mappedUserIDs)
	}
	if args.FacilityID != 0 {
		tx = tx.Where("facility_id = ?", args.FacilityID)
	}

	if len(userSearch) > 0 {
		tx = applyUserSearchConditions(tx, userSearch)
	}
	if err := tx.Count(&args.Total).Error; err != nil {
		return nil, NewDBError(err, "error counting unmapped users")
	}
	if err := tx.Offset(args.CalcOffset()).Limit(args.PerPage).Find(&users).Error; err != nil {
		return nil, NewDBError(err, "error getting unmapped users")
	}
	return users, nil
}

func applyUserSearchConditions(tx *gorm.DB, userSearch []string) *gorm.DB {
	var conditions []string
	var args []interface{}

	for _, search := range userSearch {
		search = strings.TrimSpace(search)
		if search == "" {
			continue
		}
		searchTerm := "%" + strings.ToLower(search) + "%"

		if strings.Contains(search, "@") {
			conditions = append(conditions, "LOWER(email) LIKE ?")
			args = append(args, searchTerm)
		} else if strings.Contains(search, " ") {
			splitNames := strings.Fields(search)
			for _, namePart := range splitNames {
				nameTerm := "%" + strings.ToLower(namePart) + "%"
				conditions = append(conditions, "(LOWER(name_first) LIKE ? OR LOWER(name_last) LIKE ?)")
				args = append(args, nameTerm, nameTerm)
			}
		} else {
			conditions = append(conditions, "(LOWER(name_first) LIKE ? OR LOWER(name_last) LIKE ? OR LOWER(username) LIKE ?)")
			args = append(args, searchTerm, searchTerm, searchTerm)
		}
	}

	if len(conditions) > 0 {
		combinedCondition := "(" + strings.Join(conditions, " OR ") + ")"
		tx = tx.Where(combinedCondition, args...)
	}
	return tx
}
