package programs

import (
	"context"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/identity"
	"github.com/UnlockedLabs/unlocked-core/backend/src/modules/module"

	log "github.com/sirupsen/logrus"
)

// Programs' background job: class activation. It runs in the scheduler
// itself. Until EN-197, core's scheduler published it to NATS and
// provider-middleware ran it.

const (
	// everyMorningAt5AM is when class activation runs, in the scheduler's time
	// zone.
	everyMorningAt5AM = "0 5 * * *"
	activationTimeout = 30 * time.Minute
)

// scheduleJobs runs class activation every morning and, outside dev, once
// at start.
func scheduleJobs(s module.Scheduler, store *Store, dev bool) {
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), activationTimeout)
		defer cancel()
		if err := activateScheduledClasses(ctx, store); err != nil {
			log.Errorf("failed to activate scheduled classes: %v", err)
		}
	}
	if err := s.Cron(everyMorningAt5AM, run); err != nil {
		log.Errorf("Failed to schedule class activation: %v", err)
	}
	if dev {
		return
	}
	if err := s.AtStart(run); err != nil {
		log.Errorf("Failed to schedule class activation at start: %v", err)
	}
}

// activateScheduledClasses makes Active every Scheduled class whose start
// date has come in its facility's time zone, and gives its Enrolled
// enrollments that have no enrolled date today's date. It makes the changes
// as the system_batch user, which the models' update hooks record.
func activateScheduledClasses(ctx context.Context, store *Store) error {
	batchUserID, err := store.SystemBatchUserID(ctx)
	if err != nil {
		return err
	}
	cohortIDs, err := store.ScheduledCohortsDue(ctx)
	if err != nil {
		return err
	}
	if len(cohortIDs) == 0 {
		log.Infoln("no scheduled classes are due for activation")
		return nil
	}
	log.Infof("activating %d scheduled class(es): %v", len(cohortIDs), cohortIDs)
	batchCtx := identity.NewContext(ctx, &identity.Claims{UserID: batchUserID})
	return store.ActivateCohorts(batchCtx, cohortIDs, batchUserID, time.Now().UTC())
}
