// Package server wires the HTTP API, the signaling WebSocket and (optionally)
// the built client onto one http.Handler. See PROTOCOL.md for the contract.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/andrew-k681/go-chat/server/internal/hub"
	"github.com/andrew-k681/go-chat/server/internal/ice"
)

var roomIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type Server struct {
	hub      *hub.Hub
	ice      *ice.Provider
	origins  []string // normalized, exact match
	log      *slog.Logger
	upgrader websocket.Upgrader
	conns    sync.WaitGroup // open WebSocket handlers
}

func New(h *hub.Hub, p *ice.Provider, allowOrigins []string, log *slog.Logger) *Server {
	s := &Server{hub: h, ice: p, origins: allowOrigins, log: log}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return s.originAllowed(r.Header.Get("Origin")) },
	}
	return s
}

// Handler returns the routes. static may be nil (API-only, e.g. dev with Vite).
func (s *Server) Handler(static fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/rooms", s.createRoom)
	mux.HandleFunc("GET /api/rooms/{id}", s.getRoom)
	mux.HandleFunc("GET /api/ice", s.getICE)
	mux.HandleFunc("GET /ws/{id}", s.serveWS)
	mux.HandleFunc("/api/", http.NotFound)
	mux.HandleFunc("/ws/", http.NotFound)
	if static != nil {
		mux.Handle("/", spa(static))
	}
	return securityHeaders(mux)
}

// Wait blocks until every WebSocket handler has returned (after hub.Close)
// or ctx is done.
func (s *Server) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() { s.conns.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// originAllowed requires an Origin from the allowlist. Browsers always send
// Origin on WebSocket upgrades and cross-site POSTs, so this blocks
// cross-site WebSocket hijacking and drive-by room creation.
func (s *Server) originAllowed(origin string) bool {
	return origin != "" && slices.Contains(s.origins, strings.ToLower(origin))
}

func (s *Server) createRoom(w http.ResponseWriter, r *http.Request) {
	if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	id, err := s.hub.CreateRoom(clientIP(r))
	switch {
	case errors.Is(err, hub.ErrShuttingDown):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	}
	s.log.Debug("room created", "room", short(id))
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (s *Server) getRoom(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, ok := 0, false
	if roomIDRe.MatchString(id) {
		n, ok = s.hub.PeerCount(id)
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "peers": n})
}

func (s *Server) getICE(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"iceServers": s.ice.Servers()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// securityHeaders applies to every response. no-referrer matters because the
// room URL is the credential and must not leak to third parties.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// spa serves files from fsys and falls back to index.html for client routes
// such as /room/{id}.
func spa(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "."
		}
		if _, err := fs.Stat(fsys, name); err != nil {
			http.ServeFileFS(w, r, fsys, "index.html")
			return
		}
		files.ServeHTTP(w, r)
	})
}

// clientIP uses the TCP peer address. Behind a reverse proxy every client
// shares the proxy's address, so per-IP limits become global limits.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// short returns a log-safe prefix of a room id (the full id is a credential).
func short(id string) string {
	if len(id) > 6 {
		return id[:6] + "…"
	}
	return id
}
