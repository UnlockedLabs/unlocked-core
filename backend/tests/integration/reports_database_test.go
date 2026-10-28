package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramOutcomesReport_ClassAggregation(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility := createTestFacility(t, env, "Test Facility")
	program := createTestProgram(t, env, "Math Program", facility.ID)

	createTestClass(t, env, program, facility, "Math 101")
	createTestClass(t, env, program, facility, "Math 102")
	class3 := createTestClass(t, env, program, facility, "Math 103")

	user := createTestUser(t, env, "student", facility.ID)
	createTestEnrollment(t, env, class3.ID, user.ID, programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:      programs.ProgramOutcomesReport,
		StartDate: time.Now().AddDate(0, -2, 0),
		EndDate:   time.Now(),
	}

	rows, err := env.Programs.GenerateProgramOutcomesReport(context.Background(), req)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 3, rows[0].TotalClasses, "Should count all 3 active classes without duplication")
	assert.Equal(t, 1, rows[0].FacilitiesActive)
	assert.Equal(t, 1, rows[0].TotalEnrollments)
}

func TestProgramOutcomesReport_EnrollmentNoDuplication(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility := createTestFacility(t, env, "Test Facility")
	program := createTestProgram(t, env, "English Program", facility.ID)

	class := createTestClass(t, env, program, facility, "English 101")

	user1 := createTestUser(t, env, "student1", facility.ID)
	user2 := createTestUser(t, env, "student2", facility.ID)
	user3 := createTestUser(t, env, "student3", facility.ID)

	createTestEnrollment(t, env, class.ID, user1.ID, programs.Enrolled)
	createTestEnrollment(t, env, class.ID, user2.ID, programs.Enrolled)
	createTestEnrollment(t, env, class.ID, user3.ID, programs.EnrollmentCompleted)

	req := &programs.ReportGenerateRequest{
		Type:      programs.ProgramOutcomesReport,
		StartDate: time.Now().AddDate(0, -2, 0),
		EndDate:   time.Now(),
	}

	rows, err := env.Programs.GenerateProgramOutcomesReport(context.Background(), req)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	// All-time enrolled counts every status (3); currently enrolled counts
	// only "Enrolled" (2). Neither should be multiplied by joins.
	assert.Equal(t, 3, rows[0].TotalEnrollments)
	assert.Equal(t, 2, rows[0].ActiveEnrollments)
}

func createTestFacility(t *testing.T, env *TestEnv, name string) *models.Facility {
	facility, err := env.CreateTestFacility(name)
	require.NoError(t, err)
	return facility
}

func createTestProgram(t *testing.T, env *TestEnv, name string, facilityID uint) *programs.Program {
	program, err := env.CreateTestProgram(
		name,
		programs.FederalGrants,
		nil,
		nil,
		true,
		nil,
	)
	require.NoError(t, err)

	err = env.SetFacilitiesToProgram(program.ID, []uint{facilityID})
	require.NoError(t, err)

	programType := programs.ProgramType{
		ProgramType: programs.Educational,
		ProgramID:   program.ID,
	}
	err = env.DB.Create(&programType).Error
	require.NoError(t, err)

	creditType := programs.ProgramCreditType{
		CreditType: programs.Education,
		ProgramID:  program.ID,
	}
	err = env.DB.Create(&creditType).Error
	require.NoError(t, err)

	return program
}

func createTestClass(t *testing.T, env *TestEnv, program *programs.Program, facility *models.Facility, name string) *programs.ProgramClassCohort {
	// Create unique instructor prefix from class name (remove spaces and special chars for alphanumunicode validation)
	instructorPrefix := strings.ToLower(strings.ReplaceAll(name, " ", ""))
	// Remove any non-alphanumeric characters to comply with validation
	instructorPrefix = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, instructorPrefix)
	// Fallback if name becomes empty after sanitization
	if instructorPrefix == "" {
		instructorPrefix = fmt.Sprintf("reports%d", time.Now().UnixNano())
	}

	instructor, err := env.CreateTestInstructor(facility.ID, instructorPrefix)
	require.NoError(t, err)

	class, err := env.CreateTestClass(program, facility, programs.Active, &instructor.ID)
	require.NoError(t, err)
	// The displayed name lives on the parent CLASS now -- the cohort has none. Reports
	// select it through a join, so renaming the class is what the assertions observe.
	err = env.DB.Model(&programs.ProgramClass{}).
		Where("id = ?", class.ClassID).
		Update("name", name).Error
	require.NoError(t, err)
	class.ClassName = name
	return class
}

func createTestUser(t *testing.T, env *TestEnv, username string, facilityID uint) *models.User {
	user, err := env.CreateTestUser(username, models.Student, facilityID, username+"-123")
	require.NoError(t, err)
	return user
}

func createTestEnrollment(t *testing.T, env *TestEnv, classID, userID uint, status programs.ProgramEnrollmentStatus) *programs.ProgramClassEnrollment {
	enrolledAt := time.Now().AddDate(0, -1, 0)
	enrollment, err := env.CreateTestEnrollmentWithDates(classID, userID, status, enrolledAt, nil)
	require.NoError(t, err)
	return enrollment
}

func createTestUserWithName(t *testing.T, env *TestEnv, username, lastName, firstName string, facilityID uint, docID string) *models.User {
	user, err := env.CreateTestUser(username, models.Student, facilityID, docID)
	require.NoError(t, err)
	user.NameLast = lastName
	user.NameFirst = firstName
	err = env.DB.Save(user).Error
	require.NoError(t, err)
	return user
}

func TestGetEnrolledResidentsForClass(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility := createTestFacility(t, env, "Test Facility")
	program := createTestProgram(t, env, "Test Program", facility.ID)
	class := createTestClass(t, env, program, facility, "Test Class")

	student1 := createTestUserWithName(t, env, "student1", "Smith", "John", facility.ID, "DOC001")
	student2 := createTestUserWithName(t, env, "student2", "Jones", "Jane", facility.ID, "DOC002")
	student3 := createTestUserWithName(t, env, "student3", "Brown", "Bob", facility.ID, "DOC003")

	createTestEnrollment(t, env, class.ID, student1.ID, programs.Enrolled)
	createTestEnrollment(t, env, class.ID, student2.ID, programs.Enrolled)

	args := &models.QueryContext{
		Ctx:     context.Background(),
		PerPage: 100,
		Page:    1,
	}

	users, err := enrolledResidents(env, args, class.ID)

	require.NoError(t, err)
	require.Len(t, users, 2, "Should return only 2 enrolled students")

	userIDs := make(map[uint]bool)
	for _, u := range users {
		userIDs[u.ID] = true
		assert.Equal(t, models.Student, u.Role, "All returned users should be students")
	}

	assert.True(t, userIDs[student1.ID], "Student1 should be in results")
	assert.True(t, userIDs[student2.ID], "Student2 should be in results")
	assert.False(t, userIDs[student3.ID], "Student3 should NOT be in results (not enrolled)")
}

func TestGetEnrolledResidentsForClass_ExcludesNonEnrolledStatuses(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.CleanupTestEnv()

	facility := createTestFacility(t, env, "Test Facility")
	program := createTestProgram(t, env, "Test Program", facility.ID)
	class := createTestClass(t, env, program, facility, "Test Class")

	student1 := createTestUserWithName(t, env, "student1", "Smith", "John", facility.ID, "DOC123")
	student2 := createTestUserWithName(t, env, "student2", "Jones", "Jane", facility.ID, "DOC456")
	student3 := createTestUserWithName(t, env, "student3", "Brown", "Bob", facility.ID, "DOC789")

	createTestEnrollment(t, env, class.ID, student1.ID, programs.Enrolled)
	createTestEnrollment(t, env, class.ID, student2.ID, programs.EnrollmentCompleted)
	createTestEnrollment(t, env, class.ID, student3.ID, programs.EnrollmentIncompleteDropped)

	args := &models.QueryContext{
		Ctx:     context.Background(),
		PerPage: 100,
		Page:    1,
	}

	users, err := enrolledResidents(env, args, class.ID)

	require.NoError(t, err)
	require.Len(t, users, 1, "Should return only actively enrolled student")
	assert.Equal(t, student1.ID, users[0].ID)
}

// enrolledResidents is the users list's only_enrolled filter: Programs'
// enrolled user IDs, then core's page of those residents.
func enrolledResidents(env *TestEnv, args *models.QueryContext, classID uint) ([]models.User, error) {
	ids, err := programs.NewAPI(env.DB.DB).EnrolledUserIDs(args.Ctx, classID)
	if err != nil {
		return nil, err
	}
	return env.DB.GetResidentsByIDs(args, ids, "users.name_last asc")
}
