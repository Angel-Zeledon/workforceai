package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"aiworkforce/backend/internal/auth"
	"aiworkforce/backend/internal/domain"
)

// closeTokenExpired is the (private-use) close code sent when WSMaxAge elapses.
const closeTokenExpired = 4401

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = 30 * time.Second
)

// ws upgrades the connection, sends the hello frame and then streams hub frames.
func (s *server) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil) // CheckOrigin = same allow-list as CORS
	if err != nil {
		return // Upgrade already replied
	}
	defer conn.Close()

	// Register before building hello so no event is missed in between.
	org := s.org(r)
	// >>> human presence: the socket knows its person (addressed chat frames) and joins the presence hub.
	uid := ""
	if p, ok := auth.PrincipalFrom(r.Context()); ok {
		uid = p.UserID
	} else if s.Presence != nil || s.PeopleChat != nil {
		uid = demoUser
	}
	client := s.Hub.RegisterUser(org, uid)
	defer s.Hub.Unregister(client)
	if s.Presence != nil && uid != "" {
		s.Presence.Join(org, uid, s.personName(r.Context(), org, uid))
		defer s.Presence.Leave(org, uid)
	}
	// <<< human presence

	agents, err := s.Queries.Agents(r.Context())
	if err != nil {
		s.Log.Error("ws hello", "err", err)
		agents = []domain.Agent{}
	}
	hello, _ := json.Marshal(domain.Event{ID: uuid.NewString(), Type: domain.EvHello, TS: time.Now().UTC(), OrgID: org,
		Payload: map[string]any{"agents": list(agents)}})
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	if err := conn.WriteMessage(websocket.TextMessage, hello); err != nil {
		return
	}

	// Reader: only to process control frames and detect disconnects.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(pongWait)) })
		conn.SetReadLimit(4096) // human presence: client frames are tiny (position, status)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if s.Presence != nil && uid != "" {
				_ = s.Presence.Handle(org, uid, msg) // bad frames are ignored; identity is the socket's, never the frame's
			}
		}
	}()

	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	var expired <-chan time.Time
	if s.WSMaxAge > 0 {
		t := time.NewTimer(s.WSMaxAge)
		defer t.Stop()
		expired = t.C
	}
	for {
		select {
		case <-expired:
			// The token the socket was opened with has expired: make the client reconnect with a fresh one.
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(closeTokenExpired, "token expired"), time.Now().Add(writeWait))
			return
		case frame, ok := <-client.Send:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
				return
			}
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-gone:
			return
		}
	}
}
