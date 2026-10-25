package database

import (
	"database/sql"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
)

func (db *DB) FacilityBlockingChildren(facilityID int) (models.FacilityBlockingChildren, error) {
	var b models.FacilityBlockingChildren
	row := db.Raw(`SELECT
      (SELECT COUNT(*) FROM users
         WHERE facility_id = @fid AND deleted_at IS NULL) AS users,
      (SELECT COUNT(*) FROM program_classes
         WHERE facility_id = @fid AND deleted_at IS NULL) AS classes,
      (SELECT COUNT(*) FROM facilities_programs
         WHERE facility_id = @fid AND deleted_at IS NULL) AS programs,
      (SELECT COUNT(*) FROM rooms
         WHERE facility_id = @fid AND deleted_at IS NULL) AS rooms,
      (SELECT COUNT(*) FROM login_activity
         WHERE facility_id = @fid) AS login_activity,
      (SELECT COUNT(*) FROM open_content_activities
         WHERE facility_id = @fid) AS open_content_activity,
      (SELECT COUNT(*) FROM open_content_favorites
         WHERE facility_id = @fid) AS open_content_favorites,
      (SELECT COUNT(*) FROM facility_visibility_statuses
         WHERE facility_id = @fid) AS visibility_statuses,
      (SELECT COUNT(*) FROM user_account_history
         WHERE facility_id = @fid) AS user_history`, sql.Named("fid", facilityID)).Row()

	if err := row.Scan(&b.Users, &b.Classes, &b.Programs, &b.Rooms, &b.LoginActivity,
		&b.OpenContentActivity, &b.OpenContentFavorites, &b.VisibilityStatuses, &b.UserHistory); err != nil {
		return b, newGetRecordsDBError(err, "delete_guard_facility_counts")
	}
	return b, nil
}
