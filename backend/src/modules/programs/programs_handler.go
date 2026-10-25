package programs

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/features"
	"github.com/UnlockedLabs/unlocked-contracts/users"
)

/*
* @Query Params:
* ?page=: page
* ?perPage=: perPage
* ?sort=: sort
* ?search=: search
* ?searchFields=: searchFields
 */
func (srv *server) handleIndexPrograms(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	programs, err := srv.store.GetPrograms(&args)
	if err != nil {
		log.add("search_query", args.Search)
		return newDatabaseServiceError(err)
	}
	return writePaginatedResponse(w, http.StatusOK, programs, args.IntoMeta())
}

func (srv *server) handleIndexProgramsFacilitiesStats(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	adminRole := claimsFrom(r).Role
	timeFilter, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil {
		timeFilter = -1
	}
	var programs ProgramsFacilitiesStats
	if adminRole == users.FacilityAdmin {
		programs, err = srv.store.GetProgramsFacilityStats(&args, timeFilter)
	} else {
		programs, err = srv.store.GetProgramsFacilitiesStats(&args, timeFilter)
	}

	if err != nil {
		return newDatabaseServiceError(err)
	}

	return writeJsonResponse(w, http.StatusOK, programs)
}

func (srv *server) handleIndexProgramsOverviewTable(w http.ResponseWriter, r *http.Request, log sLog) error {
	args := getQueryContext(r)
	claims := claimsFrom(r)
	adminRole := claims.Role
	timeFilter, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil {
		timeFilter = -1
	}
	includeArchived := r.URL.Query().Get("include_archived") == "true"
	filters := make(map[string]string, 11)
	for key, values := range r.URL.Query() {
		if strings.HasPrefix(key, "filter_") && len(values) > 0 {
			filters[strings.TrimPrefix(key, "filter_")] = values[0]
		}
	}
	programs, err := srv.store.GetProgramsOverviewTable(&args, timeFilter, includeArchived, adminRole, filters)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	for i := range programs {
		programs[i].Source = "unlocked"
	}
	if claims.HasFeature(features.ProviderPlatforms) {
		if canvasPrograms, canvasErr := srv.getCanvasProviderPrograms(args.FacilityID, adminRole); canvasErr != nil {
			log.errorf("failed to fetch canvas provider programs: %v", canvasErr)
		} else {
			canvasPrograms = filterCanvasPrograms(canvasPrograms, &args, filters)
			dbTotal := args.Total
			args.Total += int64(len(canvasPrograms))
			programs = append(programs, canvasPageSlice(canvasPrograms, dbTotal, &args)...)
		}
	}
	return writePaginatedResponse(w, http.StatusOK, programs, args.IntoMeta())
}

func (srv *server) handleShowProgram(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	if uint(id) >= CanvasProgramIDOffset {
		return srv.handleShowCanvasProgram(w, r, log, uint(id))
	}

	claims := claimsFrom(r)
	facilityID := claims.FacilityID
	args := getQueryContext(r)
	if args.Params.Get("facility_id") != "" {
		facilityID = args.FacilityID
	} else if claims.CanSwitchFacility() {
		facilityID = 0
	}

	program, err := srv.store.GetProgramByID(id)
	if err != nil {
		log.add("program_id", id)
		return newDatabaseServiceError(err)
	}

	overview, err := srv.store.FetchEnrollmentMetrics(id, facilityID)
	if err != nil {
		log.add("program_id", id)
		return newDatabaseServiceError(err)
	}

	activeClassFacilityIDs, err := srv.store.GetActiveClassFacilityIDs(r.Context(), id)
	if err != nil {
		log.add("program_id", id)
		return newDatabaseServiceError(err)
	}
	overview.Program = *program
	overview.ActiveClassFacilityIDs = activeClassFacilityIDs
	return writeJsonResponse(w, http.StatusOK, overview)
}

type ProgramForm struct {
	Name                 string              `json:"name"`
	Description          string              `json:"description"`
	FundingType          FundingType         `json:"funding_type"`
	CreditTypes          []ProgramCreditType `json:"credit_types"`
	IsActive             bool                `json:"is_active"`
	ProgramTypes         []ProgramType       `json:"program_types"`
	Facilities           []int               `json:"facilities"`
	HasProgramCompletion *bool               `json:"has_program_completion"`
}

func (srv *server) handleCreateProgram(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := claimsFrom(r)
	var program ProgramForm
	err := json.NewDecoder(r.Body).Decode(&program)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	if len(program.Facilities) == 0 {
		program.Facilities = []int{int(claims.FacilityID)}
	}

	newProg := Program{
		Name:                 program.Name,
		Description:          program.Description,
		FundingType:          program.FundingType,
		IsActive:             program.IsActive,
		ProgramTypes:         program.ProgramTypes,
		ProgramCreditTypes:   program.CreditTypes,
		HasProgramCompletion: program.HasProgramCompletion != nil && *program.HasProgramCompletion,
	}

	err = srv.WithUserContext(r).CreateProgram(&newProg)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	log.add("program_id", newProg.ID)
	facilityPrograms := IterMap(func(id int) FacilitiesPrograms {
		return FacilitiesPrograms{
			FacilityID: uint(id),
			ProgramID:  newProg.ID,
		}
	}, program.Facilities)
	if err := srv.WithUserContext(r).Model(&FacilitiesPrograms{}).Create(&facilityPrograms).Error; err != nil {
		log.info("Error creating facility program: " + err.Error())
	}
	return writeJsonResponse(w, http.StatusCreated, newProg)
}

func (srv *server) handleUpdateProgram(w http.ResponseWriter, r *http.Request, log sLog) error {
	claims := claimsFrom(r)
	var programForm ProgramForm
	err := json.NewDecoder(r.Body).Decode(&programForm)
	if err != nil {
		return newJSONReqBodyServiceError(err)
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	programID := uint(id)
	theProg := Program{
		DatabaseFields: DatabaseFields{
			ID:           programID,
			UpdateUserID: UintPtr(claims.UserID),
		},
		Name:               programForm.Name,
		Description:        programForm.Description,
		FundingType:        programForm.FundingType,
		IsActive:           programForm.IsActive,
		ProgramTypes:       programForm.ProgramTypes,
		ProgramCreditTypes: programForm.CreditTypes,
	}

	// HasProgramCompletion is passed as the raw *bool rather than resolved here: the
	// column IS in UpdateProgram's Select list, so it is always written, and an omitted
	// field would otherwise decode to false and silently clear the flag. Resolving it
	// against a row this handler read separately would reintroduce a lost update, so
	// UpdateProgram resolves it against the row it has locked.
	updated, updateErr := srv.WithUserContext(r).UpdateProgram(&theProg, programForm.Facilities, programForm.HasProgramCompletion)
	if updateErr != nil {
		return newDatabaseServiceError(updateErr)
	}
	return writeJsonResponse(w, http.StatusOK, updated)
}

func (srv *server) handleUpdateProgramStatus(w http.ResponseWriter, r *http.Request, log sLog) error {
	programUpdate := make(map[string]any)
	if err := json.NewDecoder(r.Body).Decode(&programUpdate); err != nil {
		return newJSONReqBodyServiceError(err)
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	log.add("program_id", id)
	claims := claimsFrom(r)
	programUpdate["update_user_id"] = claims.UserID
	facilities, updated, err := srv.WithUserContext(r).UpdateProgramStatus(programUpdate, uint(id))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	payload := struct {
		Updated    bool     `json:"updated"`
		Facilities []string `json:"facilities"`
		Message    string   `json:"message"`
	}{
		Updated:    updated,
		Facilities: facilities,
	}

	if updated {
		payload.Message = "Program status updated successfully"
	} else {
		payload.Message = "Unable to archive program with active or scheduled classes"
	}

	return writeJsonResponse(w, http.StatusOK, payload)
}

func (srv *server) handleGetProgramArchiveCheck(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	if uint(id) >= CanvasProgramIDOffset {
		return writeJsonResponse(w, http.StatusOK, struct {
			Facilities []string `json:"facilities"`
		}{Facilities: []string{}})
	}
	log.add("program_id", id)
	facilities, err := srv.store.GetProgramActiveClassFacilities(r.Context(), uint(id))
	if err != nil {
		return newDatabaseServiceError(err)
	}
	payload := struct {
		Facilities []string `json:"facilities"`
	}{
		Facilities: facilities,
	}
	return writeJsonResponse(w, http.StatusOK, payload)
}

func (srv *server) handleGetProgramDeleteCheck(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	if uint(id) >= CanvasProgramIDOffset {
		return writeJsonResponse(w, http.StatusOK, struct {
			CanDelete bool                   `json:"can_delete"`
			Blockers  DeleteBlockingChildren `json:"blockers"`
		}{CanDelete: false, Blockers: DeleteBlockingChildren{}})
	}
	log.add("program_id", id)
	blockers, err := srv.store.ProgramBlockingChildren(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	payload := struct {
		CanDelete bool                   `json:"can_delete"`
		Blockers  DeleteBlockingChildren `json:"blockers"`
	}{
		CanDelete: !blockers.HasAny(),
		Blockers:  blockers,
	}
	return writeJsonResponse(w, http.StatusOK, payload)
}

func (srv *server) handleDeleteProgram(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	log.add("program_id", id)

	blockers, err := srv.store.ProgramBlockingChildren(id)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if blockers.HasAny() {
		log.info("program delete blocked by child records")
		return writeDeleteConflictResponse(w, "cannot delete: program has child records", blockers)
	}

	if err = srv.store.DeleteProgram(id); err != nil {
		return newDatabaseServiceError(err)
	}
	log.info("Program deleted")
	return writeJsonResponse(w, http.StatusNoContent, "Program deleted successfully")
}

func (srv *server) handleGetProgramHistory(w http.ResponseWriter, r *http.Request, log sLog) error {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return newInvalidIdServiceError(err, "program ID")
	}
	if uint(id) >= CanvasProgramIDOffset {
		args := getQueryContext(r)
		return writePaginatedResponse(w, http.StatusOK,
			[]ChangeLogEntry{},
			newPaginationInfo(1, args.PerPage, 0))
	}
	args := getQueryContext(r)
	categories := r.URL.Query()["categories"]
	historyEvents, err := srv.store.GetChangeLogEntries(&args, "programs", id, categories)
	if err != nil {
		return err
	}
	pageMeta, createdByDetails, err := srv.getCreatedByForHistory(id, "programs", args.IntoMeta(), &args, len(historyEvents), categories)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	if createdByDetails.Action != "" {
		historyEvents = append(historyEvents, createdByDetails)
	}
	return writePaginatedResponse(w, http.StatusOK, historyEvents, pageMeta)
}

func (srv *server) getCreatedByForHistory(id int, tableName string, pageMeta paginationMeta, args *QueryContext, numOfHistoryEvents int, categories []string) (paginationMeta, ActivityHistoryResponse, error) {
	var (
		createdByDetails ActivityHistoryResponse
		err              error
	)
	if int(args.Total) == args.PerPage {
		pageMeta.LastPage++
		pageMeta.Total++
	}
	if (args.Total == 0 || (int64(args.Page) == int64(pageMeta.LastPage) && numOfHistoryEvents < args.PerPage)) && (len(categories) == 0 || slices.Contains(categories, "info")) { //add get class created by here
		switch tableName {
		case TableNamePrograms:
			createdByDetails, err = srv.store.GetProgramCreatedAtAndBy(id, args)
			if err != nil {
				return pageMeta, createdByDetails, newDatabaseServiceError(err)
			}
			prog := "program"
			createdByDetails.FieldName = &prog
		case TableNameCohort:
			createdByDetails, err = srv.store.GetClassCreatedAtAndBy(id, args)
			if err != nil {
				return pageMeta, createdByDetails, newDatabaseServiceError(err)
			}
			class := "class"
			createdByDetails.FieldName = &class
		default:
			return pageMeta, createdByDetails, newBadRequestServiceError(errors.New("table name not recognized"), fmt.Sprintf("table name %s not recognized", tableName))
		}
		createdByDetails.Action = ProgClassHistory
		//count this record only if there are history events
		if args.Total > 0 {
			pageMeta.Total++
		}
	}
	return pageMeta, createdByDetails, nil
}

func (srv *server) handleExportProgramCSV(w http.ResponseWriter, r *http.Request, log sLog) error {
	queryCtx := getQueryContext(r)
	csvData, err := srv.store.GetProgramsCSVData(&queryCtx)
	if err != nil {
		return newDatabaseServiceError(err)
	}
	toCSV, err := ProgramDataToCSVFormat(csvData)
	if err != nil {
		return newInternalServerServiceError(err, "Failed to convert program data to CSV format")
	}
	date := time.Now().Format("2006-01-02")
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"Program-Data-%s.csv\"", date))
	w.WriteHeader(http.StatusOK)
	writer := csv.NewWriter(w)

	writeErr := writer.WriteAll(toCSV)
	if writeErr != nil {
		return newInternalServerServiceError(writeErr, "Failed to write CSV data")
	}
	return nil
}

func (srv *server) handleGetProgramFilters(w http.ResponseWriter, r *http.Request, log sLog) error {
	type EnumResponse struct {
		FundingTypes []FundingType `json:"funding_types"`
		ProgramTypes []ProgType    `json:"program_types"`
		CreditTypes  []CreditType  `json:"credit_types"`
	}
	resp := EnumResponse{
		FundingTypes: AllFundingTypes,
		ProgramTypes: AllProgTypes,
		CreditTypes:  AllCreditTypes,
	}
	return writeJsonResponse(w, http.StatusOK, resp)
}
