package main

import (
	"context"
	"net/http"
	"time"

	"github.com/UnlockedLabs/unlocked-contracts/jobs"

	"github.com/nats-io/nats.go"
)

func (sh *ServiceHandler) registerRoutes() {
	sh.Mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

const CANCEL_TIMEOUT = 30 * time.Minute

func (sh *ServiceHandler) initSubscription() error {
	subscriptions := []struct {
		topic string
		fn    func(ctx context.Context, msg *nats.Msg)
	}{
		{jobs.ActivateScheduledClasses.PubName(), sh.handleActivateScheduledClasses},
	}
	for _, sub := range subscriptions {
		_, err := sh.nats.QueueSubscribe(sub.topic, "middleware", func(msg *nats.Msg) {
			ctx, cancel := context.WithTimeout(sh.ctx, CANCEL_TIMEOUT)
			defer cancel()
			sub.fn(ctx, msg)
		})
		if err != nil {
			logger().Fatalf("Error subscribing to NATS topic %s: %v", sub.topic, err)
			return err
		}
	}

	return nil
}
