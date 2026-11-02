package handlers

import (
	"context"
	"sync"
	"testing"
	"time"

	ocapi "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Open Content's messages reach the user's open tab as the websocket's own
// message shape, as they did while it was a module, and the tab ends the
// message's activity when it closes.
func TestNotifyFromOpenContent(t *testing.T) {
	srv := newTestingServer()
	srv.wsClient = newClientManager()
	client := &WsClient{UserID: 7, sendChan: make(chan []byte, 1)}
	srv.wsClient.clients[7] = client

	t.Run("open tab", func(t *testing.T) {
		srv.notifyFromOpenContent([]byte(`{"user_id":7,"event":"visits","activity_id":42,"text":"hi"}`))
		select {
		case sent := <-client.sendChan:
			assert.JSONEq(t, `{"event_type":"visits","user_id":7,"session_id":"","msg":{"activity_id":42,"msg":"hi"}}`, string(sent))
		case <-time.After(time.Second):
			require.Fail(t, "nothing sent to the user's tab")
		}
		assert.Equal(t, int64(42), client.OpenContentActivityID)
	})

	t.Run("no open tab", func(t *testing.T) {
		assert.NotPanics(t, func() { srv.notifyFromOpenContent([]byte(`{"user_id":8,"event":"visits","activity_id":1}`)) })
		assert.Empty(t, client.sendChan)
	})

	t.Run("not JSON", func(t *testing.T) {
		assert.NotPanics(t, func() { srv.notifyFromOpenContent([]byte(`not json`)) })
		assert.Empty(t, client.sendChan)
	})
}

// endingOpenContent is Open Content for the websocket tests: it records the
// visits it's asked to end, and runs during first, to check what its caller
// holds.
type endingOpenContent struct {
	ocapi.OpenContent
	during func()

	mu    sync.Mutex
	ended []int64
}

func (o *endingOpenContent) EndActivity(_ context.Context, activityID int64) {
	if o.during != nil {
		o.during()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ended = append(o.ended, activityID)
}

// within fails the test unless run returns within a second.
func within(t *testing.T, what string, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		run()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		require.FailNow(t, what+" hung")
	}
}

func newTab(userID uint, activityID int64) *WsClient {
	ctx, cancel := context.WithCancel(context.Background())
	return &WsClient{UserID: userID, OpenContentActivityID: activityID, ctx: ctx, cancel: cancel, sendChan: make(chan []byte, 1)}
}

// Shutting down ends every open tab's visit, and doesn't hang on the lock it
// takes the tabs out under.
func TestClientManagerClose(t *testing.T) {
	srv := newTestingServer()
	oc := &endingOpenContent{}
	cm := newClientManager()
	tabs := []*WsClient{newTab(7, 42), newTab(8, 43)}
	for _, tab := range tabs {
		cm.addClient(tab)
	}

	within(t, "Close", func() { cm.Close(srv.Db, oc) })

	assert.ElementsMatch(t, []int64{42, 43}, oc.ended)
	assert.Empty(t, cm.clients)
	for _, tab := range tabs {
		assert.Error(t, tab.ctx.Err(), "user %d's tab is canceled", tab.UserID)
	}
}

// Ending a tab's visit, which asks Open Content over HTTP, doesn't hold the
// lock every other user's connect waits on.
func TestHandleCleanupReleasesLock(t *testing.T) {
	srv := newTestingServer()
	cm := newClientManager()
	cm.addClient(newTab(7, 42))
	oc := &endingOpenContent{during: func() { cm.addClient(newTab(8, 0)) }}

	within(t, "handleCleanup", func() { cm.handleCleanup(srv.Db, oc, 7) })

	assert.Equal(t, []int64{42}, oc.ended)
	assert.Contains(t, cm.clients, uint(8), "another user connected while the visit ended")
}
