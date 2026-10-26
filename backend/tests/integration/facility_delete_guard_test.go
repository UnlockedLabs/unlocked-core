package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/handlers"
	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/stretchr/testify/suite"
)

type FacilityDeleteGuardTestSuite struct {
	suite.Suite
	env *TestEnv
}

func (suite *FacilityDeleteGuardTestSuite) SetupSuite() {
	suite.env = SetupTestEnv(suite.T())
}

func (suite *FacilityDeleteGuardTestSuite) TearDownSuite() {
	suite.env.CleanupTestEnv()
}

func (suite *FacilityDeleteGuardTestSuite) SetupTest() {
	suite.env.DB.Exec("DELETE FROM program_class_enrollments")
	suite.env.DB.Exec("DELETE FROM program_classes")
	suite.env.DB.Exec("DELETE FROM facilities_programs")
	suite.env.DB.Exec("DELETE FROM programs")
	suite.env.DB.Exec("DELETE FROM rooms")
	suite.env.DB.Exec("DELETE FROM users WHERE role != 'system_admin'")
	suite.env.DB.Exec("DELETE FROM facilities")
}

// seedAdmin creates a system admin at its own facility so the admin is never an
// association of the facility under test.
func (suite *FacilityDeleteGuardTestSuite) seedAdmin() *models.User {
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	adminFac := &models.Facility{Name: "Admin Facility " + ts, Timezone: "America/New_York"}
	suite.env.DB.Create(adminFac)

	admin := &models.User{
		NameFirst: "Sys", NameLast: "Admin",
		Username: "sysadmin" + ts, Email: "sysadmin" + ts + "@t.test",
		Role: models.SystemAdmin, FacilityID: adminFac.ID,
	}
	suite.env.DB.Create(admin)
	return admin
}

// seedFacility returns an empty facility with no associated records.
func (suite *FacilityDeleteGuardTestSuite) seedFacility() *models.Facility {
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	f := &models.Facility{Name: "Target Facility " + ts, Timezone: "America/New_York"}
	suite.env.DB.Create(f)
	return f
}

func (suite *FacilityDeleteGuardTestSuite) TestDelete_EmptyFacility_Succeeds() {
	admin := suite.seedAdmin()
	facility := suite.seedFacility()

	NewRequest[any](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/facilities/%d", facility.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusNoContent)

	var live int64
	suite.env.DB.Model(&models.Facility{}).
		Where("id = ? AND deleted_at IS NULL", facility.ID).
		Count(&live)
	suite.Equal(int64(0), live, "facility should be soft-deleted")
}

func (suite *FacilityDeleteGuardTestSuite) TestDelete_BlockedByUser() {
	admin := suite.seedAdmin()
	facility := suite.seedFacility()

	// A staff user (not a resident) — total_residents would not count this, so
	// this also proves the backend guard is broader than the UI stats heuristic.
	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	suite.env.DB.Create(&models.User{
		NameFirst: "Fac", NameLast: "Admin",
		Username: "facadmin" + ts, Email: "facadmin" + ts + "@t.test",
		Role: models.FacilityAdmin, FacilityID: facility.ID,
	})

	resp := NewRequest[models.FacilityBlockingChildren](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/facilities/%d", facility.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusConflict)

	got := resp.GetData()
	suite.Equal(int64(1), got.Users, "should report 1 blocking user")

	suite.assertStillExists(facility.ID)
}

func (suite *FacilityDeleteGuardTestSuite) TestDelete_BlockedByRoom() {
	admin := suite.seedAdmin()
	facility := suite.seedFacility()

	suite.env.DB.Create(&programs.Room{FacilityID: facility.ID, Name: "101"})

	resp := NewRequest[models.FacilityBlockingChildren](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/facilities/%d", facility.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusConflict)

	got := resp.GetData()
	suite.Equal(int64(1), got.Rooms, "should report 1 blocking room")

	suite.assertStillExists(facility.ID)
}

func (suite *FacilityDeleteGuardTestSuite) TestDelete_BlockedByClass() {
	admin := suite.seedAdmin()
	facility := suite.seedFacility()

	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	program := &programs.Program{Name: "Prog " + ts, IsActive: true}
	suite.env.DB.Create(program)
	suite.env.DB.Create(&programs.FacilitiesPrograms{FacilityID: facility.ID, ProgramID: program.ID})
	// Status and Capacity are COHORT-level now -- the class tier has neither, and the
	// facility guard counts program_classes (the class tier), so a bare class is enough.
	suite.env.DB.Create(&programs.ProgramClass{
		ProgramID: program.ID, FacilityID: facility.ID,
		Name: "Class " + ts, Description: models.StringPtr("x"),
	})

	resp := NewRequest[models.FacilityBlockingChildren](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/facilities/%d", facility.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusConflict)

	got := resp.GetData()
	suite.Equal(int64(1), got.Classes, "should report 1 blocking class")
	suite.Equal(int64(1), got.Programs, "should report 1 blocking program link")

	suite.assertStillExists(facility.ID)
}

func (suite *FacilityDeleteGuardTestSuite) TestDelete_LoginActivityOnly_Succeeds() {
	admin := suite.seedAdmin()
	facility := suite.seedFacility()

	// login_activity is derivative analytics and must NOT block deletion.
	suite.env.DB.Create(&models.LoginActivity{
		TimeInterval: time.Now(),
		FacilityID:   facility.ID,
		TotalLogins:  1,
	})

	NewRequest[any](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/facilities/%d", facility.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusNoContent)

	var live int64
	suite.env.DB.Model(&models.Facility{}).
		Where("id = ? AND deleted_at IS NULL", facility.ID).
		Count(&live)
	suite.Equal(int64(0), live, "login_activity should not block deletion")
}

func (suite *FacilityDeleteGuardTestSuite) TestList_CanDeleteReflectsAssociations() {
	admin := suite.seedAdmin()
	empty := suite.seedFacility()
	withResident := suite.seedFacility()

	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	suite.env.DB.Create(&models.User{
		NameFirst: "Res", NameLast: "Ident",
		Username: "res" + ts, Email: "res" + ts + "@t.test",
		Role: models.Student, FacilityID: withResident.ID,
	})

	resp := NewRequest[[]models.FacilityWithStats](suite.env.Client, suite.T(), http.MethodGet,
		"/api/facilities?per_page=1000", nil).
		WithTestClaims(&handlers.Claims{Role: models.SystemAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusOK)

	byID := map[uint]models.FacilityWithStats{}
	for _, f := range resp.GetData() {
		byID[f.ID] = f
	}
	suite.True(byID[empty.ID].CanDelete, "empty facility should be deletable")
	suite.False(byID[withResident.ID].CanDelete, "facility with a resident should not be deletable")
}

func (suite *FacilityDeleteGuardTestSuite) TestDelete_DeptAdmin_Succeeds() {
	admin := suite.seedAdmin()
	facility := suite.seedFacility()

	NewRequest[any](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/facilities/%d", facility.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.DepartmentAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusNoContent)

	var live int64
	suite.env.DB.Model(&models.Facility{}).
		Where("id = ? AND deleted_at IS NULL", facility.ID).
		Count(&live)
	suite.Equal(int64(0), live, "department admin should be able to delete an empty facility")
}

func (suite *FacilityDeleteGuardTestSuite) TestDelete_FacilityAdmin_Forbidden() {
	admin := suite.seedAdmin()
	facility := suite.seedFacility()

	NewRequest[any](suite.env.Client, suite.T(), http.MethodDelete,
		fmt.Sprintf("/api/facilities/%d", facility.ID), nil).
		WithTestClaims(&handlers.Claims{Role: models.FacilityAdmin, UserID: admin.ID, FacilityID: 0}).
		Do().
		ExpectStatus(http.StatusUnauthorized)

	suite.assertStillExists(facility.ID)
}

func (suite *FacilityDeleteGuardTestSuite) assertStillExists(facilityID uint) {
	var count int64
	suite.env.DB.Model(&models.Facility{}).
		Where("id = ? AND deleted_at IS NULL", facilityID).
		Count(&count)
	suite.Equal(int64(1), count, "facility should still exist after blocked delete")
}

func TestFacilityDeleteGuardTestSuite(t *testing.T) {
	suite.Run(t, new(FacilityDeleteGuardTestSuite))
}
