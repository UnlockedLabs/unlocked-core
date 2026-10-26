package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type ProgramOutcomesReportTestSuite struct {
	suite.Suite
	env *TestEnv
}

func (suite *ProgramOutcomesReportTestSuite) SetupSuite() {
	suite.env = SetupTestEnv(suite.T())
}

func (suite *ProgramOutcomesReportTestSuite) TearDownSuite() {
	suite.env.CleanupTestEnv()
}

func (suite *ProgramOutcomesReportTestSuite) SetupTest() {
	suite.cleanDatabase()
}

func (suite *ProgramOutcomesReportTestSuite) cleanDatabase() {
	suite.env.DB.Exec("DELETE FROM program_class_event_attendance")
	suite.env.DB.Exec("DELETE FROM program_class_events")
	suite.env.DB.Exec("DELETE FROM program_class_enrollments")
	suite.env.DB.Exec("DELETE FROM program_class_cohorts")
	suite.env.DB.Exec("DELETE FROM program_class_credit_types")
	suite.env.DB.Exec("DELETE FROM program_classes")
	suite.env.DB.Exec("DELETE FROM program_types")
	suite.env.DB.Exec("DELETE FROM facilities_programs")
	suite.env.DB.Exec("DELETE FROM programs")
	suite.env.DB.Exec("DELETE FROM rooms")
	suite.env.DB.Exec("DELETE FROM facilities")
	suite.env.DB.Exec("DELETE FROM users WHERE role != 'system_admin'")
}

func (suite *ProgramOutcomesReportTestSuite) TestClassStatusFilter_Active() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)

	activeClass := suite.createClass("Active Class", program.ID, facility.ID, programs.Active)
	pausedClass := suite.createClass("Paused Class", program.ID, facility.ID, programs.Paused)

	student1 := suite.createStudent("Student", "One", facility.ID)
	student2 := suite.createStudent("Student", "Two", facility.ID)

	now := time.Now()
	suite.createEnrollment(activeClass.ID, student1.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(pausedClass.ID, student2.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:        programs.ProgramOutcomesReport,
		StartDate:   now.AddDate(0, -2, 0),
		EndDate:     now,
		ClassStatus: stringPtr("Active"),
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1)
	assert.Equal(suite.T(), 1, results[0].TotalEnrollments)
	assert.Equal(suite.T(), 1, results[0].ActiveEnrollments)
}

func (suite *ProgramOutcomesReportTestSuite) TestClassStatusFilter_NotActive() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)

	activeClass := suite.createClass("Active Class", program.ID, facility.ID, programs.Active)
	completedClass := suite.createClass("Completed Class", program.ID, facility.ID, programs.Completed)
	cancelledClass := suite.createClass("Cancelled Class", program.ID, facility.ID, programs.Cancelled)

	student1 := suite.createStudent("Student", "One", facility.ID)
	student2 := suite.createStudent("Student", "Two", facility.ID)
	student3 := suite.createStudent("Student", "Three", facility.ID)

	now := time.Now()
	suite.createEnrollment(activeClass.ID, student1.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(completedClass.ID, student2.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(cancelledClass.ID, student3.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:        programs.ProgramOutcomesReport,
		StartDate:   now.AddDate(0, -2, 0),
		EndDate:     now,
		ClassStatus: stringPtr("Not Active"),
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1)
	assert.Equal(suite.T(), 2, results[0].TotalEnrollments)
}

func (suite *ProgramOutcomesReportTestSuite) TestClassStatusFilter_All() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)

	activeClass := suite.createClass("Active Class", program.ID, facility.ID, programs.Active)
	pausedClass := suite.createClass("Paused Class", program.ID, facility.ID, programs.Paused)

	student1 := suite.createStudent("Student", "One", facility.ID)
	student2 := suite.createStudent("Student", "Two", facility.ID)

	now := time.Now()
	suite.createEnrollment(activeClass.ID, student1.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(pausedClass.ID, student2.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:        programs.ProgramOutcomesReport,
		StartDate:   now.AddDate(0, -2, 0),
		EndDate:     now,
		ClassStatus: stringPtr("All"),
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1)
	assert.Equal(suite.T(), 2, results[0].TotalEnrollments)
}

func (suite *ProgramOutcomesReportTestSuite) TestClassStatusFilter_DefaultsToActive() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)

	activeClass := suite.createClass("Active Class", program.ID, facility.ID, programs.Active)
	pausedClass := suite.createClass("Paused Class", program.ID, facility.ID, programs.Paused)

	student1 := suite.createStudent("Student", "One", facility.ID)
	student2 := suite.createStudent("Student", "Two", facility.ID)

	now := time.Now()
	suite.createEnrollment(activeClass.ID, student1.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(pausedClass.ID, student2.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:        programs.ProgramOutcomesReport,
		StartDate:   now.AddDate(0, -2, 0),
		EndDate:     now,
		ClassStatus: nil,
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1)
	assert.Equal(suite.T(), 1, results[0].TotalEnrollments)
}

func (suite *ProgramOutcomesReportTestSuite) TestMultipleProgramTypes_NoDuplicateRows() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)

	suite.addProgramType(program.ID, programs.Educational)
	suite.addProgramType(program.ID, programs.Vocational)

	activeClass := suite.createClass("Active Class", program.ID, facility.ID, programs.Active)

	student1 := suite.createStudent("Student", "One", facility.ID)
	student2 := suite.createStudent("Student", "Two", facility.ID)

	now := time.Now()
	suite.createEnrollment(activeClass.ID, student1.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(activeClass.ID, student2.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:      programs.ProgramOutcomesReport,
		StartDate: now.AddDate(0, -2, 0),
		EndDate:   now,
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1, "Should return single row even with multiple program types")
	assert.Equal(suite.T(), 2, results[0].TotalEnrollments)
	assert.Contains(suite.T(), results[0].ProgramType, "Educational")
	assert.Contains(suite.T(), results[0].ProgramType, "Vocational")
}

func (suite *ProgramOutcomesReportTestSuite) TestEnrollmentDateFiltering() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)
	activeClass := suite.createClass("Active Class", program.ID, facility.ID, programs.Active)

	student1 := suite.createStudent("Student", "One", facility.ID)
	student2 := suite.createStudent("Student", "Two", facility.ID)
	student3 := suite.createStudent("Student", "Three", facility.ID)

	now := time.Now()
	reportStart := now.AddDate(0, -1, 0)
	reportEnd := now

	suite.createEnrollment(activeClass.ID, student1.ID, now.AddDate(0, -2, 0), programs.Enrolled)
	suite.createEnrollment(activeClass.ID, student2.ID, now.AddDate(0, -1, 5), programs.Enrolled)
	suite.createEnrollment(activeClass.ID, student3.ID, now.AddDate(0, 1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:      programs.ProgramOutcomesReport,
		StartDate: reportStart,
		EndDate:   reportEnd,
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1)
	// Windowed: only the enrollment whose enrolled_at falls within
	// [reportStart, reportEnd] counts. The one before the window and the
	// future one are both excluded.
	assert.Equal(suite.T(), 1, results[0].TotalEnrollments, "Only enrollments within the window should count")
}

func (suite *ProgramOutcomesReportTestSuite) TestEnrollmentStatusCounts() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)
	activeClass := suite.createClass("Active Class", program.ID, facility.ID, programs.Active)

	students := make([]*models.User, 10)
	for i := 0; i < 10; i++ {
		students[i] = suite.createStudent("Student", fmt.Sprintf("%d", i), facility.ID)
	}

	now := time.Now()

	suite.createEnrollment(activeClass.ID, students[0].ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(activeClass.ID, students[1].ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(activeClass.ID, students[2].ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(activeClass.ID, students[3].ID, now.AddDate(0, -1, 0), programs.EnrollmentCompleted)
	suite.createEnrollment(activeClass.ID, students[4].ID, now.AddDate(0, -1, 0), programs.EnrollmentCompleted)
	suite.createEnrollment(activeClass.ID, students[5].ID, now.AddDate(0, -1, 0), programs.EnrollmentIncompleteDropped)
	suite.createEnrollment(activeClass.ID, students[6].ID, now.AddDate(0, -1, 0), programs.EnrollmentIncompleteDropped)
	suite.createEnrollment(activeClass.ID, students[7].ID, now.AddDate(0, -1, 0), programs.EnrollmentIncompleteDropped)

	req := &programs.ReportGenerateRequest{
		Type:      programs.ProgramOutcomesReport,
		StartDate: now.AddDate(0, -2, 0),
		EndDate:   now,
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1)
	// All-time enrolled counts every status; currently enrolled counts only
	// the "Enrolled" status. Completed/dropped are no longer broken out as
	// separate columns in this report.
	assert.Equal(suite.T(), 8, results[0].TotalEnrollments, "All-time enrolled should include all statuses")
	assert.Equal(suite.T(), 3, results[0].ActiveEnrollments, "Currently enrolled should only include Enrolled status")
}

func (suite *ProgramOutcomesReportTestSuite) TestAttendanceDoesNotAffectEnrollmentCounts() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)
	activeClass := suite.createClass("Active Class", program.ID, facility.ID, programs.Active)

	student1 := suite.createStudent("Student", "One", facility.ID)
	student2 := suite.createStudent("Student", "Two", facility.ID)

	now := time.Now()
	suite.createEnrollment(activeClass.ID, student1.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(activeClass.ID, student2.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	event1 := suite.createEvent(activeClass.ID, now.AddDate(0, 0, -5))
	event2 := suite.createEvent(activeClass.ID, now.AddDate(0, 0, -4))
	event3 := suite.createEvent(activeClass.ID, now.AddDate(0, 0, -3))

	suite.createAttendance(event1.ID, student1.ID, programs.Present)
	suite.createAttendance(event2.ID, student1.ID, programs.Present)
	suite.createAttendance(event3.ID, student1.ID, programs.Present)
	suite.createAttendance(event1.ID, student2.ID, programs.Absent_Unexcused)

	req := &programs.ReportGenerateRequest{
		Type:      programs.ProgramOutcomesReport,
		StartDate: now.AddDate(0, -2, 0),
		EndDate:   now,
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1)
	assert.Equal(suite.T(), 2, results[0].TotalEnrollments, "Enrollment count should not be affected by attendance records")
	assert.Equal(suite.T(), 2, results[0].ActiveEnrollments)
}

func (suite *ProgramOutcomesReportTestSuite) TestMultipleClassesInSameProgram() {
	facility := suite.createFacility("Test Facility")
	program := suite.createProgram("Test Program", facility.ID, programs.StateGrants)

	class1 := suite.createClass("Class 1", program.ID, facility.ID, programs.Active)
	class2 := suite.createClass("Class 2", program.ID, facility.ID, programs.Active)

	students := make([]*models.User, 9)
	for i := 0; i < 9; i++ {
		students[i] = suite.createStudent("Student", fmt.Sprintf("%d", i), facility.ID)
	}

	now := time.Now()

	for i := 0; i < 5; i++ {
		suite.createEnrollment(class1.ID, students[i].ID, now.AddDate(0, -1, 0), programs.Enrolled)
	}
	for i := 5; i < 9; i++ {
		suite.createEnrollment(class2.ID, students[i].ID, now.AddDate(0, -1, 0), programs.Enrolled)
	}

	req := &programs.ReportGenerateRequest{
		Type:      programs.ProgramOutcomesReport,
		StartDate: now.AddDate(0, -2, 0),
		EndDate:   now,
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1)
	assert.Equal(suite.T(), 9, results[0].TotalEnrollments)
}

func (suite *ProgramOutcomesReportTestSuite) TestFacilityIDsFilter_MultipleFacilities() {
	facilityA := suite.createFacility("Facility A")
	facilityB := suite.createFacility("Facility B")
	facilityC := suite.createFacility("Facility C")

	programA := suite.createProgram("Program A", facilityA.ID, programs.StateGrants)
	programB := suite.createProgram("Program B", facilityB.ID, programs.StateGrants)
	programC := suite.createProgram("Program C", facilityC.ID, programs.StateGrants)

	classA := suite.createClass("Class A", programA.ID, facilityA.ID, programs.Active)
	classB := suite.createClass("Class B", programB.ID, facilityB.ID, programs.Active)
	classC := suite.createClass("Class C", programC.ID, facilityC.ID, programs.Active)

	studentA := suite.createStudent("A", "Student", facilityA.ID)
	studentB := suite.createStudent("B", "Student", facilityB.ID)
	studentC := suite.createStudent("C", "Student", facilityC.ID)

	now := time.Now()
	suite.createEnrollment(classA.ID, studentA.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(classB.ID, studentB.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(classC.ID, studentC.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:        programs.ProgramOutcomesReport,
		StartDate:   now.AddDate(0, -2, 0),
		EndDate:     now,
		FacilityIDs: []uint{facilityA.ID, facilityB.ID},
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 2, "Should include only the two requested facilities' programs")
	names := []string{results[0].ProgramName, results[1].ProgramName}
	assert.Contains(suite.T(), names, "Program A")
	assert.Contains(suite.T(), names, "Program B")
	assert.NotContains(suite.T(), names, "Program C")
}

func (suite *ProgramOutcomesReportTestSuite) TestFacilityIDFilter_SingleFacility() {
	facilityA := suite.createFacility("Facility A")
	facilityB := suite.createFacility("Facility B")

	programA := suite.createProgram("Program A", facilityA.ID, programs.StateGrants)
	programB := suite.createProgram("Program B", facilityB.ID, programs.StateGrants)

	classA := suite.createClass("Class A", programA.ID, facilityA.ID, programs.Active)
	classB := suite.createClass("Class B", programB.ID, facilityB.ID, programs.Active)

	studentA := suite.createStudent("A", "Student", facilityA.ID)
	studentB := suite.createStudent("B", "Student", facilityB.ID)

	now := time.Now()
	suite.createEnrollment(classA.ID, studentA.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(classB.ID, studentB.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:       programs.ProgramOutcomesReport,
		StartDate:  now.AddDate(0, -2, 0),
		EndDate:    now,
		FacilityID: &facilityA.ID,
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 1, "Single facility_id should scope to one facility")
	assert.Equal(suite.T(), "Program A", results[0].ProgramName)
}

func (suite *ProgramOutcomesReportTestSuite) TestProgramIDsFilter_MultiplePrograms() {
	facility := suite.createFacility("Test Facility")

	programA := suite.createProgram("Program A", facility.ID, programs.StateGrants)
	programB := suite.createProgram("Program B", facility.ID, programs.StateGrants)
	programC := suite.createProgram("Program C", facility.ID, programs.StateGrants)

	classA := suite.createClass("Class A", programA.ID, facility.ID, programs.Active)
	classB := suite.createClass("Class B", programB.ID, facility.ID, programs.Active)
	classC := suite.createClass("Class C", programC.ID, facility.ID, programs.Active)

	studentA := suite.createStudent("A", "Student", facility.ID)
	studentB := suite.createStudent("B", "Student", facility.ID)
	studentC := suite.createStudent("C", "Student", facility.ID)

	now := time.Now()
	suite.createEnrollment(classA.ID, studentA.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(classB.ID, studentB.ID, now.AddDate(0, -1, 0), programs.Enrolled)
	suite.createEnrollment(classC.ID, studentC.ID, now.AddDate(0, -1, 0), programs.Enrolled)

	req := &programs.ReportGenerateRequest{
		Type:       programs.ProgramOutcomesReport,
		StartDate:  now.AddDate(0, -2, 0),
		EndDate:    now,
		ProgramIDs: []uint{programA.ID, programB.ID},
	}

	results, err := suite.env.Programs.GenerateProgramOutcomesReport(suite.env.Context, req)
	assert.NoError(suite.T(), err)
	assert.Len(suite.T(), results, 2, "Should include only the two requested programs")
	names := []string{results[0].ProgramName, results[1].ProgramName}
	assert.Contains(suite.T(), names, "Program A")
	assert.Contains(suite.T(), names, "Program B")
	assert.NotContains(suite.T(), names, "Program C")
}

func (suite *ProgramOutcomesReportTestSuite) createFacility(name string) *models.Facility {
	facility := &models.Facility{Name: name}
	suite.env.DB.Create(facility)
	return facility
}

func (suite *ProgramOutcomesReportTestSuite) createProgram(name string, facilityID uint, fundingType programs.FundingType) *programs.Program {
	program := &programs.Program{
		Name:        name,
		IsActive:    true,
		FundingType: fundingType,
	}
	suite.env.DB.Create(program)

	association := &programs.FacilitiesPrograms{
		FacilityID: facilityID,
		ProgramID:  program.ID,
	}
	suite.env.DB.Create(association)

	return program
}

func (suite *ProgramOutcomesReportTestSuite) addProgramType(programID uint, progType programs.ProgType) {
	pt := &programs.ProgramType{
		ProgramID:   programID,
		ProgramType: progType,
	}
	suite.env.DB.Create(pt)
}

func (suite *ProgramOutcomesReportTestSuite) createClass(name string, programID, facilityID uint, status programs.ClassStatus) *programs.ProgramClassCohort {
	class := &programs.ProgramClassCohort{
		ProgramID:  programID,
		FacilityID: facilityID,
		Status:     status,
	}
	suite.env.DB.Create(class)
	return class
}

func (suite *ProgramOutcomesReportTestSuite) createStudent(firstName, lastName string, facilityID uint) *models.User {
	timestamp := fmt.Sprintf("%d", time.Now().UnixNano())
	student := &models.User{
		NameFirst:  firstName,
		NameLast:   lastName,
		Username:   firstName + lastName + timestamp,
		Email:      firstName + lastName + timestamp + "@test.com",
		Role:       models.Student,
		FacilityID: facilityID,
	}
	suite.env.DB.Create(student)
	return student
}

func (suite *ProgramOutcomesReportTestSuite) createEnrollment(classID, userID uint, enrolledAt time.Time, status programs.ProgramEnrollmentStatus) *programs.ProgramClassEnrollment {
	enrollment := &programs.ProgramClassEnrollment{
		CohortID:         classID,
		UserID:           userID,
		EnrolledAt:       &enrolledAt,
		EnrollmentStatus: status,
	}
	suite.env.DB.Create(enrollment)
	return enrollment
}

func (suite *ProgramOutcomesReportTestSuite) createEvent(classID uint, eventDate time.Time) *programs.ProgramClassEvent {
	rrule := fmt.Sprintf("DTSTART:%s\nRRULE:FREQ=DAILY;COUNT=1", eventDate.Format("20060102T090000Z"))
	roomID, err := suite.env.getOrCreateTestRoom(classID)
	if err != nil {
		suite.T().Fatalf("failed to create test room: %v", err)
	}
	instructorID, err := suite.env.getOrCreateInstructorForClass(classID)
	if err != nil {
		suite.T().Fatalf("failed to get instructor: %v", err)
	}
	event := &programs.ProgramClassEvent{
		CohortID:       classID,
		Duration:       "2h",
		RecurrenceRule: rrule,
		RoomID:         &roomID,
		InstructorID:   &instructorID,
	}
	suite.env.DB.Create(event)
	return event
}

func (suite *ProgramOutcomesReportTestSuite) createAttendance(eventID, userID uint, status programs.Attendance) {
	attendance := &programs.ProgramClassEventAttendance{
		EventID:          eventID,
		UserID:           userID,
		AttendanceStatus: status,
	}
	suite.env.DB.Create(attendance)
}

func stringPtr(s string) *string {
	return &s
}

func TestProgramOutcomesReportTestSuite(t *testing.T) {
	suite.Run(t, new(ProgramOutcomesReportTestSuite))
}
