package stats

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/andrew-k681/go-chat/server/internal/config"
)

// Handler serves GET /stats behind a bearer token.
func Handler(agg *Aggregator, token config.Secret) http.Handler {
	want := []byte(token.Reveal())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="stats"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		json.NewEncoder(w).Encode(agg.Snapshot())
	})
	return mux
}
