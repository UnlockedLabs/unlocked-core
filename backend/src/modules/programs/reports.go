package programs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/xuri/excelize/v2"
)

type ReportType string

const (
	AttendanceReport         ReportType = "attendance"
	ProgramOutcomesReport    ReportType = "program_outcomes"
	FacilityComparisonReport ReportType = "facility_comparison"
	ClassRosterReport        ReportType = "class_roster"
	ResidentProfileReport    ReportType = "resident_profile"
)

type ReportFormat string

const (
	FormatCSV   ReportFormat = "csv"
	FormatPDF   ReportFormat = "pdf"
	FormatExcel ReportFormat = "excel"
)

type ReportGenerateRequest struct {
	Type      ReportType   `json:"type" validate:"required,oneof=attendance program_outcomes facility_comparison class_roster resident_profile"`
	Format    ReportFormat `json:"format" validate:"required,oneof=csv pdf excel"`
	StartDate time.Time    `json:"start_date" validate:"required"`
	EndDate   time.Time    `json:"end_date" validate:"required,gtefield=StartDate"`

	FacilityID   *uint         `json:"facility_id"`
	FacilityIDs  []uint        `json:"facility_ids" validate:"omitempty,dive,min=1"`
	ProgramID    *uint         `json:"program_id"`
	ProgramIDs   []uint        `json:"program_ids" validate:"omitempty,dive,min=1"`
	CohortID     *uint         `json:"cohort_id"`
	UserID       *uint         `json:"user_id"`
	ClassStatus  *string       `json:"class_status"`
	ProgramTypes []ProgType    `json:"program_types"`
	FundingTypes []FundingType `json:"funding_types"`

	IncludeClassBreakdown   bool     `json:"include_class_breakdown"`
	IncludeInactive         bool     `json:"include_inactive"`
	EnrollmentStatuses      []string `json:"enrollment_statuses"`
	IncludeIncompleteReason bool     `json:"include_incomplete_reason"`
	IncludeAttendanceRate   bool     `json:"include_attendance_rate"`
	IncludeEnrollmentDates  bool     `json:"include_enrollment_dates"`
}

type AttendanceReportRow struct {
	FacilityName     string
	ProgramName      string
	ClassName        string
	Date             time.Time
	StudentLastName  string
	StudentFirstName string
	DocID            string
	AttendanceStatus Attendance
	SeatTimeMinutes  *int
	AbsenceReason    *string
	RecordedBy       *string
}

type ProgramOutcomesReportRow struct {
	ProgramName       string
	IsActive          bool
	ProgramType       string
	FacilitiesActive  int
	TotalClasses      int
	ActiveEnrollments int
	TotalEnrollments  int
	TotalCapacity     int
	Utilization       int
}

type FacilityComparisonReportRow struct {
	FacilityName       string
	TotalPrograms      int
	ActivePrograms     int
	TotalEnrollments   int
	ActiveEnrollments  int
	CompletionRate     float64
	AttendanceRate     float64
	TopProgramType     string
	TotalCreditHours   float64
	CertificatesEarned int
	LastActivityDate   *time.Time
}

type AttendanceReportData struct {
	Data []AttendanceReportRow
}

type ProgramClassBreakdownRow struct {
	ClassName         string
	Status            string
	CreditHours       *int64
	Capacity          int
	ActiveEnrollments int
	RangeEnrollments  int
}

type ProgramOutcomesReportData struct {
	Data          []ProgramOutcomesReportRow
	Classes       []ProgramClassBreakdownRow
	IncludeStatus bool
}

func programStatusLabel(isActive bool) string {
	if isActive {
		return "Active"
	}
	return "Inactive"
}

type FacilityComparisonReportData struct {
	Data []FacilityComparisonReportRow
}

func (r AttendanceReportData) Len() int {
	return len(r.Data)
}

func (r AttendanceReportData) ToCSV() ([][]string, error) {
	csvData := [][]string{
		{"Date", "Program Name", "Class Name", "Facility", "Resident Name", "DOC ID", "Attendance Status", "Note"},
	}

	for _, row := range r.Data {
		csvData = append(csvData, []string{
			row.Date.Format("2006-01-02"),
			row.ProgramName,
			row.ClassName,
			row.FacilityName,
			formatResidentName(row.StudentLastName, row.StudentFirstName),
			row.DocID,
			row.AttendanceStatus.HumanReadable(),
			FormatNullableString(row.AbsenceReason),
		})
	}

	return csvData, nil
}

func (r ProgramOutcomesReportData) Len() int {
	return len(r.Data)
}

func (r ProgramOutcomesReportData) headers() []string {
	headers := []string{"Program Name", "Program Type", "Facilities Active", "Total Classes", "Currently Enrolled", "Enrolled in Range", "Total Capacity", "Utilization %"}
	// Status is appended (never inserted) so the fixed column positions the PDF
	// template maps stay stable; the extra trailing column is hidden in the PDF
	// unless requested.
	if r.IncludeStatus {
		headers = append(headers, "Status")
	}
	return headers
}

func (r ProgramOutcomesReportData) rowValues(row ProgramOutcomesReportRow) []string {
	values := []string{
		row.ProgramName,
		humanizeProgramTypes(row.ProgramType),
		strconv.Itoa(row.FacilitiesActive),
		strconv.Itoa(row.TotalClasses),
		strconv.Itoa(row.ActiveEnrollments),
		strconv.Itoa(row.TotalEnrollments),
		strconv.Itoa(row.TotalCapacity),
		strconv.Itoa(row.Utilization),
	}
	if r.IncludeStatus {
		values = append(values, programStatusLabel(row.IsActive))
	}
	return values
}

func (r ProgramOutcomesReportData) rowCells(row ProgramOutcomesReportRow) []any {
	cells := []any{
		row.ProgramName,
		humanizeProgramTypes(row.ProgramType),
		row.FacilitiesActive,
		row.TotalClasses,
		row.ActiveEnrollments,
		row.TotalEnrollments,
		row.TotalCapacity,
		row.Utilization,
	}
	if r.IncludeStatus {
		cells = append(cells, programStatusLabel(row.IsActive))
	}
	return cells
}

func (r ProgramOutcomesReportData) ToCSV() ([][]string, error) {
	csvData := [][]string{r.headers()}
	for _, row := range r.Data {
		csvData = append(csvData, r.rowValues(row))
	}

	return csvData, nil
}

func humanizeProgramTypes(agg string) string {
	if agg == "" || agg == "N/A" {
		return agg
	}
	parts := strings.Split(agg, ",")
	for i, p := range parts {
		parts[i] = HumanReadableProgType(strings.TrimSpace(p))
	}
	return strings.Join(parts, ", ")
}

func (r FacilityComparisonReportData) Len() int {
	return len(r.Data)
}

func (r FacilityComparisonReportData) ToCSV() ([][]string, error) {
	csvData := [][]string{
		{"Facility", "Total Programs", "Active Programs", "Total Enrollments", "Active Enrollments", "Completion Rate (%)", "Attendance Rate (%)", "Top Program Type", "Total Credit Hours", "Certificates Earned", "Last Activity Date"},
	}

	for _, row := range r.Data {
		lastActivity := ""
		if row.LastActivityDate != nil {
			lastActivity = row.LastActivityDate.Format("2006-01-02")
		}

		csvData = append(csvData, []string{
			row.FacilityName,
			strconv.Itoa(row.TotalPrograms),
			strconv.Itoa(row.ActivePrograms),
			strconv.Itoa(row.TotalEnrollments),
			strconv.Itoa(row.ActiveEnrollments),
			strconv.FormatFloat(row.CompletionRate, 'f', 2, 64),
			strconv.FormatFloat(row.AttendanceRate, 'f', 2, 64),
			HumanReadableProgType(row.TopProgramType),
			strconv.FormatFloat(row.TotalCreditHours, 'f', 2, 64),
			strconv.Itoa(row.CertificatesEarned),
			lastActivity,
		})
	}

	return csvData, nil
}

//nolint:errcheck // Excel cell setting errors are unlikely and checked at sheet creation
func (r AttendanceReportData) ToExcel() (*excelize.File, error) {
	f := excelize.NewFile()
	sheetName := "Attendance Report"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Excel sheet: %w", err)
	}
	f.SetActiveSheet(index)

	headers := []string{"Date", "Program Name", "Class Name", "Facility", "Resident Name", "DOC ID", "Attendance Status", "Note"}
	for i, header := range headers {
		cell := fmt.Sprintf("%s1", excelColumnName(i))
		f.SetCellValue(sheetName, cell, header)
	}

	for i, row := range r.Data {
		rowNum := i + 2
		f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), row.Date.Format("2006-01-02"))
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), row.ProgramName)
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", rowNum), row.ClassName)
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", rowNum), row.FacilityName)
		f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), formatResidentName(row.StudentLastName, row.StudentFirstName))
		f.SetCellValue(sheetName, fmt.Sprintf("F%d", rowNum), row.DocID)
		f.SetCellValue(sheetName, fmt.Sprintf("G%d", rowNum), row.AttendanceStatus.HumanReadable())
		f.SetCellValue(sheetName, fmt.Sprintf("H%d", rowNum), FormatNullableString(row.AbsenceReason))
	}

	return f, nil
}

//nolint:errcheck // Excel cell setting errors are unlikely and checked at sheet creation
func (r ProgramOutcomesReportData) ToExcel() (*excelize.File, error) {
	f := excelize.NewFile()
	sheetName := "Program Outcomes"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Excel sheet: %w", err)
	}
	f.SetActiveSheet(index)

	for i, header := range r.headers() {
		cell := fmt.Sprintf("%s1", excelColumnName(i))
		f.SetCellValue(sheetName, cell, header)
	}

	for i, row := range r.Data {
		rowNum := i + 2
		for j, val := range r.rowCells(row) {
			f.SetCellValue(sheetName, fmt.Sprintf("%s%d", excelColumnName(j), rowNum), val)
		}
	}

	if len(r.Classes) > 0 {
		titleRow := len(r.Data) + 3 // one blank spacer row after the program rows
		f.SetCellValue(sheetName, fmt.Sprintf("A%d", titleRow), "Class Breakdown")

		classHeaders := []string{"Class", "Status", "Credit Hours", "Capacity", "Currently Enrolled", "Enrolled in Range", "Utilization %"}
		headerRow := titleRow + 1
		for i, h := range classHeaders {
			f.SetCellValue(sheetName, fmt.Sprintf("%s%d", excelColumnName(i), headerRow), h)
		}

		for k, cr := range r.subRows() {
			rowNum := headerRow + 1 + k
			for j, val := range cr {
				f.SetCellValue(sheetName, fmt.Sprintf("%s%d", excelColumnName(j), rowNum), val)
			}
		}
	}

	return f, nil
}

//nolint:errcheck // Excel cell setting errors are unlikely and checked at sheet creation
func (r FacilityComparisonReportData) ToExcel() (*excelize.File, error) {
	f := excelize.NewFile()
	sheetName := "Facility Comparison"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Excel sheet: %w", err)
	}
	f.SetActiveSheet(index)

	headers := []string{"Facility", "Total Programs", "Active Programs", "Total Enrollments", "Active Enrollments", "Completion Rate (%)", "Attendance Rate (%)", "Top Program Type", "Total Credit Hours", "Certificates Earned", "Last Activity Date"}
	for i, header := range headers {
		cell := fmt.Sprintf("%s1", excelColumnName(i))
		f.SetCellValue(sheetName, cell, header)
	}

	for i, row := range r.Data {
		rowNum := i + 2
		lastActivity := ""
		if row.LastActivityDate != nil {
			lastActivity = row.LastActivityDate.Format("2006-01-02")
		}

		f.SetCellValue(sheetName, fmt.Sprintf("A%d", rowNum), row.FacilityName)
		f.SetCellValue(sheetName, fmt.Sprintf("B%d", rowNum), row.TotalPrograms)
		f.SetCellValue(sheetName, fmt.Sprintf("C%d", rowNum), row.ActivePrograms)
		f.SetCellValue(sheetName, fmt.Sprintf("D%d", rowNum), row.TotalEnrollments)
		f.SetCellValue(sheetName, fmt.Sprintf("E%d", rowNum), row.ActiveEnrollments)
		f.SetCellValue(sheetName, fmt.Sprintf("F%d", rowNum), fmt.Sprintf("%.2f", row.CompletionRate))
		f.SetCellValue(sheetName, fmt.Sprintf("G%d", rowNum), fmt.Sprintf("%.2f", row.AttendanceRate))
		f.SetCellValue(sheetName, fmt.Sprintf("H%d", rowNum), HumanReadableProgType(row.TopProgramType))
		f.SetCellValue(sheetName, fmt.Sprintf("I%d", rowNum), fmt.Sprintf("%.2f", row.TotalCreditHours))
		f.SetCellValue(sheetName, fmt.Sprintf("J%d", rowNum), row.CertificatesEarned)
		f.SetCellValue(sheetName, fmt.Sprintf("K%d", rowNum), lastActivity)
	}

	return f, nil
}

func (r AttendanceReportData) ToPDF() (module.Report, error) {
	tableData := make([][]string, len(r.Data))
	for i, row := range r.Data {
		tableData[i] = []string{
			row.Date.Format("2006-01-02"),
			row.ProgramName,
			row.ClassName,
			row.FacilityName,
			formatResidentName(row.StudentLastName, row.StudentFirstName),
			row.DocID,
			row.AttendanceStatus.HumanReadable(),
			FormatNullableString(row.AbsenceReason),
		}
	}

	return module.Report{
		Title: "Attendance Records",
		Rows:  tableData,
	}, nil
}

func (r ProgramOutcomesReportData) ToPDF() (module.Report, error) {
	tableData := make([][]string, len(r.Data))
	for i, row := range r.Data {
		tableData[i] = r.rowValues(row)
	}

	return module.Report{
		Title:   "Program Export",
		Rows:    tableData,
		SubRows: r.subRows(),
		// Toggles the trailing Status column in the PDF template.
		Params: map[string]string{
			"ShowStatus": strconv.FormatBool(r.IncludeStatus),
		},
	}, nil
}

func (r ProgramOutcomesReportData) subRows() [][]string {
	rows := make([][]string, len(r.Classes))
	for i, c := range r.Classes {
		util := 0
		if c.Capacity > 0 {
			util = int(math.Round(100.0 * float64(c.ActiveEnrollments) / float64(c.Capacity)))
		}
		creditHours := ""
		if c.CreditHours != nil {
			creditHours = strconv.FormatInt(*c.CreditHours, 10)
		}
		rows[i] = []string{
			c.ClassName,
			c.Status,
			creditHours,
			strconv.Itoa(c.Capacity),
			strconv.Itoa(c.ActiveEnrollments),
			strconv.Itoa(c.RangeEnrollments),
			strconv.Itoa(util) + "%",
		}
	}
	return rows
}

func (r FacilityComparisonReportData) ToPDF() (module.Report, error) {
	tableData := make([][]string, len(r.Data))
	for i, row := range r.Data {
		lastActivity := ""
		if row.LastActivityDate != nil {
			lastActivity = row.LastActivityDate.Format("2006-01-02")
		}
		tableData[i] = []string{
			row.FacilityName,
			fmt.Sprintf("%d", row.TotalPrograms),
			fmt.Sprintf("%d", row.ActivePrograms),
			fmt.Sprintf("%d", row.TotalEnrollments),
			fmt.Sprintf("%d", row.ActiveEnrollments),
			fmt.Sprintf("%.1f", row.CompletionRate),
			fmt.Sprintf("%.1f", row.AttendanceRate),
			HumanReadableProgType(row.TopProgramType),
			fmt.Sprintf("%.1f", row.TotalCreditHours),
			fmt.Sprintf("%d", row.CertificatesEarned),
			lastActivity,
		}
	}

	return module.Report{
		Title: "Facility Comparison",
		Rows:  tableData,
	}, nil
}

func FormatNullableInt(val *int) string {
	if val == nil {
		return ""
	}
	return strconv.Itoa(*val)
}

func FormatNullableString(val *string) string {
	if val == nil {
		return ""
	}
	return *val
}

func excelColumnName(col int) string {
	name := ""
	for col >= 0 {
		name = string(rune('A'+(col%26))) + name
		col = col/26 - 1
	}
	return name
}

func HumanReadableProgType(s string) string {
	return ProgType(s).HumanReadable()
}

func HumanReadableFundingType(s string) string {
	return FundingType(s).HumanReadable()
}

// ---- Class Roster report ----

type ClassRosterReportRow struct {
	NameLast         string
	NameFirst        string
	DocID            string
	EnrollmentStatus string
	EnrolledAt       *time.Time
	EndedAt          *time.Time
	SessionsAttended int
	TotalSessions    int
}

type ClassRosterReportData struct {
	Data                    []ClassRosterReportRow
	IncludeIncompleteReason bool
	IncludeAttendanceRate   bool
	IncludeEnrollmentDates  bool
}

func (r ClassRosterReportData) Len() int {
	return len(r.Data)
}

func (r ClassRosterReportData) headers() []string {
	headers := []string{"Resident Name", "DOC ID", "Enrollment Status"}
	if r.IncludeIncompleteReason {
		headers = append(headers, "Incomplete Reason")
	}
	if r.IncludeAttendanceRate {
		headers = append(headers, "Avg Attendance Rate")
	}
	if r.IncludeEnrollmentDates {
		headers = append(headers, "Enrolled At", "Ended At")
	}
	return headers
}

func (r ClassRosterReportData) rowValues(row ClassRosterReportRow) []string {
	status, reason := splitEnrollmentStatus(row.EnrollmentStatus)
	values := []string{
		formatResidentName(row.NameLast, row.NameFirst),
		row.DocID,
		status,
	}
	if r.IncludeIncompleteReason {
		values = append(values, reason)
	}
	if r.IncludeAttendanceRate {
		values = append(values, formatAttendanceRate(row.SessionsAttended, row.TotalSessions))
	}
	if r.IncludeEnrollmentDates {
		values = append(values, formatReportDate(row.EnrolledAt), formatReportDate(row.EndedAt))
	}
	return values
}

func (r ClassRosterReportData) ToCSV() ([][]string, error) {
	csvData := [][]string{r.headers()}
	for _, row := range r.Data {
		csvData = append(csvData, r.rowValues(row))
	}
	return csvData, nil
}

//nolint:errcheck // Excel cell setting errors are unlikely and checked at sheet creation
func (r ClassRosterReportData) ToExcel() (*excelize.File, error) {
	f := excelize.NewFile()
	sheetName := "Class Roster"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Excel sheet: %w", err)
	}
	f.SetActiveSheet(index)

	for i, header := range r.headers() {
		f.SetCellValue(sheetName, fmt.Sprintf("%s1", excelColumnName(i)), header)
	}
	for i, row := range r.Data {
		for j, val := range r.rowValues(row) {
			f.SetCellValue(sheetName, fmt.Sprintf("%s%d", excelColumnName(j), i+2), val)
		}
	}

	return f, nil
}

func (r ClassRosterReportData) ToPDF() (module.Report, error) {
	tableData := make([][]string, len(r.Data))
	for i, row := range r.Data {
		status, reason := splitEnrollmentStatus(row.EnrollmentStatus)
		tableData[i] = []string{
			formatResidentName(row.NameLast, row.NameFirst),
			row.DocID,
			status,
			reason,
			formatAttendanceRate(row.SessionsAttended, row.TotalSessions),
			formatReportDate(row.EnrolledAt),
			formatReportDate(row.EndedAt),
		}
	}

	return module.Report{
		Title: "Class Roster",
		Rows:  tableData,
		// use these to hide/show columns in report
		Params: map[string]string{
			"ShowIncompleteReason": strconv.FormatBool(r.IncludeIncompleteReason),
			"ShowAttendanceRate":   strconv.FormatBool(r.IncludeAttendanceRate),
			"ShowEnrollmentDates":  strconv.FormatBool(r.IncludeEnrollmentDates),
		},
	}, nil
}

// ---- Resident Profile report ----

type ResidentProfileReportRow struct {
	NameLast         string
	NameFirst        string
	DocID            string
	FacilityName     string
	ProgramName      string
	ClassName        string
	EnrollmentStatus string
	EnrolledAt       *time.Time
	EndedAt          *time.Time
	SessionsAttended int
	TotalSessions    int
}

type ResidentProfileReportData struct {
	Data []ResidentProfileReportRow
}

var residentProfileHeaders = []string{
	"Resident Name", "DOC ID", "Facility", "Program Name", "Class Name",
	"Enrollment Status", "Enrolled Date", "End Date", "Sessions Attended",
	"Total Sessions", "Attendance Rate", "Completion Status",
}

func (r ResidentProfileReportData) Len() int {
	return len(r.Data)
}

func (r ResidentProfileReportData) rowValues(row ResidentProfileReportRow) []string {
	return []string{
		formatResidentName(row.NameLast, row.NameFirst),
		row.DocID,
		row.FacilityName,
		row.ProgramName,
		row.ClassName,
		enrollmentStatusDisplay(row.EnrollmentStatus),
		formatReportDate(row.EnrolledAt),
		formatReportDate(row.EndedAt),
		strconv.Itoa(row.SessionsAttended),
		strconv.Itoa(row.TotalSessions),
		formatAttendanceRate(row.SessionsAttended, row.TotalSessions),
		enrollmentCompletionStatus(row.EnrollmentStatus),
	}
}

func (r ResidentProfileReportData) ToCSV() ([][]string, error) {
	csvData := [][]string{residentProfileHeaders}
	for _, row := range r.Data {
		csvData = append(csvData, r.rowValues(row))
	}
	return csvData, nil
}

//nolint:errcheck // Excel cell setting errors are unlikely and checked at sheet creation
func (r ResidentProfileReportData) ToExcel() (*excelize.File, error) {
	f := excelize.NewFile()
	sheetName := "Resident Profile"
	index, err := f.NewSheet(sheetName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Excel sheet: %w", err)
	}
	f.SetActiveSheet(index)

	for i, header := range residentProfileHeaders {
		f.SetCellValue(sheetName, fmt.Sprintf("%s1", excelColumnName(i)), header)
	}
	for i, row := range r.Data {
		for j, val := range r.rowValues(row) {
			f.SetCellValue(sheetName, fmt.Sprintf("%s%d", excelColumnName(j), i+2), val)
		}
	}

	return f, nil
}

func (r ResidentProfileReportData) ToPDF() (module.Report, error) {
	tableData := make([][]string, len(r.Data))
	for i, row := range r.Data {
		tableData[i] = r.rowValues(row)
	}

	return module.Report{
		Title: "Resident Profile",
		Rows:  tableData,
	}, nil
}

// ---- shared enrollment/report helpers ----
func formatResidentName(last, first string) string {
	name := strings.TrimSpace(fmt.Sprintf("%s, %s", last, first))
	return strings.Trim(name, ", ")
}

func formatReportDate(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func formatAttendanceRate(attended, total int) string {
	if total <= 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", 100.0*float64(attended)/float64(total))
}

func splitEnrollmentStatus(status string) (display string, reason string) {
	if reason, ok := strings.CutPrefix(status, "Incomplete:"); ok {
		return "Incomplete", strings.TrimSpace(reason)
	}
	return status, ""
}

func enrollmentStatusDisplay(status string) string {
	if _, reason := splitEnrollmentStatus(status); reason != "" {
		return reason
	}
	return status
}

func enrollmentCompletionStatus(status string) string {
	switch {
	case status == string(EnrollmentCompleted):
		return "Completed"
	case status == string(Enrolled):
		return "In Progress"
	case status == string(EnrollmentCancelled):
		return "Cancelled"
	case strings.HasPrefix(status, "Incomplete:"):
		return "Incomplete"
	default:
		return status
	}
}
