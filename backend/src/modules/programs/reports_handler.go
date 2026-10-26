package programs

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-contracts/users"
	"github.com/sirupsen/logrus"
	"github.com/xuri/excelize/v2"
)

const (
	maxFacilitiesInList = 50
)

func (srv *server) handleGenerateReport(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := claimsFrom(r)

	var req ReportGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return newJSONReqBodyServiceError(err)
	}

	if !isValidReportType(req.Type) {
		return newBadRequestServiceError(errors.New("invalid report type"),
			"invalid report type specified")
	}

	if !isValidReportFormat(req.Format) {
		return newBadRequestServiceError(errors.New("invalid format"),
			"invalid export format specified")
	}

	if req.StartDate.After(req.EndDate) {
		return newBadRequestServiceError(errors.New("invalid date range"),
			"start_date cannot be after end_date")
	}

	if claims.Role == users.FacilityAdmin {
		facilityID := claims.FacilityID
		req.FacilityID = &facilityID
		// Facility admins are scoped to their own facility; ignore any
		// client-supplied multi-facility list so it can't widen scope.
		req.FacilityIDs = nil
	}

	if req.Type == FacilityComparisonReport {
		if !claims.CanSwitchFacility() {
			return newForbiddenServiceError(errors.New("role insufficient"),
				"facility comparison reports require DepartmentAdmin or SystemAdmin role")
		}
		if len(req.FacilityIDs) == 0 {
			return newBadRequestServiceError(errors.New("missing facility_ids"),
				"facility_ids array is required for comparison reports")
		}
		if len(req.FacilityIDs) > maxFacilitiesInList {
			return newBadRequestServiceError(errors.New("too many facilities"),
				fmt.Sprintf("cannot compare more than %d facilities", maxFacilitiesInList))
		}
	}

	if req.Type == ClassRosterReport && req.CohortID == nil {
		return newBadRequestServiceError(errors.New("missing cohort_id"),
			"cohort_id is required for class roster reports")
	}

	if req.Type == ResidentProfileReport && req.UserID == nil {
		return newBadRequestServiceError(errors.New("missing user_id"),
			"user_id is required for resident profile reports")
	}

	switch req.Type {
	case AttendanceReport:
		return srv.handleAttendanceReport(w, r, &req, claims)
	case ProgramOutcomesReport:
		return srv.handleProgramOutcomesReport(w, r, &req, claims)
	case FacilityComparisonReport:
		return srv.handleFacilityComparisonReport(w, r, &req, claims)
	case ClassRosterReport:
		return srv.handleClassRosterReport(w, r, &req, claims)
	case ResidentProfileReport:
		return srv.handleResidentProfileReport(w, r, &req, claims)
	default:
		return newBadRequestServiceError(errors.New("invalid report type"),
			"invalid report type specified")
	}
}

func (srv *server) handleAttendanceReport(w http.ResponseWriter, r *http.Request, req *ReportGenerateRequest, claims *identity.Claims) error {
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	rows, err := srv.store.GenerateAttendanceReport(ctx, req)
	if err != nil {
		srv.logReportFailure(claims.UserID, "attendance", req, startTime, 0)
		return newDatabaseServiceError(err)
	}

	report := AttendanceReportData{Data: rows}

	facilityName := ""
	if req.FacilityID != nil {
		if facility, err := srv.store.GetFacilityByID(int(*req.FacilityID)); err == nil && facility != nil {
			facilityName = facility.Name
		}
	}

	residentName := ""
	if req.UserID != nil {
		if user, err := srv.store.GetUserByID(*req.UserID); err == nil && user != nil {
			residentName = fmt.Sprintf("%s, %s", user.NameLast, user.NameFirst)
		}
	}

	if err := srv.exportReport(w, report, req.Format, req, facilityName, residentName); err != nil {
		srv.logReportFailure(claims.UserID, "attendance", req, startTime, report.Len())
		return err
	}

	srv.logReportSuccess(claims.UserID, "attendance", req, startTime, report.Len(), claims.FacilityID, 1)
	return nil
}

func (srv *server) handleProgramOutcomesReport(w http.ResponseWriter, r *http.Request, req *ReportGenerateRequest, claims *identity.Claims) error {
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	rows, err := srv.store.GenerateProgramOutcomesReport(ctx, req)
	if err != nil {
		srv.logReportFailure(claims.UserID, "program_outcomes", req, startTime, 0)
		return newDatabaseServiceError(err)
	}

	// Surface the Active/Inactive column only when inactive programs are in scope.
	report := ProgramOutcomesReportData{Data: rows, IncludeStatus: req.IncludeInactive}

	// Class-level breakdown is only meaningful for a single program at a single
	// facility (the frontend enforces this before enabling the option).
	if req.IncludeClassBreakdown {
		if programID, facilityID, ok := singleBreakdownScope(req); ok {
			classes, err := srv.store.GenerateProgramClassBreakdown(ctx, req, programID, facilityID)
			if err != nil {
				srv.logReportFailure(claims.UserID, "program_outcomes", req, startTime, 0)
				return newDatabaseServiceError(err)
			}
			report.Classes = classes
		}
	}

	facilityName := ""
	if req.FacilityID != nil {
		if facility, err := srv.store.GetFacilityByID(int(*req.FacilityID)); err == nil && facility != nil {
			facilityName = facility.Name
		}
	}

	if err := srv.exportReport(w, report, req.Format, req, facilityName, ""); err != nil {
		srv.logReportFailure(claims.UserID, "program_outcomes", req, startTime, report.Len())
		return err
	}

	srv.logReportSuccess(claims.UserID, "program_outcomes", req, startTime, report.Len(), claims.FacilityID, 1)
	return nil
}

func (srv *server) handleFacilityComparisonReport(w http.ResponseWriter, r *http.Request, req *ReportGenerateRequest, claims *identity.Claims) error {
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	rows, err := srv.store.GenerateFacilityComparisonReport(ctx, req, req.FacilityIDs)
	if err != nil {
		srv.logReportFailure(claims.UserID, "facility_comparison", req, startTime, 0)
		return newDatabaseServiceError(err)
	}

	report := FacilityComparisonReportData{Data: rows}

	if err := srv.exportReport(w, report, req.Format, req, "Multiple Facilities", ""); err != nil {
		srv.logReportFailure(claims.UserID, "facility_comparison", req, startTime, report.Len())
		return err
	}

	srv.logReportSuccess(claims.UserID, "facility_comparison", req, startTime, report.Len(), 0, len(req.FacilityIDs))
	return nil
}

func (srv *server) handleClassRosterReport(w http.ResponseWriter, r *http.Request, req *ReportGenerateRequest, claims *identity.Claims) error {
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	rows, err := srv.store.GenerateClassRosterReport(ctx, req)
	if err != nil {
		srv.logReportFailure(claims.UserID, "class_roster", req, startTime, 0)
		return newDatabaseServiceError(err)
	}

	report := ClassRosterReportData{
		Data:                    rows,
		IncludeIncompleteReason: req.IncludeIncompleteReason,
		IncludeAttendanceRate:   req.IncludeAttendanceRate,
		IncludeEnrollmentDates:  req.IncludeEnrollmentDates,
	}

	facilityName := ""
	if req.FacilityID != nil {
		if facility, err := srv.store.GetFacilityByID(int(*req.FacilityID)); err == nil && facility != nil {
			facilityName = facility.Name
		}
	}

	if err := srv.exportReport(w, report, req.Format, req, facilityName, ""); err != nil {
		srv.logReportFailure(claims.UserID, "class_roster", req, startTime, report.Len())
		return err
	}

	srv.logReportSuccess(claims.UserID, "class_roster", req, startTime, report.Len(), claims.FacilityID, 1)
	return nil
}

func (srv *server) handleResidentProfileReport(w http.ResponseWriter, r *http.Request, req *ReportGenerateRequest, claims *identity.Claims) error {
	startTime := time.Now()

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	rows, err := srv.store.GenerateResidentProfileReport(ctx, req)
	if err != nil {
		srv.logReportFailure(claims.UserID, "resident_profile", req, startTime, 0)
		return newDatabaseServiceError(err)
	}

	report := ResidentProfileReportData{Data: rows}

	residentName := ""
	if req.UserID != nil {
		if user, err := srv.store.GetUserByID(*req.UserID); err == nil && user != nil {
			residentName = fmt.Sprintf("%s, %s", user.NameLast, user.NameFirst)
		}
	}

	if err := srv.exportReport(w, report, req.Format, req, "", residentName); err != nil {
		srv.logReportFailure(claims.UserID, "resident_profile", req, startTime, report.Len())
		return err
	}

	srv.logReportSuccess(claims.UserID, "resident_profile", req, startTime, report.Len(), claims.FacilityID, 1)
	return nil
}

func (srv *server) exportReport(w http.ResponseWriter, report reportExporter, format ReportFormat, req *ReportGenerateRequest, facilityName, residentName string) error {
	switch format {
	case FormatCSV:
		csvData, err := report.ToCSV()
		if err != nil {
			return newInternalServerServiceError(err, "failed to format CSV")
		}

		pdfConfig, err := report.ToPDF()
		if err != nil {
			return newInternalServerServiceError(err, "failed to generate PDF config")
		}
		filename := fmt.Sprintf("%s-Report-%s.csv", pdfConfig.Title, time.Now().Format("2006-01-02"))
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		w.WriteHeader(http.StatusOK)

		writer := csv.NewWriter(w)
		if err := writer.WriteAll(csvData); err != nil {
			return newInternalServerServiceError(err, "failed to write CSV")
		}
		return nil

	case FormatExcel:
		f, err := report.ToExcel()
		if err != nil {
			return newInternalServerServiceError(err, "failed to create Excel file")
		}

		pdfConfig, err := report.ToPDF()
		if err != nil {
			return newInternalServerServiceError(err, "failed to generate PDF config")
		}
		filename := fmt.Sprintf("%s-Report-%s.xlsx", pdfConfig.Title, time.Now().Format("2006-01-02"))
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		w.WriteHeader(http.StatusOK)

		if err := f.Write(w); err != nil {
			return newInternalServerServiceError(err, "failed to write Excel file")
		}
		return nil

	case FormatPDF:
		config, err := report.ToPDF()
		if err != nil {
			return newInternalServerServiceError(err, "failed to generate PDF config")
		}

		config.Filters = srv.buildFilterSummary(req, facilityName, residentName)

		templateName := ""
		switch req.Type {
		case AttendanceReport:
			templateName = "attendance_report"
		case ProgramOutcomesReport:
			templateName = "program_outcomes_report"
		case FacilityComparisonReport:
			templateName = "facility_comparison_report"
		case ClassRosterReport:
			templateName = "class_roster_report"
		case ResidentProfileReport:
			templateName = "resident_profile_report"
		default:
			return newBadRequestServiceError(errors.New("unsupported report type"), "unsupported report type for PDF generation")
		}

		pdfBytes, err := srv.reports.PDF(templateName, config)

		if err != nil {
			logrus.WithError(err).Error("Failed to generate PDF with Jasper")
			return newInternalServerServiceError(err, "failed to generate PDF")
		}

		filename := fmt.Sprintf("%s-Report-%s.pdf", config.Title, time.Now().Format("2006-01-02"))
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		w.WriteHeader(http.StatusOK)

		if _, err := w.Write(pdfBytes); err != nil {
			return newInternalServerServiceError(err, "failed to write PDF")
		}
		return nil

	default:
		return newBadRequestServiceError(errors.New("invalid format"), "invalid export format")
	}
}

func summarizeFilterValues(all bool, names []string) string {
	if all || len(names) == 0 {
		return "All"
	}
	const maxChars = 110 //set for a specific font size and style--dejavu sans
	var b strings.Builder
	shown := 0
	for _, n := range names {
		piece := n
		if b.Len() > 0 {
			piece = ", " + n
		}
		if b.Len()+len(piece) > maxChars && shown > 0 {
			break
		}
		b.WriteString(piece)
		shown++
	}
	if shown < len(names) {
		fmt.Fprintf(&b, " +%d more", len(names)-shown)
	}
	return b.String()
}

func (srv *server) buildFilterSummary(req *ReportGenerateRequest, facilityName, residentName string) []module.ReportFilter {
	dateRange := fmt.Sprintf("%s - %s",
		req.StartDate.Format("January 2, 2006"),
		req.EndDate.Format("January 2, 2006"))

	if req.Type == ProgramOutcomesReport {
		facilityValue := "All"
		if facilityName != "" {
			facilityValue = facilityName
		} else if len(req.FacilityIDs) > 0 {
			names, err := srv.store.GetFacilityNamesByIDs(req.FacilityIDs)
			if err != nil {
				logrus.WithError(err).Warn("failed to resolve facility names for report filter summary")
			}
			facilityValue = summarizeFilterValues(false, names)
		}

		programValue := "All"
		if len(req.ProgramIDs) > 0 {
			names, err := srv.store.GetProgramNamesByIDs(req.ProgramIDs)
			if err != nil {
				logrus.WithError(err).Warn("failed to resolve program names for report filter summary")
			}
			programValue = summarizeFilterValues(false, names)
		}

		typeNames := make([]string, 0, len(req.ProgramTypes))
		for _, pt := range req.ProgramTypes {
			typeNames = append(typeNames, pt.HumanReadable())
		}

		return []module.ReportFilter{
			{Label: "Facilities", Value: facilityValue},
			{Label: "Programs", Value: programValue},
			{Label: "Program Types", Value: summarizeFilterValues(len(req.ProgramTypes) == 0, typeNames)},
			{Label: "Date Range", Value: dateRange},
		}
	}

	if req.Type == AttendanceReport {
		var filters []module.ReportFilter
		classValue := "All classes"
		if req.CohortID != nil {
			if class, err := srv.store.GetCohortByID(int(*req.CohortID)); err == nil && class != nil {
				classValue = class.ClassName
			}
		}
		filters = append(filters, module.ReportFilter{Label: "Class", Value: classValue})

		residentValue := "All residents"
		if residentName != "" {
			residentValue = residentName
		}
		filters = append(filters, module.ReportFilter{Label: "Resident", Value: residentValue})

		filters = append(filters, module.ReportFilter{Label: "Date Range", Value: dateRange})

		if facilityName != "" {
			filters = append(filters, module.ReportFilter{Label: "Facility", Value: facilityName})
		}
		return filters
	}

	if req.Type == ClassRosterReport {
		var filters []module.ReportFilter
		if req.CohortID != nil {
			if class, err := srv.store.GetCohortByID(int(*req.CohortID)); err == nil && class != nil {
				filters = append(filters, module.ReportFilter{Label: "Class", Value: class.ClassName})
			}
		}
		filters = append(filters, module.ReportFilter{
			Label: "Enrollment Statuses",
			Value: summarizeFilterValues(len(req.EnrollmentStatuses) == 0, req.EnrollmentStatuses),
		})
		if facilityName != "" {
			filters = append(filters, module.ReportFilter{Label: "Facility", Value: facilityName})
		}
		return filters
	}

	if req.Type == ResidentProfileReport {
		if residentName != "" {
			return []module.ReportFilter{{Label: "Resident", Value: residentName}}
		}
		return nil
	}

	var filters []module.ReportFilter

	filters = append(filters, module.ReportFilter{Label: "Date Range", Value: dateRange})

	if facilityName != "" {
		filters = append(filters, module.ReportFilter{Label: "Facility", Value: facilityName})
	}

	if residentName != "" {
		filters = append(filters, module.ReportFilter{Label: "Resident", Value: residentName})
	}

	if req.ClassStatus != nil && *req.ClassStatus != "" && *req.ClassStatus != "All" {
		filters = append(filters, module.ReportFilter{Label: "Class Status", Value: *req.ClassStatus})
	}

	if len(req.ProgramTypes) > 0 {
		var types []string
		for _, pt := range req.ProgramTypes {
			types = append(types, pt.HumanReadable())
		}
		filters = append(filters, module.ReportFilter{Label: "Program Types", Value: strings.Join(types, ", ")})
	}

	if len(req.FundingTypes) > 0 {
		var types []string
		for _, ft := range req.FundingTypes {
			types = append(types, ft.HumanReadable())
		}
		filters = append(filters, module.ReportFilter{Label: "Funding Types", Value: strings.Join(types, ", ")})
	}

	return filters
}

func singleBreakdownScope(req *ReportGenerateRequest) (programID, facilityID uint, ok bool) {
	switch {
	case req.ProgramID != nil:
		programID = *req.ProgramID
	case len(req.ProgramIDs) == 1:
		programID = req.ProgramIDs[0]
	default:
		return 0, 0, false
	}
	switch {
	case req.FacilityID != nil:
		facilityID = *req.FacilityID
	case len(req.FacilityIDs) == 1:
		facilityID = req.FacilityIDs[0]
	default:
		return 0, 0, false
	}
	return programID, facilityID, true
}

func isValidReportType(rt ReportType) bool {
	switch rt {
	case AttendanceReport, ProgramOutcomesReport, FacilityComparisonReport,
		ClassRosterReport, ResidentProfileReport:
		return true
	default:
		return false
	}
}

func isValidReportFormat(format ReportFormat) bool {
	switch format {
	case FormatCSV, FormatPDF, FormatExcel:
		return true
	default:
		return false
	}
}

type reportExporter interface {
	ToCSV() ([][]string, error)
	ToExcel() (*excelize.File, error)
	ToPDF() (module.Report, error)
	Len() int
}

func (srv *server) logReportSuccess(userID uint, reportType string, req *ReportGenerateRequest, startTime time.Time, rowCount int, facilityID uint, facilityCount int) {
	dateRangeDays := int(req.EndDate.Sub(req.StartDate).Hours() / 24)

	fields := logrus.Fields{
		"audit":              "report_generation",
		"user_id":            userID,
		"report_type":        reportType,
		"facility_id":        facilityID,
		"format":             string(req.Format),
		"success":            true,
		"row_count":          rowCount,
		"processing_time_ms": time.Since(startTime).Milliseconds(),
		"date_range_days":    dateRangeDays,
		"facility_count":     facilityCount,
		"timestamp":          time.Now(),
	}

	if reportType == "facility_comparison" && facilityCount > 1 {
		fields["data_scope"] = "multi_facility"
	} else if facilityCount == 1 {
		fields["data_scope"] = "single_facility"
	}

	logrus.WithFields(fields).Info("Report generated")
}

func (srv *server) logReportFailure(userID uint, reportType string, req *ReportGenerateRequest, startTime time.Time, rowCount int) {
	dateRangeDays := int(req.EndDate.Sub(req.StartDate).Hours() / 24)

	fields := logrus.Fields{
		"audit":              "report_generation",
		"user_id":            userID,
		"report_type":        reportType,
		"format":             string(req.Format),
		"success":            false,
		"row_count":          rowCount,
		"processing_time_ms": time.Since(startTime).Milliseconds(),
		"date_range_days":    dateRangeDays,
		"timestamp":          time.Now(),
	}

	logrus.WithFields(fields).Error("Report generation failed")
}
