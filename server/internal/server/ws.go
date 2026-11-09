package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/websocket"

	"github.com/andrew-k681/go-chat/server/internal/hub"
)

const (
	maxMessageSize = 64 << 10 // SDP can be ~10 KB
	maxNameRunes   = 40
	joinTimeout    = 10 * time.Second
	writeTimeout   = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = pongWait * 9 / 10
)

// clientMsg is the union of client → server messages.
type clientMsg struct {
	Type string          `json:"type"`
	Name string          `json:"name"`
	To   string          `json:"to"`
	Data json.RawMessage `json:"data"`
}

func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	roomID := r.PathValue("id")
	if !roomIDRe.MatchString(roomID) {
		http.NotFound(w, r)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil) // rejects bad Origin with 403
	if err != nil {
		return
	}
	s.conns.Add(1)
	defer s.conns.Done()
	defer conn.Close()
	conn.SetReadLimit(maxMessageSize)

	// Handshake: the first message must be a valid join.
	conn.SetReadDeadline(time.Now().Add(joinTimeout))
	var m clientMsg
	if err := readMsg(conn, &m); err != nil {
		closeWithError(conn, errBadMessage.Error())
		return
	}
	if m.Type != "join" {
		closeWithError(conn, "join first")
		return
	}
	name, ok := cleanName(m.Name)
	if !ok {
		closeWithError(conn, "invalid name")
		return
	}
	peer, err := s.hub.Join(roomID, name)
	if err != nil {
		closeWithError(conn, err.Error())
		return
	}
	log := s.log.With("room", short(roomID), "peer", peer.ID)
	log.Debug("peer joined")

	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		writeLoop(conn, peer, stop)
	}()

	reason := s.readLoop(conn, roomID, peer)

	s.hub.Leave(roomID, peer)
	close(stop)
	<-writerDone // from here on this goroutine is the only writer
	if reason != "" {
		closeWithError(conn, reason)
	}
	log.Debug("peer left", "reason", reason)
}

// readLoop handles messages after join. It returns a protocol error to send
// to the client, or "" when the connection just went away.
func (s *Server) readLoop(conn *websocket.Conn, roomID string, peer *hub.Peer) string {
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	for {
		var m clientMsg
		if err := readMsg(conn, &m); err != nil {
			if errors.Is(err, errBadMessage) {
				return errBadMessage.Error()
			}
			return "" // closed, timed out or too large
		}
		switch m.Type {
		case "signal":
			if m.To == "" || !isJSONObject(m.Data) {
				return "invalid signal"
			}
			s.hub.Relay(roomID, peer, m.To, m.Data)
		case "join":
			return "already joined"
		default:
			return "unknown message type"
		}
	}
}

// writeLoop is the connection's only writer while the peer is in the hub.
// It drains the peer's queue in order and keeps the socket alive with pings.
func writeLoop(conn *websocket.Conn, peer *hub.Peer, stop <-chan struct{}) {
	ping := time.NewTicker(pingPeriod)
	defer ping.Stop()
	for {
		select {
		case <-stop:
			return
		case <-peer.Gone():
			// Dropped by the hub: closing the socket unblocks the reader.
			deadline := time.Now().Add(writeTimeout)
			conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, ""), deadline)
			conn.Close()
			return
		case msg := <-peer.Out():
			conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				conn.Close()
				return
			}
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeTimeout)); err != nil {
				conn.Close()
				return
			}
		}
	}
}

// errBadMessage covers binary frames and JSON that doesn't fit clientMsg.
var errBadMessage = errors.New("invalid message")

func readMsg(conn *websocket.Conn, v *clientMsg) error {
	typ, data, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	if typ != websocket.TextMessage || json.Unmarshal(data, v) != nil {
		return errBadMessage
	}
	return nil
}

// closeWithError sends {"type":"error"} and a normal close frame. Only call
// it when no other goroutine writes to conn.
func closeWithError(conn *websocket.Conn, text string) {
	deadline := time.Now().Add(writeTimeout)
	conn.SetWriteDeadline(deadline)
	conn.WriteMessage(websocket.TextMessage, hub.ErrorMessage(text))
	conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, text), deadline)
}

// cleanName trims the display name and enforces 1..40 printable runes.
func cleanName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxNameRunes {
		return "", false
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", false
	}
	return name, true
}

func isJSONObject(raw json.RawMessage) bool {
	return bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{"))
}
