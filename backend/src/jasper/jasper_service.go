package jasper

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src"
	"github.com/UnlockedLabs/unlocked-core/backend/src/database"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	ocapi "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	programsapi "github.com/UnlockedLabs/unlocked-contracts/programs"
	"github.com/evertonvps/go-jasper"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

type jasperService struct {
	ctx              context.Context
	db               *database.DB
	openContent      ocapi.OpenContent
	programs         programsapi.Programs
	hasProgramAccess bool
}

func newJasperServiceWithContext(ctx context.Context, db *database.DB, openContent ocapi.OpenContent, programs programsapi.Programs, hasProgramAccess bool) *jasperService {
	return &jasperService{
		ctx:              ctx,
		db:               db,
		openContent:      openContent,
		programs:         programs,
		hasProgramAccess: hasProgramAccess,
	}
}

func (js *jasperService) generateUsageReportPDF(userID int) ([]byte, error) {
	user, err := js.db.GetUserByID(uint(userID))
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	userPrograms, err := js.programs.ResidentPrograms(js.ctx, uint(userID))
	if err != nil {
		return nil, fmt.Errorf("failed to get user programs: %w", err)
	}

	sessionEngagements, err := js.db.GetUserSessionEngagement(userID, -1)
	if err != nil {
		return nil, fmt.Errorf("failed to get user engagements: %w", err)
	}

	resourcesAccessed, err := js.openContent.ResourcesAccessed(js.ctx, uint(userID))
	if err != nil {
		return nil, fmt.Errorf("failed to get resource count: %w", err)
	}

	type JasperReportData struct {
		Programs []programsapi.ResidentProgramClassInfo `json:"programs"`
	}
	reportData := JasperReportData{
		Programs: userPrograms,
	}
	jsonData, err := json.Marshal(reportData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal report data: %w", err)
	}

	facilityName := "Unknown"
	if user.Facility != nil {
		facilityName = user.Facility.Name
	}

	params := []jasper.Parameter{
		{Key: "ResidentName", Value: fmt.Sprintf("%s %s", user.NameFirst, user.NameLast)},
		{Key: "ResidentID", Value: models.FormatNullableString(user.DocID)},
		{Key: "FacilityName", Value: facilityName},
		{Key: "GeneratedDate", Value: time.Now().Format("January 2, 2006")},
		{Key: "DateRange", Value: fmt.Sprintf("%s - present", user.CreatedAt.Format("January 2, 2006"))},
		{Key: "TotalTimeSpent", Value: js.calculateTotalTime(sessionEngagements)},
		{Key: "TotalLogins", Value: js.calculateTotalLogins(user)},
		{Key: "TotalResourcesAccessed", Value: fmt.Sprintf("%d", resourcesAccessed)},
		{Key: "HasProgramAccess", Value: fmt.Sprintf("%t", js.hasProgramAccess)},
		{Key: "LogoImage", Value: base64.StdEncoding.EncodeToString(src.UnlockedLogoImg)},
		{Key: "ProgramsCount", Value: fmt.Sprintf("%d", len(userPrograms))},
	}

	tempDir := os.Getenv("JASPER_TEMP_DIR")
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "jasper-reports")
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create temp directory: %w", err)
		}
		logrus.WithField("temp_dir", tempDir).Info("JASPER_TEMP_DIR not set, using fallback directory")
	}
	outputFileName := fmt.Sprintf("user_usage_report_%s", uuid.New().String())
	outputFilePath := filepath.Join(tempDir, outputFileName)
	jsonFileName := fmt.Sprintf("jasper_report_%s.json", uuid.New().String())
	jsonFilePath := filepath.Join(tempDir, jsonFileName)

	logrus.WithFields(logrus.Fields{
		"user_id":          userID,
		"output_file_path": outputFilePath,
		"json_data_length": len(jsonData),
		"json_file_path":   jsonFilePath,
	}).Info("Creating temporary JSON file for Jasper Reports")

	if err := os.WriteFile(jsonFilePath, jsonData, 0600); err != nil {
		return nil, fmt.Errorf("failed to write temporary data file: %w", err)
	}

	defer func() {
		if err := os.Remove(jsonFilePath); err != nil {
			logrus.WithFields(logrus.Fields{
				"json_file": jsonFilePath,
				"error":     err,
			}).Warn("Failed to remove temporary JSON file")
		}

		if err := os.Remove(outputFilePath + ".pdf"); err != nil {
			logrus.WithFields(logrus.Fields{
				"pdf_file": outputFilePath + ".pdf",
				"error":    err,
			}).Warn("Failed to remove temporary PDF file")
		}
	}()

	gj := jasper.NewGoJasperJsonData(jsonFilePath, "", params, "pdf", outputFilePath)
	gj.Output = outputFilePath

	if path, err := exec.LookPath("jasperstarter"); err == nil {
		gj.Executable = path
	} else {
		logrus.Info("jasperstarter not found in PATH, falling back to default path")
		gj.Executable = "/opt/jasperstarter/bin/jasperstarter"
	}

	templateDir := os.Getenv("JASPER_TEMPLATE_DIR")
	if templateDir == "" {
		templateDir = "/templates"
	}
	compiledTemplatePath := filepath.Join(templateDir, "user_usage_report.jasper")
	//TODO keep this here for now, we will figure out how to compile reports during build time
	// template := "/app/backend/src/templates/user_usage_report.jrxml"
	// if err := gj.Compile(template); err != nil {
	// }

	pdfBytes, err := gj.Process(compiledTemplatePath)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			"template_path": compiledTemplatePath,
			"error":         err,
		}).Error("Failed to process compiled template")

		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"user_id":    userID,
		"pdf_length": len(pdfBytes),
	}).Info("Successfully generated Jasper PDF report")

	return pdfBytes, nil
}

func (js *jasperService) calculateTotalTime(engagements []models.SessionEngagement) string {
	if len(engagements) == 0 {
		return "none"
	}
	return fmt.Sprintf("%.0f minutes", engagements[0].TotalMinutes)
}

func (js *jasperService) calculateTotalLogins(user *models.User) string {
	if user.LoginMetrics == nil {
		return "0"
	}
	return fmt.Sprintf("%d", user.LoginMetrics.Total)
}

func GenerateUsageReportPDF(ctx context.Context, db *database.DB, openContent ocapi.OpenContent, programs programsapi.Programs, hasProgramAccess bool, userID int) ([]byte, error) {
	service := newJasperServiceWithContext(ctx, db, openContent, programs, hasProgramAccess)
	return service.generateUsageReportPDF(userID)
}
