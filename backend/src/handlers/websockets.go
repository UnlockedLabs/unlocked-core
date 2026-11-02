package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/UnlockedLabs/unlocked-core/backend/src/database"

	ocapi "github.com/UnlockedLabs/unlocked-contracts/opencontent"
	"github.com/coder/websocket"
	"github.com/nats-io/nats.go"
	log "github.com/sirupsen/logrus"
)

func (srv *Server) registerWebsocketRoute() {
	srv.Mux.Handle("/api/ws/listen", srv.authMiddleware(srv.handleError(srv.handleWebsocketConnection), nil))
}

const (
	bytesBuffer = 256
)

type WsEventType string

const (
	ClientHello   WsEventType = "client_hello"
	ClientGoodbye WsEventType = "client_goodbye"
	VisitEvent    WsEventType = "visits"
	BookmarkEvent WsEventType = "bookmarks"
)

type MsgContent struct {
	ActivityID int64  `json:"activity_id"`
	Msg        string `json:"msg"`
}

type WsMsg struct {
	EventType WsEventType `json:"event_type"`
	UserID    uint        `json:"user_id"`
	SessionID string      `json:"session_id"`
	Msg       MsgContent  `json:"msg"`
}

func (ws *WsMsg) getClientKey() uint {
	return ws.UserID
}

type WsClient struct {
	Conn                  *websocket.Conn
	UserID                uint
	EventType             WsEventType
	SessionID             string
	OpenContentActivityID int64
	ctx                   context.Context
	cancel                context.CancelFunc
	sendChan              chan []byte
	mutex                 sync.Mutex
}

func (ws *WsClient) getClientKey() uint {
	return ws.UserID
}

type ClientManager struct {
	clients map[uint]*WsClient
	mutex   sync.RWMutex
}

// Close ends every open tab, as the server shuts down. It takes the tabs out
// under the lock and ends them after releasing it, all at once, since ending
// a tab's visit asks Open Content over HTTP.
func (c *ClientManager) Close(db *database.DB, openContent ocapi.OpenContent) {
	c.mutex.Lock()
	clients := c.clients
	c.clients = make(map[uint]*WsClient)
	ends := make([]tabEnd, 0, len(clients))
	for _, client := range clients {
		ends = append(ends, client.tabEnd())
	}
	c.mutex.Unlock()

	var wg sync.WaitGroup
	for _, end := range ends {
		wg.Go(func() { end.run(db, openContent) })
	}
	for key, client := range clients {
		client.cancel()
		if client.Conn != nil {
			err := client.Conn.Close(websocket.StatusNormalClosure, "server shutdown")
			if err != nil {
				log.Warnf("Failed to close WebSocket for user %d: %v", key, err)
			}
			client.Conn = nil
		}
	}
	wg.Wait()
}

// forwardOpenContentNotifications sends each message Open Content publishes
// to the user's open tab. Every server subscribes, without a queue group,
// since each holds different tabs.
func (srv *Server) forwardOpenContentNotifications() error {
	_, err := srv.nats.Subscribe(ocapi.NotifySubject, func(msg *nats.Msg) {
		srv.notifyFromOpenContent(msg.Data)
	})
	return err
}

// notifyFromOpenContent sends one of Open Content's messages to the user's
// open tab. The tab ends the message's activity when it closes.
func (srv *Server) notifyFromOpenContent(data []byte) {
	var n ocapi.Notification
	if err := json.Unmarshal(data, &n); err != nil {
		log.Errorf("Failed to read an Open Content notification: %v", err)
		return
	}
	srv.wsClient.notifyUser(WsMsg{
		EventType: WsEventType(n.Event),
		UserID:    n.UserID,
		Msg:       MsgContent{ActivityID: n.ActivityID, Msg: n.Text},
	})
}

func newClientManager() *ClientManager {
	return &ClientManager{
		clients: make(map[uint]*WsClient),
		mutex:   sync.RWMutex{},
	}
}

func (cm *ClientManager) addClient(client *WsClient) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	clientKey := client.getClientKey()
	if cm.clients[clientKey] == nil {
		cm.clients[clientKey] = client
		log.Debugf("Added client with key/user_id %d", clientKey)
	} else {
		log.Warnf("Client already existed with key/user_id %d", clientKey)
	}
}

func (cm *ClientManager) removeClient(client *WsClient, reason string) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	if client.Conn == nil {
		log.Warn("Connection already closed, skipping removal.")
		return
	}
	clientKey := client.getClientKey()
	if cm.clients[clientKey] != nil {
		log.Debugf("Removing client key/user_id %d", clientKey)
		err := client.Conn.Close(websocket.StatusNormalClosure, reason)
		if err != nil {
			log.Errorf("Failed to close connection: %v", err)
		}
		delete(cm.clients, clientKey)
		client.Conn = nil
		client.cancel()
	}
}

func (cm *ClientManager) notifyUser(event WsMsg) {
	clientKey := event.getClientKey()
	cm.mutex.RLock()
	client, exists := cm.clients[clientKey]
	cm.mutex.RUnlock()
	if exists {
		client.mutex.Lock()
		defer client.mutex.Unlock()
		client.send(event)
		return
	}
}

func (client *WsClient) send(event WsMsg) {
	log.Debugf("Sending message to user_id %d, message: %s, activityID: %d", client.UserID, event.Msg.Msg, event.Msg.ActivityID)
	if event.Msg.ActivityID > 0 {
		client.OpenContentActivityID = event.Msg.ActivityID
	}
	response, err := json.Marshal(event)
	if err != nil {
		log.Errorf("Failed to marshal event: %v", err)
		return
	}
	client.sendChan <- response
}

func (client *WsClient) writePump() {
	for {
		select {
		case message := <-client.sendChan:
			log.Debugf("Writing message to user_id %d", client.UserID)
			err := client.Conn.Write(client.ctx, websocket.MessageText, message)
			if err != nil {
				log.Errorf("Failed to write message: %v", err)
				return
			}
		case <-client.ctx.Done():
			return
		}
	}
}

func (srv *Server) handleWebsocketConnection(w http.ResponseWriter, r *http.Request, log sLog) error {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return newInternalServerServiceError(err, "")
	}
	log.debug("WebSocket connection established")
	user := r.Context().Value(ClaimsKey).(*Claims)
	ctx, cancel := context.WithCancel(context.Background())
	client := &WsClient{
		Conn:     conn,
		UserID:   user.UserID,
		ctx:      ctx,
		cancel:   cancel,
		sendChan: make(chan []byte, bytesBuffer),
	}
	srv.handleIfClientExists(client, "connected from a different device or tab")
	srv.wsClient.addClient(client)
	go client.writePump()
	go srv.handleWsHeartbeat(client)
	go srv.handleWsReader(ctx, client)
	<-ctx.Done()
	srv.wsClient.removeClient(client, "")
	return nil
}

// handleCleanup ends the session and the Open Content visit of the user's
// open tab. It reads them under the lock and ends them after releasing it,
// since ending the visit asks Open Content over HTTP, and every other user's
// connect waits on the lock.
func (cm *ClientManager) handleCleanup(db *database.DB, openContent ocapi.OpenContent, clientKey uint) {
	cm.mutex.RLock()
	client, ok := cm.clients[clientKey]
	var end tabEnd
	if ok {
		end = client.tabEnd()
	}
	cm.mutex.RUnlock()
	if ok {
		end.run(db, openContent)
	}
}

// tabEnd is what ends when a user's tab closes: its login session and its
// Open Content visit.
type tabEnd struct {
	userID     uint
	sessionID  string
	activityID int64
}

func (client *WsClient) tabEnd() tabEnd {
	return tabEnd{userID: client.UserID, sessionID: client.SessionID, activityID: client.OpenContentActivityID}
}

func (end tabEnd) run(db *database.DB, openContent ocapi.OpenContent) {
	if end.sessionID != "" {
		db.LogUserSessionEnded(end.userID, end.sessionID)
	}
	if end.activityID > 0 {
		openContent.EndActivity(context.Background(), end.activityID)
	}
}

func (srv *Server) handleIfClientExists(client *WsClient, reason string) {
	clientKey := client.getClientKey()
	srv.wsClient.mutex.RLock()
	existingClient, exists := srv.wsClient.clients[clientKey]
	srv.wsClient.mutex.RUnlock()
	if exists {
		log.Warnf("client exists for key/user_id %d. Closing connection.", clientKey)
		srv.wsClient.handleCleanup(srv.Db, srv.openContent, clientKey)
		srv.wsClient.removeClient(existingClient, reason)
	}
}

func (srv *Server) handleWsReader(ctx context.Context, client *WsClient) {
	defer client.cancel()
	for {
		_, msg, err := client.Conn.Read(ctx)
		if err != nil { //if there was an error then we should attempt a clean up
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
				websocket.CloseStatus(err) == websocket.StatusGoingAway {
				log.Trace("WebSocket connection closed by client")
				srv.handleIfClientExists(client, "websocket closed, unable to read message")
			} else {
				log.Debug("Error reading from WebSocket, due to frontend closure")
			}
			srv.wsClient.removeClient(client, "websocket closed, unable to read message")
			return
		}
		var event WsMsg
		if err := json.Unmarshal(msg, &event); err != nil {
			log.Warnf("Invalid message event from user %d: %v", client.UserID, err)
			continue
		}
		switch event.EventType {
		case VisitEvent:
			srv.openContent.EndActivity(context.Background(), event.Msg.ActivityID)
		case ClientGoodbye:
			srv.Db.LogUserSessionEnded(event.UserID, event.SessionID)
		case ClientHello:
			client.SessionID = event.SessionID
			srv.Db.LogUserSessionStarted(client.UserID, event.SessionID)
		default:
			log.Warnf("Invalid event type %s", event.EventType)
		}
	}
}

func (srv *Server) handleWsHeartbeat(client *WsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-client.ctx.Done():
			return
		case <-ticker.C:
			if err := client.Conn.Ping(client.ctx); err != nil {
				log.Errorf("Failed to send ping: %v", err)
				srv.wsClient.removeClient(client, "ping failed")
				return
			}
		}
	}
}
