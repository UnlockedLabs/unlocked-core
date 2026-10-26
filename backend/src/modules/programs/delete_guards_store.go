package programs

import (
	"database/sql"
)

// ClassBlockingChildren counts every type of child record attached to a class
// in a single round-trip. Used by the delete-check preflight and by the 409
// response on DELETE /api/program-classes/{id}.
func (db *Store) ClassBlockingChildren(classID int) (DeleteBlockingChildren, error) {
	var b DeleteBlockingChildren
	row := db.Raw(`
SELECT
  (SELECT COUNT(*) FROM program_class_enrollments
     WHERE cohort_id = ? AND deleted_at IS NULL) AS enrollments,
  (SELECT COUNT(*) FROM program_class_events
     WHERE cohort_id = ? AND deleted_at IS NULL) AS events,
  (SELECT COUNT(*) FROM class_completions
     WHERE cohort_id = ? AND deleted_at IS NULL) AS completions,
  (SELECT COUNT(*) FROM program_class_event_attendance pcea
     JOIN program_class_events e ON e.id = pcea.event_id
     WHERE e.cohort_id = ? AND pcea.deleted_at IS NULL) AS attendance_flags,
  ((SELECT COUNT(*) FROM change_log_entries
       WHERE table_name = 'program_class_cohorts' AND parent_ref_id = ?)
   + (SELECT COUNT(*) FROM program_classes_history
       WHERE table_name = 'program_class_cohorts' AND parent_ref_id = ?)
  ) AS history
`, classID, classID, classID, classID, classID, classID).Row()

	if err := row.Scan(&b.Enrollments, &b.Events, &b.Completions, &b.AttendanceFlags, &b.History); err != nil {
		return b, newGetRecordsDBError(err, "delete_guard_class_counts")
	}

	var status ClassStatus
	if err := db.Model(&ProgramClassCohort{}).
		Where("id = ?", classID).
		Select("status").
		Limit(1).
		Scan(&status).Error; err != nil {
		return b, newGetRecordsDBError(err, "delete_guard_class_status")
	}
	if status != "" && status != Scheduled {
		b.NonDeletableStatus = string(status)
	}
	return b, nil
}

// ProgramBlockingChildren counts every type of child record across every class
// belonging to the program in a single round-trip. b.Classes is the count of
// classes under the program; the other fields aggregate per-class child rows
// across all of them. Program-level change_log entries are added to History.
func (db *Store) ProgramBlockingChildren(programID int) (DeleteBlockingChildren, error) {
	var b DeleteBlockingChildren
	row := db.Raw(`
WITH cls AS (
  SELECT id FROM program_class_cohorts
   WHERE program_id = @pid AND deleted_at IS NULL
)
SELECT
  -- Counted off the CLASS tier, not off cls. A class with no cohorts under it must
  -- still block: DeleteProgram removes neither program_classes nor program_class_cohorts
  -- rows, and there is no DELETE route for the class tier at all -- so a class this
  -- guard misses outlives its program with nothing able to reach it again.
  (SELECT COUNT(*) FROM program_classes
     WHERE program_id = @pid AND deleted_at IS NULL) AS classes,
  (SELECT COUNT(*) FROM program_class_enrollments
     WHERE cohort_id IN (SELECT id FROM cls) AND deleted_at IS NULL) AS enrollments,
  (SELECT COUNT(*) FROM program_class_events
     WHERE cohort_id IN (SELECT id FROM cls) AND deleted_at IS NULL) AS events,
  (SELECT COUNT(*) FROM class_completions
     WHERE cohort_id IN (SELECT id FROM cls) AND deleted_at IS NULL) AS completions,
  (SELECT COUNT(*) FROM program_class_event_attendance pcea
     JOIN program_class_events e ON e.id = pcea.event_id
     WHERE e.cohort_id IN (SELECT id FROM cls) AND pcea.deleted_at IS NULL) AS attendance_flags,
  ((SELECT COUNT(*) FROM change_log_entries
       WHERE table_name = 'program_class_cohorts' AND parent_ref_id IN (SELECT id FROM cls))
   + (SELECT COUNT(*) FROM program_classes_history
       WHERE table_name = 'program_class_cohorts' AND parent_ref_id IN (SELECT id FROM cls))
   + (SELECT COUNT(*) FROM change_log_entries
       WHERE table_name = 'programs' AND parent_ref_id = @pid)
  ) AS history
`, sql.Named("pid", programID)).Row()

	if err := row.Scan(&b.Classes, &b.Enrollments, &b.Events, &b.Completions, &b.AttendanceFlags, &b.History); err != nil {
		return b, newGetRecordsDBError(err, "delete_guard_program_counts")
	}
	return b, nil
}
