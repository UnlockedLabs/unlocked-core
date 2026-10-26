package main

import (
	"context"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/models"

	log "github.com/sirupsen/logrus"
)

func (sh *ServiceHandler) cleanupJob(ctx context.Context, provId *int, jobId string, success bool) {
	log.Infof("job %s succeeded?: %v \n cleaning up task", jobId, success)
	var task models.RunnableTask
	tx := sh.db.WithContext(ctx).Model(models.RunnableTask{})
	if provId != nil {
		tx = tx.Where("provider_platform_id = ? AND job_id = ?", *provId, jobId)
	} else {
		tx = tx.Where("job_id = ?", jobId)
	}
	if err := tx.
		First(&task).
		Error; err != nil {
		log.Errorf("failed to fetch task: %v", err)
		return
	}
	task.Status = models.StatusPending
	if success {
		task.LastRun = time.Now()
	}
	if err := sh.db.WithContext(ctx).Save(&task).Error; err != nil {
		log.Errorf("failed to update task: %v", err)
		return
	}
}
