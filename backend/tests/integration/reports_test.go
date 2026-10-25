package integration

import (
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"
)

func TestAttendanceReportToCSV(t *testing.T) {
	seatTime := 90
	absenceReason := "Medical appointment"
	recordedBy := "Jane Admin"

	data := []programs.AttendanceReportRow{
		{
			FacilityName:     "Test Facility",
			ProgramName:      "Math 101",
			ClassName:        "Algebra",
			Date:             time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
			StudentLastName:  "Doe",
			StudentFirstName: "John",
			DocID:            "12345",
			AttendanceStatus: programs.Present,
			SeatTimeMinutes:  &seatTime,
			RecordedBy:       &recordedBy,
			AbsenceReason:    nil,
		},
		{
			FacilityName:     "Test Facility",
			ProgramName:      "Math 101",
			ClassName:        "Algebra",
			Date:             time.Date(2024, 3, 16, 0, 0, 0, 0, time.UTC),
			StudentLastName:  "Smith",
			StudentFirstName: "Jane",
			DocID:            "67890",
			AttendanceStatus: programs.Absent_Excused,
			SeatTimeMinutes:  nil,
			RecordedBy:       nil,
			AbsenceReason:    &absenceReason,
		},
	}

	report := programs.AttendanceReportData{Data: data}
	csv, err := report.ToCSV()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(csv) != 3 {
		t.Fatalf("Expected 3 rows (header + 2 data), got %d", len(csv))
	}

	headerRow := csv[0]
	expectedHeaders := []string{"Date", "Program Name", "Class Name", "Facility", "Resident Name", "DOC ID", "Attendance Status", "Note"}
	if len(headerRow) != len(expectedHeaders) {
		t.Fatalf("Expected %d header columns, got %d", len(expectedHeaders), len(headerRow))
	}
	for i, expected := range expectedHeaders {
		if headerRow[i] != expected {
			t.Errorf("Header column %d: expected '%s', got '%s'", i, expected, headerRow[i])
		}
	}

	firstDataRow := csv[1]
	if firstDataRow[0] != "2024-03-15" {
		t.Errorf("Expected date '2024-03-15', got '%s'", firstDataRow[0])
	}
	if firstDataRow[1] != "Math 101" {
		t.Errorf("Expected program 'Math 101', got '%s'", firstDataRow[1])
	}
	if firstDataRow[2] != "Algebra" {
		t.Errorf("Expected class 'Algebra', got '%s'", firstDataRow[2])
	}
	if firstDataRow[3] != "Test Facility" {
		t.Errorf("Expected facility 'Test Facility', got '%s'", firstDataRow[3])
	}
	if firstDataRow[4] != "Doe, John" {
		t.Errorf("Expected resident 'Doe, John', got '%s'", firstDataRow[4])
	}
	if firstDataRow[5] != "12345" {
		t.Errorf("Expected DOC ID '12345', got '%s'", firstDataRow[5])
	}
	if firstDataRow[7] != "" {
		t.Errorf("Expected empty note, got '%s'", firstDataRow[7])
	}

	secondDataRow := csv[2]
	if secondDataRow[4] != "Smith, Jane" {
		t.Errorf("Expected resident 'Smith, Jane', got '%s'", secondDataRow[4])
	}
	if secondDataRow[7] != "Medical appointment" {
		t.Errorf("Expected note 'Medical appointment', got '%s'", secondDataRow[7])
	}
}

func TestProgramOutcomesReportToCSV(t *testing.T) {
	data := []programs.ProgramOutcomesReportRow{
		{
			ProgramName:       "Math Program",
			ProgramType:       "Educational",
			FacilitiesActive:  3,
			TotalClasses:      8,
			ActiveEnrollments: 60,
			TotalEnrollments:  100,
			TotalCapacity:     80,
			Utilization:       75,
		},
	}

	report := programs.ProgramOutcomesReportData{Data: data}
	csv, err := report.ToCSV()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(csv) != 2 {
		t.Fatalf("Expected 2 rows (header + 1 data), got %d", len(csv))
	}

	// Columns: Program Name, Program Type, Facilities Active, Total Classes,
	// Currently Enrolled, All-Time Enrolled, Total Capacity, Utilization %
	dataRow := csv[1]
	if dataRow[0] != "Math Program" {
		t.Errorf("Expected program 'Math Program', got '%s'", dataRow[0])
	}
	if dataRow[4] != "60" {
		t.Errorf("Expected currently enrolled '60', got '%s'", dataRow[4])
	}
	if dataRow[5] != "100" {
		t.Errorf("Expected all-time enrolled '100', got '%s'", dataRow[5])
	}
	if dataRow[7] != "75" {
		t.Errorf("Expected utilization '75', got '%s'", dataRow[7])
	}
}

func TestFacilityComparisonReportToCSV(t *testing.T) {
	lastActivity := time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC)

	data := []programs.FacilityComparisonReportRow{
		{
			FacilityName:       "Facility A",
			TotalPrograms:      15,
			ActivePrograms:     12,
			TotalEnrollments:   200,
			ActiveEnrollments:  150,
			CompletionRate:     45.5,
			AttendanceRate:     70.2,
			TopProgramType:     "Academic",
			TotalCreditHours:   1200.5,
			CertificatesEarned: 85,
			LastActivityDate:   &lastActivity,
		},
		{
			FacilityName:       "Facility B",
			TotalPrograms:      8,
			ActivePrograms:     6,
			TotalEnrollments:   120,
			ActiveEnrollments:  90,
			CompletionRate:     38.0,
			AttendanceRate:     62.5,
			TopProgramType:     "Vocational",
			TotalCreditHours:   650.0,
			CertificatesEarned: 42,
			LastActivityDate:   nil,
		},
	}

	report := programs.FacilityComparisonReportData{Data: data}
	csv, err := report.ToCSV()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if len(csv) != 3 {
		t.Fatalf("Expected 3 rows (header + 2 data), got %d", len(csv))
	}

	firstDataRow := csv[1]
	if firstDataRow[0] != "Facility A" {
		t.Errorf("Expected facility 'Facility A', got '%s'", firstDataRow[0])
	}
	if firstDataRow[10] != "2024-03-20" {
		t.Errorf("Expected last activity '2024-03-20', got '%s'", firstDataRow[10])
	}

	secondDataRow := csv[2]
	if secondDataRow[10] != "" {
		t.Errorf("Expected empty last activity date, got '%s'", secondDataRow[10])
	}
}

func TestFormatNullableInt(t *testing.T) {
	tests := []struct {
		name     string
		input    *int
		expected string
	}{
		{"nil value", nil, ""},
		{"zero value", intPtr(0), "0"},
		{"positive value", intPtr(42), "42"},
		{"negative value", intPtr(-10), "-10"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := programs.FormatNullableInt(tt.input)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestFormatNullableString(t *testing.T) {
	tests := []struct {
		name     string
		input    *string
		expected string
	}{
		{"nil value", nil, ""},
		{"empty string", strPtr(""), ""},
		{"normal string", strPtr("test"), "test"},
		{"string with spaces", strPtr("test string"), "test string"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := models.FormatNullableString(tt.input)
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}

func strPtr(s string) *string {
	return &s
}
