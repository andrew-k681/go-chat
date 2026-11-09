package server

import (
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"

	"github.com/andrew-k681/go-chat/server/internal/config"
	"github.com/andrew-k681/go-chat/server/internal/hub"
	"github.com/andrew-k681/go-chat/server/internal/ice"
)

const origin = "http://localhost:5173"

func newTestServer(t *testing.T, static fs.FS) *httptest.Server {
	t.Helper()
	h := hub.New(hub.Options{MaxPeers: 2, MaxRooms: 100, MaxRoomsPerIP: 100, RoomTTL: time.Minute})
	p := ice.NewProvider(config.Config{STUNURLs: []string{"stun:s"}})
	s := New(h, p, []string{origin}, slog.New(slog.DiscardHandler))
	ts := httptest.NewServer(s.Handler(static))
	t.Cleanup(func() { h.Close(); ts.Close() })
	return ts
}

func createRoom(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	res, err := http.Post(ts.URL+"/api/rooms", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct{ ID string }
	json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != http.StatusCreated || !roomIDRe.MatchString(body.ID) {
		t.Fatalf("create room: %d %q", res.StatusCode, body.ID)
	}
	return body.ID
}

func dial(t *testing.T, ts *httptest.Server, roomID, org string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/" + roomID
	hdr := http.Header{}
	if org != "" {
		hdr.Set("Origin", org)
	}
	c, res, err := websocket.DefaultDialer.Dial(url, hdr)
	if c != nil {
		t.Cleanup(func() { c.Close() })
	}
	return c, res, err
}

func mustDial(t *testing.T, ts *httptest.Server, roomID string) *websocket.Conn {
	t.Helper()
	c, _, err := dial(t, ts, roomID, origin)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	if err := c.WriteJSON(v); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var m map[string]any
	if err := c.ReadJSON(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestHTTPAPI(t *testing.T) {
	ts := newTestServer(t, nil)
	id := createRoom(t, ts)

	res, _ := http.Get(ts.URL + "/api/rooms/" + id)
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"peers":0`) {
		t.Errorf("get room: %d %s", res.StatusCode, body)
	}
	if res.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Error("missing Referrer-Policy")
	}

	for _, path := range []string{"/api/rooms/doesNotExist123", "/api/rooms/bad!id", "/api/nope"} {
		res, _ := http.Get(ts.URL + path)
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("GET %s = %d, want 404", path, res.StatusCode)
		}
	}

	res, _ = http.Get(ts.URL + "/api/ice")
	var ice struct{ IceServers []map[string]any }
	json.NewDecoder(res.Body).Decode(&ice)
	res.Body.Close()
	if len(ice.IceServers) != 1 {
		t.Errorf("ice = %+v", ice)
	}

	req, _ := http.NewRequest("POST", ts.URL+"/api/rooms", nil)
	req.Header.Set("Origin", "https://evil.example")
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin create = %d, want 403", res.StatusCode)
	}
}

func TestSignalingFlow(t *testing.T) {
	ts := newTestServer(t, nil)
	id := createRoom(t, ts)

	alice := mustDial(t, ts, id)
	send(t, alice, map[string]string{"type": "join", "name": "  Alice "})
	w := recv(t, alice)
	if w["type"] != "welcome" {
		t.Fatalf("alice: %v", w)
	}
	aliceID := w["id"].(string)

	bob := mustDial(t, ts, id)
	send(t, bob, map[string]string{"type": "join", "name": "Bob"})
	pj := recv(t, alice)
	if pj["type"] != "peer-joined" || pj["peer"].(map[string]any)["name"] != "Bob" {
		t.Fatalf("alice: %v", pj)
	}
	w = recv(t, bob)
	peers := w["peers"].([]any)
	if len(peers) != 1 || peers[0].(map[string]any)["name"] != "Alice" {
		t.Fatalf("bob welcome: %v", w)
	}
	bobID := w["id"].(string)

	// The newcomer offers; the server stamps "from".
	send(t, bob, map[string]any{"type": "signal", "to": aliceID, "from": "spoofed",
		"data": map[string]any{"description": map[string]string{"type": "offer", "sdp": "v=0"}}})
	sig := recv(t, alice)
	if sig["type"] != "signal" || sig["from"] != bobID {
		t.Fatalf("signal: %v", sig)
	}

	// Room is full at 2.
	carol := mustDial(t, ts, id)
	send(t, carol, map[string]string{"type": "join", "name": "Carol"})
	if e := recv(t, carol); e["type"] != "error" || e["message"] != "room full" {
		t.Fatalf("carol: %v", e)
	}

	bob.Close()
	if pl := recv(t, alice); pl["type"] != "peer-left" || pl["id"] != bobID {
		t.Fatalf("alice: %v", pl)
	}
}

func TestWSRejections(t *testing.T) {
	ts := newTestServer(t, nil)
	id := createRoom(t, ts)

	for _, org := range []string{"", "https://evil.example"} {
		if _, res, err := dial(t, ts, id, org); err == nil || res.StatusCode != http.StatusForbidden {
			t.Errorf("origin %q: want 403", org)
		}
	}

	tests := []struct {
		name  string
		room  string
		first any
		want  string
	}{
		{"unknown room", "unknownRoom12345", map[string]string{"type": "join", "name": "x"}, "room not found"},
		{"signal before join", id, map[string]any{"type": "signal", "to": "x", "data": map[string]any{}}, "join first"},
		{"empty name", id, map[string]string{"type": "join", "name": "   "}, "invalid name"},
		{"long name", id, map[string]string{"type": "join", "name": strings.Repeat("é", 41)}, "invalid name"},
		{"control char", id, map[string]string{"type": "join", "name": "a\x07b"}, "invalid name"},
		{"wrong type", id, map[string]any{"type": "join", "name": 5}, "invalid message"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mustDial(t, ts, tt.room)
			send(t, c, tt.first)
			if e := recv(t, c); e["type"] != "error" || e["message"] != tt.want {
				t.Fatalf("got %v, want error %q", e, tt.want)
			}
		})
	}

	t.Run("second join", func(t *testing.T) {
		c := mustDial(t, ts, id)
		send(t, c, map[string]string{"type": "join", "name": "A"})
		recv(t, c) // welcome
		send(t, c, map[string]string{"type": "join", "name": "A"})
		if e := recv(t, c); e["message"] != "already joined" {
			t.Fatalf("got %v", e)
		}
	})

	t.Run("oversized message", func(t *testing.T) {
		c := mustDial(t, ts, id)
		send(t, c, map[string]string{"type": "join", "name": strings.Repeat("a", maxMessageSize)})
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := c.ReadMessage(); err == nil {
			t.Fatal("connection should be closed")
		}
	})
}

func TestStaticSPA(t *testing.T) {
	ts := newTestServer(t, fstest.MapFS{
		"index.html":   {Data: []byte("<html>app</html>")},
		"_app/main.js": {Data: []byte("js")},
	})
	for path, want := range map[string]string{"/": "app", "/room/abcdefgh12": "app", "/_app/main.js": "js"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(string(body), want) {
			t.Errorf("GET %s = %d %q", path, res.StatusCode, body)
		}
	}
	res, _ := http.Get(ts.URL + "/api/unknown")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("/api/unknown must not fall back to the SPA: %d", res.StatusCode)
	}
}
