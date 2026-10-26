package programs

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeReports records the report it is asked to render.
type fakeReports struct {
	template string
	report   module.Report
}

func (f *fakeReports) PDF(template string, report module.Report) ([]byte, error) {
	f.template, f.report = template, report
	return []byte("%PDF-fake"), nil
}

// A PDF export hands core the report's template, table and filters, and
// answers with the PDF core renders.
func TestExportReportPDF(t *testing.T) {
	reports := &fakeReports{}
	srv := &server{store: &Store{newTestDB(t)}, reports: reports}
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	req := &ReportGenerateRequest{Type: AttendanceReport, Format: FormatPDF, StartDate: start, EndDate: end}
	report := AttendanceReportData{Data: []AttendanceReportRow{{
		FacilityName: "North", ProgramName: "GED", ClassName: "Math", Date: start,
		StudentLastName: "Doe", StudentFirstName: "Jane", DocID: "1001", AttendanceStatus: Present,
	}}}

	w := httptest.NewRecorder()
	require.NoError(t, srv.exportReport(w, report, FormatPDF, req, "North", ""))

	assert.Equal(t, "attendance_report", reports.template)
	assert.Equal(t, "Attendance Records", reports.report.Title)
	assert.Equal(t, [][]string{{"2026-03-01", "GED", "Math", "North", "Doe, Jane", "1001", "Present", ""}}, reports.report.Rows)
	assert.Equal(t, []module.ReportFilter{
		{Label: "Class", Value: "All classes"},
		{Label: "Resident", Value: "All residents"},
		{Label: "Date Range", Value: "March 1, 2026 - March 31, 2026"},
		{Label: "Facility", Value: "North"},
	}, reports.report.Filters)
	assert.Equal(t, "application/pdf", w.Header().Get("Content-Type"))
	assert.Equal(t, "%PDF-fake", w.Body.String())
}
