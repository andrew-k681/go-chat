package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"
)

func load(env map[string]string) (Config, error) {
	return LoadFrom(func(k string) string { return env[k] })
}

func TestDefaults(t *testing.T) {
	cfg, err := load(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Addr != ":8080" || cfg.MaxPeers != 6 || cfg.RoomTTL != 10*time.Minute ||
		cfg.TURNTTL != time.Hour || cfg.LogLevel != slog.LevelInfo {
		t.Errorf("bad defaults: %+v", cfg)
	}
	if !slices.Equal(cfg.AllowOrigins, []string{"http://localhost:5173"}) {
		t.Errorf("AllowOrigins = %v", cfg.AllowOrigins)
	}
	if len(cfg.STUNURLs) != 1 || len(cfg.TURNURLs) != 0 {
		t.Errorf("ICE defaults: stun=%v turn=%v", cfg.STUNURLs, cfg.TURNURLs)
	}
}

func TestOriginsAreTrimmedAndNormalized(t *testing.T) {
	cfg, err := load(map[string]string{"ALLOW_ORIGINS": " https://A.com/ , ,https://b.com:8443 "})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://a.com", "https://b.com:8443"}
	if !slices.Equal(cfg.AllowOrigins, want) {
		t.Errorf("got %v, want %v", cfg.AllowOrigins, want)
	}
}

func TestInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"wildcard origin", map[string]string{"ALLOW_ORIGINS": "*"}, "ALLOW_ORIGINS"},
		{"origin with path", map[string]string{"ALLOW_ORIGINS": "https://x.com/app"}, "ALLOW_ORIGINS"},
		{"origin without scheme", map[string]string{"ALLOW_ORIGINS": "x.com"}, "ALLOW_ORIGINS"},
		{"only commas", map[string]string{"ALLOW_ORIGINS": ",,"}, "ALLOW_ORIGINS"},
		{"max peers not int", map[string]string{"MAX_PEERS": "abc"}, "MAX_PEERS"},
		{"max peers too low", map[string]string{"MAX_PEERS": "1"}, "MAX_PEERS"},
		{"max peers too high", map[string]string{"MAX_PEERS": "100"}, "MAX_PEERS"},
		{"room ttl bad", map[string]string{"ROOM_TTL": "10"}, "ROOM_TTL"},
		{"turn ttl too short", map[string]string{"TURN_TTL": "1m"}, "TURN_TTL"},
		{"stun bad prefix", map[string]string{"STUN_URLS": "turn:x"}, "STUN_URLS"},
		{"turn without secret", map[string]string{"TURN_URLS": "turn:x:3478"}, "TURN_SECRET"},
		{"turn short secret", map[string]string{"TURN_URLS": "turn:x", "TURN_SECRET": "short"}, "TURN_SECRET"},
		{"static dir missing", map[string]string{"STATIC_DIR": "/does/not/exist"}, "STATIC_DIR"},
		{"log level bad", map[string]string{"LOG_LEVEL": "loud"}, "LOG_LEVEL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := load(tt.env)
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("want error mentioning %s, got %v", tt.key, err)
			}
		})
	}
}

func TestErrorsAreCollected(t *testing.T) {
	_, err := load(map[string]string{"MAX_PEERS": "x", "ROOM_TTL": "x"})
	if err == nil || !strings.Contains(err.Error(), "MAX_PEERS") || !strings.Contains(err.Error(), "ROOM_TTL") {
		t.Fatalf("want both errors, got %v", err)
	}
}

func TestTURNValid(t *testing.T) {
	cfg, err := load(map[string]string{
		"TURN_URLS":   "turn:t.example.com:3478?transport=udp, turns:t.example.com:5349",
		"TURN_SECRET": strings.Repeat("s", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TURNURLs) != 2 {
		t.Errorf("TURNURLs = %v", cfg.TURNURLs)
	}
}

func TestSecretNeverPrinted(t *testing.T) {
	const secret = "super-secret-value-that-is-long-enough!!"
	cfg, err := load(map[string]string{"TURN_URLS": "turn:x", "TURN_SECRET": secret})
	if err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(cfg)
	for _, out := range []string{
		fmt.Sprint(cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%q", cfg.TURNSecret), string(js),
	} {
		if strings.Contains(out, secret) {
			t.Errorf("secret leaked: %s", out)
		}
	}
	if cfg.TURNSecret.Reveal() != secret {
		t.Error("Reveal() should return the raw value")
	}
}
