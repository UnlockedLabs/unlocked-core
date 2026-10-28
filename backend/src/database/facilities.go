package database

import (
	"fmt"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func (db *DB) GetAllFacilities(page, itemsPerPage int) (int64, []models.Facility, error) {
	var total int64
	var facilities []models.Facility

	tx := db.Model(&models.Facility{})
	if err := tx.Count(&total).Error; err != nil {
		return total, nil, newGetRecordsDBError(err, "facilities")
	}
	if err := tx.Limit(itemsPerPage).Offset(calcOffset(page, itemsPerPage)).Find(&facilities).Error; err != nil {
		return total, nil, newGetRecordsDBError(err, "facilities")
	}
	return total, facilities, nil
}

func (db *DB) GetAllFacilitiesWithStats(args *models.QueryContext) ([]models.FacilityWithStats, error) {
	if err := db.Model(&models.Facility{}).Count(&args.Total).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}

	const query = `SELECT
			f.id, f.name, f.timezone, f.created_at, f.updated_at,
			COUNT(DISTINCT CASE WHEN p.is_active = true AND p.archived_at IS NULL AND p.deleted_at IS NULL THEN p.id END) AS active_programs,
			COUNT(DISTINCT CASE WHEN pc.status IN ('Active') AND pc.archived_at IS NULL AND pc.deleted_at IS NULL THEN pc.id END) AS active_classes,
			COUNT(DISTINCT CASE WHEN u.role = 'student' AND u.deleted_at IS NULL AND u.deactivated_at IS NULL THEN u.id END) AS total_residents,
			NOT (
				EXISTS(SELECT 1 FROM users WHERE facility_id = f.id AND deleted_at IS NULL)
				OR EXISTS(SELECT 1 FROM program_classes WHERE facility_id = f.id AND deleted_at IS NULL)
				OR EXISTS(SELECT 1 FROM facilities_programs WHERE facility_id = f.id AND deleted_at IS NULL)
				OR EXISTS(SELECT 1 FROM rooms WHERE facility_id = f.id AND deleted_at IS NULL)
				OR EXISTS(SELECT 1 FROM user_account_history WHERE facility_id = f.id)
			) AS can_delete
		FROM facilities f
		LEFT JOIN facilities_programs fp ON fp.facility_id = f.id
			AND fp.deleted_at IS NULL
		LEFT JOIN programs p ON p.id = fp.program_id
			AND p.deleted_at IS NULL
		LEFT JOIN program_class_cohorts pc ON pc.facility_id = f.id
			AND pc.deleted_at IS NULL
		LEFT JOIN users u ON u.facility_id = f.id
		WHERE f.deleted_at IS NULL
		GROUP BY f.id, f.name, f.timezone, f.created_at, f.updated_at
		ORDER BY f.name ASC
	`
	var results []models.FacilityWithStats
	if err := db.Raw(query).Offset(args.CalcOffset()).Limit(args.PerPage).Scan(&results).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}
	return results, nil
}

func (db *DB) GetAllFacilitiesOrdered() ([]models.Facility, error) {
	var facilities []models.Facility
	if err := db.Order("name").Find(&facilities).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}
	return facilities, nil
}

func (db *DB) GetFacilityByID(id int) (*models.Facility, error) {
	var facility models.Facility
	if err := db.Where("id = ?", fmt.Sprintf("%d", id)).First(&facility).Error; err != nil {
		return nil, newNotFoundDBError(err, "facilities")
	}
	return &facility, nil
}

func (db *DB) GetFacilityNamesByIDs(ids []uint) ([]string, error) {
	var names []string
	if len(ids) == 0 {
		return names, nil
	}
	if err := db.Model(&models.Facility{}).Where("id IN ?", ids).Order("name").Pluck("name", &names).Error; err != nil {
		return nil, newGetRecordsDBError(err, "facilities")
	}
	return names, nil
}

func (db *DB) CreateFacility(facility *models.Facility) error {
	if err := Validate().Struct(facility); err != nil {
		log.Error("Validation Error")
		return NewDBError(err, "facilities")
	}

	if err := db.Create(&facility).Error; err != nil {
		log.Error("error creating facility in database")
		return newCreateDBError(err, "facilities")
	}
	return nil
}

func (db *DB) UpdateFacility(facility *models.Facility, id uint) error {
	facility.ID = id
	if err := Validate().Struct(facility); err != nil {
		log.Error("Validation Error")
		return NewDBError(err, "facilities")
	}
	if err := db.Save(&facility).Error; err != nil {
		log.WithField("facility_id", facility.ID).Error("error updating facility name database/UpdateFacility")
		return newUpdateDBError(err, "facilities")
	}
	return nil
}

func (db *DB) DeleteFacility(id int) error {
	updates := db.softDeleteMap()
	result := db.Model(&models.Facility{}).
		Where("id = ? AND deleted_at IS NULL", fmt.Sprintf("%d", id)).
		Updates(updates)
	if result.Error != nil {
		return newDeleteDBError(result.Error, "facilities")
	}
	if result.RowsAffected == 0 {
		return newDeleteDBError(gorm.ErrRecordNotFound, "facilities")
	}
	return nil
}
