package integration

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/require"
)

type TestEnv struct {
	Server     *handlers.Server
	TestServer *httptest.Server
	DB         *database.DB
	// Programs is the Programs module's database, for tests that call its
	// queries directly.
	Programs *programs.Store
	Client   *Client
	Context  context.Context
	Cancel   context.CancelFunc
	T        *testing.T
}

func SetupTestEnv(t *testing.T) *TestEnv {
	ctx, cancel := context.WithCancel(context.Background())

	server := handlers.NewServer(true, ctx)
	if server == nil {
		t.Fatal("Failed to create test server")
	}

	//SeedTestData(server.Db)
	server.RegisterRoutes()

	ts := httptest.NewServer(server.Handler())

	client := NewClient(ts.URL)

	return &TestEnv{
		Server:     server,
		TestServer: ts,
		DB:         server.Db,
		Programs:   &programs.Store{DB: server.Db.DB},
		Client:     client,
		Context:    ctx,
		Cancel:     cancel,
		T:          t,
	}
}

func (env *TestEnv) CleanupTestEnv() {
	if env.TestServer != nil {
		env.TestServer.Close()
	}

	if env.Cancel != nil {
		env.Cancel()
	}

	if env.DB == nil || env.DB.DB == nil {
		return
	}

	sqlDb, err := env.DB.DB.DB()
	if err != nil {
		env.T.Errorf("failed to get sql database during cleanup: %v", err)
	}

	if err := sqlDb.Close(); err != nil {
		env.T.Errorf("failed to close sql database during cleanup: %v", err)
	}
}

func (env *TestEnv) WaitForServer(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", env.TestServer.Listener.Addr().String(), time.Second)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				env.T.Logf("error closing connection: %v", closeErr)
			}
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return &net.OpError{
		Op:   "dial",
		Net:  "tcp",
		Addr: nil,
		Err:  &timeoutError{},
	}
}

type timeoutError struct{}

func (e *timeoutError) Error() string   { return "timeout waiting for server to be ready" }
func (e *timeoutError) Timeout() bool   { return true }
func (e *timeoutError) Temporary() bool { return true }

func (env *TestEnv) CreateTestUser(username string, role models.UserRole, facilityId uint, residentId string) (*models.User, error) {
	user := &models.User{
		Username:   username,
		NameFirst:  "Test",
		NameLast:   "User",
		Role:       role,
		Email:      username + "@example.com",
		DocID:      models.NilIfBlank(residentId),
		FacilityID: facilityId,
	}

	if err := env.DB.CreateUser(user); err != nil {
		return nil, err
	}

	return user, nil
}

func (env *TestEnv) CreateTestInstructor(facilityID uint, usernamePrefix string) (*models.User, error) {
	// Use no separator to comply with alphanumunicode validation (no underscores or hyphens allowed)
	username := fmt.Sprintf("instructor%s", usernamePrefix)
	return env.CreateTestUser(username, models.FacilityAdmin, facilityID, "")
}

func (env *TestEnv) CreateTestFacility(name string) (*models.Facility, error) {

	facility := &models.Facility{
		Timezone: "America/Chicago",
		Name:     name,
	}

	if err := env.DB.CreateFacility(facility); err != nil {
		return nil, err
	}

	return facility, nil
}

func (env *TestEnv) CreateTestProgram(name string, fundingType programs.FundingType, programTypes []programs.ProgramType, creditTypes []programs.ProgramCreditType, isActive bool, archivedAt *time.Time) (*programs.Program, error) {
	program := &programs.Program{
		Name:               name,
		Description:        "This is a test program created for integration testing purposes.",
		FundingType:        fundingType,
		ProgramTypes:       programTypes,
		ProgramCreditTypes: creditTypes,
		IsActive:           isActive,
		ArchivedAt:         archivedAt,
	}

	if err := env.Programs.CreateProgram(program); err != nil {
		return nil, err
	}
	return program, nil
}

func (env *TestEnv) SetFacilitiesToProgram(programId uint, facilityIds []uint) error {
	if err := env.DB.Where("program_id = ?", programId).Delete(&programs.FacilitiesPrograms{}).Error; err != nil {
		return err
	}

	facilityPrograms := programs.IterMap(func(id uint) programs.FacilitiesPrograms {
		return programs.FacilitiesPrograms{
			FacilityID: id,
			ProgramID:  programId,
		}
	}, facilityIds)

	if len(facilityPrograms) > 0 {
		if err := env.DB.Model(&programs.FacilitiesPrograms{}).Create(&facilityPrograms).Error; err != nil {
			return err
		}
	}

	return nil
}

// InsertReturningID runs an INSERT and returns the new row's id. Tests use it
// to seed a feature module's tables, whose Go types core can't import.
func (env *TestEnv) InsertReturningID(t *testing.T, insert string, args ...any) uint {
	t.Helper()
	var id uint
	require.NoError(t, env.DB.Raw(insert+" RETURNING id", args...).Scan(&id).Error, insert)
	require.NotZero(t, id, insert)
	return id
}

func (env *TestEnv) TruncateTable(tableName string) error {
	if err := env.DB.DB.Exec("TRUNCATE TABLE " + tableName + " CASCADE").Error; err != nil {
		return err
	}
	return nil
}

func (env *TestEnv) CleanupDatabase() error {
	tables := []string{
		"users",
		"facilities",
		"programs",
		"program_types",
		"program_credit_types",
		"facilities_programs",
		"program_classes",
		"program_class_credit_types",
		"program_class_cohorts",
	}

	for _, table := range tables {
		if err := env.TruncateTable(table); err != nil {
			log.Printf("Failed to truncate table %s: %v", table, err)
			return err
		}
	}

	return nil
}

func (env *TestEnv) CreateTestClass(program *programs.Program, facility *models.Facility, status programs.ClassStatus, instructorID *uint) (*programs.ProgramClassCohort, error) {
	endDt := time.Now().Add(time.Hour * 24)
	creditHours := int64(2)

	class := &programs.ProgramClassCohort{
		ProgramID:    program.ID,
		FacilityID:   facility.ID,
		Capacity:     10,
		InstructorID: instructorID,
		Description:  "This is a test class created for integration testing purposes.",
		StartDt:      time.Now(),
		EndDt:        &endDt,
		Status:       status,
		CreditHours:  &creditHours,
	}

	if err := env.DB.Create(class).Error; err != nil {
		return nil, err
	}

	return class, nil
}

func (env *TestEnv) NameCohortClass(cohortID uint, name string) error {
	var classID uint
	if err := env.DB.Model(&programs.ProgramClassCohort{}).
		Select("class_id").
		Where("id = ?", cohortID).
		Scan(&classID).Error; err != nil {
		return err
	}
	return env.DB.Model(&programs.ProgramClass{}).
		Where("id = ?", classID).
		Update("name", name).Error
}

func (env *TestEnv) CreateTestEnrollment(classID, userID uint, status programs.ProgramEnrollmentStatus) (*programs.ProgramClassEnrollment, error) {
	enrollment := &programs.ProgramClassEnrollment{
		CohortID:         classID,
		UserID:           userID,
		EnrollmentStatus: status,
	}

	if err := env.DB.Create(enrollment).Error; err != nil {
		return nil, err
	}

	return enrollment, nil
}

func (env *TestEnv) GetEnrollmentTimestamps(enrollmentID uint) (enrolledAt, endedAt *time.Time, err error) {
	var enrollment programs.ProgramClassEnrollment
	if err := env.DB.First(&enrollment, "id = ?", enrollmentID).Error; err != nil {
		return nil, nil, err
	}
	return enrollment.EnrolledAt, enrollment.EnrollmentEndedAt, nil
}

func (env *TestEnv) CreateTestEvent(classID uint, rrule string, instructorID uint) (*programs.ProgramClassEvent, error) {
	if rrule == "" {
		startDate := time.Now().AddDate(0, 0, -2) // 2 days ago
		rrule = fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;COUNT=10", startDate.Format("20060102T090000Z"))
	}

	roomID, err := env.getOrCreateTestRoom(classID)
	if err != nil {
		return nil, err
	}
	event := &programs.ProgramClassEvent{
		CohortID:       classID,
		Duration:       "2h",
		RecurrenceRule: rrule,
		RoomID:         &roomID,
		InstructorID:   &instructorID,
	}

	if err := env.DB.Create(event).Error; err != nil {
		return nil, err
	}

	return event, nil
}

func (env *TestEnv) GetEventRecurrenceRule(eventID uint) (string, error) {
	var event programs.ProgramClassEvent
	if err := env.DB.First(&event, "id = ?", eventID).Error; err != nil {
		return "", err
	}
	return event.RecurrenceRule, nil
}

func (env *TestEnv) CreateTestEventWithRRule(classID uint, customRRule string, instructorID uint) (*programs.ProgramClassEvent, error) {
	roomID, err := env.getOrCreateTestRoom(classID)
	if err != nil {
		return nil, err
	}
	event := &programs.ProgramClassEvent{
		CohortID:       classID,
		Duration:       "2h",
		RecurrenceRule: customRRule,
		RoomID:         &roomID,
		InstructorID:   &instructorID,
	}
	if err := env.DB.Create(event).Error; err != nil {
		return nil, err
	}
	return event, nil
}

func (env *TestEnv) CreateTestEventOverride(eventID uint, date string, isCancelled bool, reason string) (*programs.ProgramClassEventOverride, error) {
	parsedDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, err
	}

	rrule := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;COUNT=1", parsedDate.Format("20060102T090000Z"))

	override := &programs.ProgramClassEventOverride{
		EventID:       eventID,
		Duration:      "2h",
		OverrideRrule: rrule,
		IsCancelled:   isCancelled,
		Reason:        models.NilIfBlank(reason),
	}

	if err := env.DB.Create(override).Error; err != nil {
		return nil, err
	}

	return override, nil
}

func (env *TestEnv) CreateTestAttendance(eventID, userID uint, date time.Time, status programs.Attendance, reason string) (*programs.ProgramClassEventAttendance, error) {
	attendance := &programs.ProgramClassEventAttendance{
		EventID:          eventID,
		UserID:           userID,
		Date:             date.Format("2006-01-02"),
		AttendanceStatus: status,
		Note:             models.NilIfBlank(reason),
	}

	if err := env.DB.Create(attendance).Error; err != nil {
		return nil, err
	}

	return attendance, nil
}

func (env *TestEnv) SetClassCreditHours(classID uint, creditHours int64) error {
	return env.DB.Model(&programs.ProgramClassCohort{}).Where("id = ?", classID).Update("credit_hours", creditHours).Error
}

func (env *TestEnv) CreateTestEnrollmentWithDates(classID, userID uint, status programs.ProgramEnrollmentStatus, enrolledAt time.Time, endedAt *time.Time) (*programs.ProgramClassEnrollment, error) {
	enrollment := &programs.ProgramClassEnrollment{
		CohortID:          classID,
		UserID:            userID,
		EnrollmentStatus:  status,
		EnrolledAt:        &enrolledAt,
		EnrollmentEndedAt: endedAt,
	}

	if err := env.DB.Create(enrollment).Error; err != nil {
		return nil, err
	}

	return enrollment, nil
}

func (env *TestEnv) getOrCreateInstructorForClass(classID uint) (uint, error) {
	var class programs.ProgramClassCohort
	if err := env.DB.First(&class, "id = ?", classID).Error; err != nil {
		return 0, err
	}
	var instructor models.User
	result := env.DB.Where("facility_id = ? AND role = ?", class.FacilityID, models.FacilityAdmin).
		Order("id ASC").First(&instructor)
	if result.Error == nil {
		return instructor.ID, nil
	}
	created, err := env.CreateTestInstructor(class.FacilityID, fmt.Sprintf("auto%d", time.Now().UnixNano()))
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (env *TestEnv) getOrCreateTestRoom(classID uint) (uint, error) {
	var class programs.ProgramClassCohort
	if err := env.DB.First(&class, "id = ?", classID).Error; err != nil {
		return 0, err
	}
	var room programs.Room
	result := env.DB.Where("facility_id = ? AND name = ?", class.FacilityID, "Test Room").First(&room)
	if result.Error == nil {
		return room.ID, nil
	}
	room = programs.Room{
		FacilityID: class.FacilityID,
		Name:       "Test Room",
	}
	if err := env.DB.Create(&room).Error; err != nil {
		return 0, err
	}
	return room.ID, nil
}
