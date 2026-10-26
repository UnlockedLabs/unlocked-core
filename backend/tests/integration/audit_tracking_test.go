package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
)

func TestAuditFieldPopulation(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.TestServer.Close()
	defer env.Cancel()

	adminUser := models.User{
		Username:   "audit_admin",
		NameFirst:  "Audit",
		NameLast:   "Admin",
		Email:      "audit_admin@unlocked.v2",
		Role:       models.SystemAdmin,
		FacilityID: 1,
	}
	if err := env.DB.Create(&adminUser).Error; err != nil {
		t.Fatalf("Failed to create test admin user: %v", err)
	}

	programData := map[string]interface{}{
		"name":         "Test Audit Program",
		"description":  "Program for testing audit fields",
		"funding_type": "Federal_Grants",
		"is_active":    true,
		"program_types": []programs.ProgramType{
			{ProgramType: programs.Educational, ProgramID: 0},
		},
		"credit_types": []programs.ProgramCreditType{
			{CreditType: programs.Completion, ProgramID: 0},
		},
		"facilities": []uint{1},
	}

	resp := NewRequest[map[string]interface{}](env.Client, t, "POST", "/api/programs", programData).
		WithTestClaims(map[string]interface{}{
			"user_id":     adminUser.ID,
			"username":    adminUser.Username,
			"role":        adminUser.Role,
			"facility_id": adminUser.FacilityID,
		}).
		Do()

	if resp.resp.StatusCode != http.StatusCreated {
		t.Fatalf("Expected status 201, got %d", resp.resp.StatusCode)
	}

	responseData := resp.GetData()
	createdProgramID := uint(responseData["id"].(float64))

	var createdProgram programs.Program
	if err := env.DB.First(&createdProgram, createdProgramID).Error; err != nil {
		t.Fatalf("Failed to query created program: %v", err)
	}

	if models.DerefUint(createdProgram.CreateUserID) == 0 {
		t.Error("CreateUserID should be set after creation")
	}
	if models.DerefUint(createdProgram.CreateUserID) != adminUser.ID {
		t.Errorf("Expected CreateUserID %d, got %d", adminUser.ID, models.DerefUint(createdProgram.CreateUserID))
	}
	if models.DerefUint(createdProgram.UpdateUserID) != 0 {
		t.Errorf("UpdateUserID should be nil/zero on initial creation, got %d", models.DerefUint(createdProgram.UpdateUserID))
	}

	updateData := map[string]interface{}{
		"name":        "Updated Audit Program",
		"description": "Updated program description",
	}

	updateResp := NewRequest[map[string]interface{}](env.Client, t, "PATCH", fmt.Sprintf("/api/programs/%d", createdProgramID), updateData).
		WithTestClaims(map[string]interface{}{
			"user_id":     adminUser.ID,
			"username":    adminUser.Username,
			"role":        adminUser.Role,
			"facility_id": adminUser.FacilityID,
		}).
		Do()

	if updateResp.resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", updateResp.resp.StatusCode)
	}

	var updatedProgram programs.Program
	if err := env.DB.First(&updatedProgram, createdProgramID).Error; err != nil {
		t.Fatalf("Failed to query updated program: %v", err)
	}

	if models.DerefUint(updatedProgram.CreateUserID) != adminUser.ID {
		t.Errorf("CreateUserID should not change on update. Expected %d, got %d", adminUser.ID, models.DerefUint(updatedProgram.CreateUserID))
	}
	if models.DerefUint(updatedProgram.UpdateUserID) != adminUser.ID {
		t.Errorf("UpdateUserID should be set to current user. Expected %d, got %d", adminUser.ID, models.DerefUint(updatedProgram.UpdateUserID))
	}

	if updatedProgram.Name != "Updated Audit Program" {
		t.Errorf("Expected program name to be updated, got: %s", updatedProgram.Name)
	}
	if updatedProgram.Description != "Updated program description" {
		t.Errorf("Expected program description to be updated, got: %s", updatedProgram.Description)
	}
}

func TestAuditFieldsWithDifferentUsers(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.TestServer.Close()
	defer env.Cancel()

	firstUser := models.User{
		Username:   "first_user",
		NameFirst:  "First",
		NameLast:   "User",
		Email:      "first_user@unlocked.v2",
		Role:       models.DepartmentAdmin,
		FacilityID: 1,
	}
	if err := env.DB.Create(&firstUser).Error; err != nil {
		t.Fatalf("Failed to create first user: %v", err)
	}

	secondUser := models.User{
		Username:   "second_user",
		NameFirst:  "Second",
		NameLast:   "User",
		Email:      "second_user@unlocked.v2",
		Role:       models.DepartmentAdmin,
		FacilityID: 1,
	}
	if err := env.DB.Create(&secondUser).Error; err != nil {
		t.Fatalf("Failed to create second user: %v", err)
	}

	programData := map[string]interface{}{
		"name":         "Multi User Test Program",
		"description":  "Testing audit with multiple users",
		"funding_type": "State_Grants",
		"is_active":    true,
		"program_types": []programs.ProgramType{
			{ProgramType: programs.Vocational, ProgramID: 0},
		},
		"facilities": []uint{1},
	}

	createResp := NewRequest[map[string]interface{}](env.Client, t, "POST", "/api/programs", programData).
		WithTestClaims(map[string]interface{}{
			"user_id":     firstUser.ID,
			"username":    firstUser.Username,
			"role":        firstUser.Role,
			"facility_id": firstUser.FacilityID,
		}).
		Do()

	if createResp.resp.StatusCode != http.StatusCreated {
		t.Fatalf("Failed to create program with first user, status: %d", createResp.resp.StatusCode)
	}

	createdProgramData := createResp.GetData()
	createdProgramID := uint(createdProgramData["id"].(float64))

	var createdProgram programs.Program
	if err := env.DB.First(&createdProgram, createdProgramID).Error; err != nil {
		t.Fatalf("Failed to query created program: %v", err)
	}

	if models.DerefUint(createdProgram.CreateUserID) != firstUser.ID {
		t.Errorf("Expected CreateUserID %d (first user), got %d", firstUser.ID, createdProgram.CreateUserID)
	}
	if models.DerefUint(createdProgram.UpdateUserID) != 0 {
		t.Errorf("Expected UpdateUserID to be nil/zero on create, got %d", createdProgram.UpdateUserID)
	}

	updateData := map[string]interface{}{
		"name": "Updated by Second User",
	}

	updateResp := NewRequest[map[string]interface{}](env.Client, t, "PATCH", fmt.Sprintf("/api/programs/%d", createdProgram.ID), updateData).
		WithTestClaims(map[string]interface{}{
			"user_id":     secondUser.ID,
			"username":    secondUser.Username,
			"role":        secondUser.Role,
			"facility_id": secondUser.FacilityID,
		}).
		Do()

	if updateResp.resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to update program with second user, status: %d", updateResp.resp.StatusCode)
	}

	updatedProgramData := updateResp.GetData()
	updatedProgramID := uint(updatedProgramData["id"].(float64))

	var updatedProgram programs.Program
	if err := env.DB.First(&updatedProgram, updatedProgramID).Error; err != nil {
		t.Fatalf("Failed to query updated program: %v", err)
	}

	if models.DerefUint(updatedProgram.CreateUserID) != firstUser.ID {
		t.Errorf("CreateUserID should remain unchanged. Expected %d, got %d", firstUser.ID, updatedProgram.CreateUserID)
	}
	if models.DerefUint(updatedProgram.UpdateUserID) != secondUser.ID {
		t.Errorf("UpdateUserID should be updated to second user. Expected %d, got %d", secondUser.ID, updatedProgram.UpdateUserID)
	}
}

func TestSystemBatchUserBackfill(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.TestServer.Close()
	defer env.Cancel()

	if env.DB.Name() == "sqlite" {
		t.Skip("sqlite tests do not run migrations/backfill for system_batch")
	}

	facility := models.Facility{
		Name:     "Test Facility",
		Timezone: "America/Chicago",
	}
	if err := env.DB.Create(&facility).Error; err != nil {
		t.Fatalf("Failed to create facility: %v", err)
	}

	var queriedFacility models.Facility
	if err := env.DB.First(&queriedFacility, facility.ID).Error; err != nil {
		t.Fatalf("Failed to query facility: %v", err)
	}

	if models.DerefUint(queriedFacility.CreateUserID) == 0 {
		t.Error("CreateUserID should be set by backfill/migration")
	}

	var systemBatchUser models.User
	if err := env.DB.Where("username = ?", "system_batch").First(&systemBatchUser).Error; err != nil {
		t.Fatalf("System batch user should exist: %v", err)
	}

	if models.DerefUint(queriedFacility.CreateUserID) != systemBatchUser.ID {
		t.Errorf("Expected CreateUserID to be system batch user (%d), got %d", systemBatchUser.ID, queriedFacility.CreateUserID)
	}

	if models.DerefUint(queriedFacility.UpdateUserID) != 0 {
		t.Errorf("UpdateUserID should be NULL for backfilled records, got %d", queriedFacility.UpdateUserID)
	}
}

func TestAuditFieldConstraints(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.TestServer.Close()
	defer env.Cancel()

	if env.DB.Name() == "sqlite" {
		t.Skip("sqlite tests do not enforce foreign key constraints in this setup")
	}

	invalidUserProgram := programs.Program{
		Name:        "Invalid User Program",
		Description: "This should fail due to foreign key constraint",
		FundingType: programs.Other,
		IsActive:    true,
		DatabaseFields: programs.DatabaseFields{
			CreateUserID: models.UintPtr(999999),
			UpdateUserID: models.UintPtr(999999),
		},
	}

	err := env.DB.Create(&invalidUserProgram).Error
	if err == nil {
		t.Error("Expected foreign key constraint violation when using non-existent user ID")
	}

	nullAuditProgram := programs.Program{
		Name:        "System Operation Program",
		Description: "Created by system operations",
		FundingType: programs.Other,
		IsActive:    true,
	}

	err = env.DB.Create(&nullAuditProgram).Error
	if err != nil {
		t.Errorf("Should allow creation with NULL audit fields: %v", err)
	}
}

func TestAuditFieldsPerformance(t *testing.T) {
	env := SetupTestEnv(t)
	defer env.TestServer.Close()
	defer env.Cancel()

	testUser := models.User{
		Username:   "perf_test_user",
		NameFirst:  "Performance",
		NameLast:   "Test",
		Email:      "perf_test@unlocked.v2",
		Role:       models.DepartmentAdmin,
		FacilityID: 1,
	}
	if err := env.DB.Create(&testUser).Error; err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}
	// Programs' models take the audit user from the caller's claims, as its
	// routes have them.
	env.Context = identity.NewContext(env.Context, &identity.Claims{UserID: testUser.ID})

	for i := 0; i < 10; i++ {
		program := programs.Program{
			Name:        fmt.Sprintf("Performance Test Program %d", i),
			Description: fmt.Sprintf("Testing performance of audit fields %d", i),
			FundingType: programs.Other,
			IsActive:    true,
		}

		err := env.DB.WithContext(env.Context).Model(&program).Create(&program).Error
		if err != nil {
			t.Fatalf("Failed to create program %d: %v", i, err)
		}

		if models.DerefUint(program.CreateUserID) != testUser.ID {
			t.Errorf("Program %d: Expected CreateUserID %d, got %d", i, testUser.ID, program.CreateUserID)
		}
		if models.DerefUint(program.UpdateUserID) != 0 {
			t.Errorf("Program %d: Expected UpdateUserID to be nil/zero on create, got %d", i, program.UpdateUserID)
		}
	}

	var created []programs.Program
	start := time.Now()
	err := env.DB.Where("create_user_id = ?", testUser.ID).Find(&created).Error
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Failed to query programs by audit field: %v", err)
	}

	if len(created) != 10 {
		t.Errorf("Expected 10 programs, got %d", len(created))
	}

	if duration > 100*time.Millisecond {
		t.Logf("Warning: Query took %v, consider checking audit field indexes", duration)
	}
}
