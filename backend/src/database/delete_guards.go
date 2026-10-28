package database

import (
	"database/sql"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
)

// FacilityBlockingChildren counts core's rows that point at a facility. The
// handlers add Programs' and Open Content's (facilityBlockers).
func (db *DB) FacilityBlockingChildren(facilityID int) (models.FacilityBlockingChildren, error) {
	var b models.FacilityBlockingChildren
	row := db.Raw(`SELECT
      (SELECT COUNT(*) FROM users
         WHERE facility_id = @fid AND deleted_at IS NULL) AS users,
      (SELECT COUNT(*) FROM login_activity
         WHERE facility_id = @fid) AS login_activity,
      (SELECT COUNT(*) FROM user_account_history
         WHERE facility_id = @fid) AS user_history`, sql.Named("fid", facilityID)).Row()

	if err := row.Scan(&b.Users, &b.LoginActivity, &b.UserHistory); err != nil {
		return b, newGetRecordsDBError(err, "delete_guard_facility_counts")
	}
	return b, nil
}
