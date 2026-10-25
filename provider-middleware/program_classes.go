package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/programs"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/nats-io/nats.go"
	"gorm.io/gorm"
)

// handleActivateScheduledClasses is the entrypoint for the daily
// `tasks.activate_scheduled_classes` job. It flips any class whose scheduled
// start date has is the current day of the job run (in its facility's local timezone) from Scheduled to
// Active. The status update goes through the same map-based update the user interface uses
// so that ProgramClassCohort.AfterUpdate fires and backfills enrolled_at on the
// class's enrollments.
func (sh *ServiceHandler) handleActivateScheduledClasses(ctx context.Context, msg *nats.Msg) {
	var body map[string]any
	if err := json.Unmarshal(msg.Data, &body); err != nil {
		logger().Errorf("failed to unmarshal activate_scheduled_classes message: %v", err)
		return
	}
	jobId, ok := body["job_id"].(string)
	if !ok {
		logger().Errorf("job_id not found in activate_scheduled_classes message: %v", body)
		return
	}
	success := sh.activateScheduledClasses(ctx) == nil
	sh.cleanupJob(ctx, nil, jobId, success)
}

func (sh *ServiceHandler) activateScheduledClasses(ctx context.Context) error {
	batchUserID, err := sh.systemBatchUserID(ctx)
	if err != nil { //batch id doesn't exist then fail
		logger().Errorf("cannot activate scheduled classes: %v", err)
		return err
	}

	var cohortIDs []int
	if err := sh.db.WithContext(ctx).
		Model(&programs.ProgramClassCohort{}).
		Joins("JOIN facilities f ON f.id = program_class_cohorts.facility_id").
		Where("program_class_cohorts.status = ?", programs.Scheduled).
		Where("program_class_cohorts.archived_at IS NULL").
		Where("program_class_cohorts.start_dt <= (now() AT TIME ZONE f.timezone)::date").
		Pluck("program_class_cohorts.id", &cohortIDs).Error; err != nil {
		logger().Errorf("failed to query scheduled classes to activate: %v", err)
		return err
	}

	if len(cohortIDs) == 0 {
		logger().Infoln("no scheduled classes are due for activation")
		return nil
	}
	logger().Infof("activating %d scheduled class(es): %v", len(cohortIDs), cohortIDs)

	// Programs' models take the update user from identity.Claims.
	batchCtx := identity.NewContext(ctx, &identity.Claims{UserID: batchUserID})
	enrolledAt := time.Now().UTC()
	if err := sh.db.WithContext(batchCtx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Model(&programs.ProgramClassCohort{}).
			Where("id IN ?", cohortIDs).
			Updates(map[string]any{"status": programs.Active}).Error; err != nil {
			return err
		}
		return tx.
			Model(&programs.ProgramClassEnrollment{}).
			Where("cohort_id IN ?", cohortIDs).
			Where("enrollment_status = ?", programs.Enrolled).
			Where("enrolled_at IS NULL").
			Updates(map[string]any{
				"enrolled_at":    enrolledAt,
				"update_user_id": batchUserID,
			}).Error
	}); err != nil {
		logger().Errorf("failed to activate scheduled classes %v: %v", cohortIDs, err)
		return err
	}
	return nil
}

func (sh *ServiceHandler) systemBatchUserID(ctx context.Context) (uint, error) {
	var user models.User
	if err := sh.db.WithContext(ctx).
		Where("username = ?", "system_batch").
		First(&user).Error; err != nil {
		return 0, fmt.Errorf("system_batch user not found: %w", err)
	}
	return user.ID, nil
}
