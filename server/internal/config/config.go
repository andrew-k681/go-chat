package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const redacted = "[redacted]"

// Secret hides its value from fmt (%v, %s, %q, %#v), slog and encoders,
// so it never ends up in logs or JSON.
type Secret string

func (Secret) String() string               { return redacted }
func (Secret) GoString() string             { return redacted }
func (Secret) LogValue() slog.Value         { return slog.StringValue(redacted) }
func (Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// Reveal returns the raw value. Use only when signing TURN credentials.
func (s Secret) Reveal() string { return string(s) }

type Config struct {
	Addr         string
	AllowOrigins []string
	MaxPeers     int
	RoomTTL      time.Duration
	STUNURLs     []string
	TURNURLs     []string
	TURNSecret   Secret
	TURNTTL      time.Duration
	StaticDir    string
	LogLevel     slog.Level

	// Room events go to Kafka only when Kafka.Brokers is set.
	Kafka     Kafka
	EventsKey Secret // HMAC key that turns room ids into room_ref
}

// Load reads the configuration from the real environment.
func Load() (Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads the configuration through getenv, so tests can pass a map.
// All problems are collected and returned together via errors.Join.
func LoadFrom(getenv func(string) string) (Config, error) {
	var errs []error
	get := withDefault(getenv)

	cfg := Config{
		Addr:       get("ADDR", ":8080"),
		TURNSecret: Secret(get("TURN_SECRET", "")),
		StaticDir:  get("STATIC_DIR", ""),
	}

	cfg.AllowOrigins = splitList(get("ALLOW_ORIGINS", "http://localhost:5173"))
	if len(cfg.AllowOrigins) == 0 {
		errs = append(errs, errors.New("ALLOW_ORIGINS: at least one origin is required"))
	}
	for i, o := range cfg.AllowOrigins {
		norm, err := normalizeOrigin(o)
		if err != nil {
			errs = append(errs, fmt.Errorf("ALLOW_ORIGINS: %w", err))
			continue
		}
		cfg.AllowOrigins[i] = norm
	}

	cfg.MaxPeers, errs = intInRange(get("MAX_PEERS", "6"), "MAX_PEERS", 2, 16, errs)
	cfg.RoomTTL, errs = durationInRange(get("ROOM_TTL", "10m"), "ROOM_TTL", time.Minute, 24*time.Hour, errs)
	cfg.TURNTTL, errs = durationInRange(get("TURN_TTL", "1h"), "TURN_TTL", 5*time.Minute, 24*time.Hour, errs)

	cfg.STUNURLs = splitList(get("STUN_URLS", "stun:stun.l.google.com:19302"))
	errs = checkPrefixes(cfg.STUNURLs, "STUN_URLS", []string{"stun:", "stuns:"}, errs)

	cfg.TURNURLs = splitList(get("TURN_URLS", ""))
	errs = checkPrefixes(cfg.TURNURLs, "TURN_URLS", []string{"turn:", "turns:"}, errs)
	if len(cfg.TURNURLs) > 0 && len(cfg.TURNSecret) < 32 {
		errs = append(errs, errors.New("TURN_SECRET: required (at least 32 chars) when TURN_URLS is set"))
	}

	if cfg.StaticDir != "" {
		if fi, err := os.Stat(cfg.StaticDir); err != nil || !fi.IsDir() {
			errs = append(errs, fmt.Errorf("STATIC_DIR=%q: not a directory", cfg.StaticDir))
		}
	}

	cfg.LogLevel, errs = logLevel(get("LOG_LEVEL", "info"), errs)

	cfg.Kafka, errs = loadKafka(get, errs)
	cfg.EventsKey = Secret(get("EVENTS_KEY", ""))
	if len(cfg.Kafka.Brokers) > 0 && len(cfg.EventsKey) < 32 {
		errs = append(errs, errors.New("EVENTS_KEY: required (at least 32 chars) when KAFKA_BROKERS is set"))
	}

	return cfg, errors.Join(errs...)
}

// withDefault wraps getenv: trimmed value, or def when unset or blank.
func withDefault(getenv func(string) string) func(key, def string) string {
	return func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
}

func logLevel(raw string, errs []error) (slog.Level, []error) {
	var l slog.Level
	if l.UnmarshalText([]byte(raw)) != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL=%q: want debug, info, warn or error", raw))
	}
	return l, errs
}

// splitList splits a comma-separated value, trimming spaces and dropping empty items.
func splitList(s string) []string {
	var out []string
	for item := range strings.SplitSeq(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// normalizeOrigin validates an origin and returns it in the exact form browsers
// send in the Origin header: lowercase scheme://host[:port], no trailing slash.
func normalizeOrigin(o string) (string, error) {
	if o == "*" {
		return "", errors.New(`"*" is not allowed; list explicit origins`)
	}
	u, err := url.Parse(o)
	if err != nil {
		return "", fmt.Errorf("%q: %w", o, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%q: scheme must be http or https", o)
	}
	if u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q: want scheme://host[:port] only", o)
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

func checkPrefixes(urls []string, key string, prefixes []string, errs []error) []error {
	for _, u := range urls {
		ok := false
		for _, p := range prefixes {
			if strings.HasPrefix(u, p) {
				ok = true
				break
			}
		}
		if !ok {
			errs = append(errs, fmt.Errorf("%s: %q must start with one of %v", key, u, prefixes))
		}
	}
	return errs
}

func intInRange(raw, key string, lo, hi int, errs []error) (int, []error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return 0, append(errs, fmt.Errorf("%s=%q: want integer %d..%d", key, raw, lo, hi))
	}
	return n, errs
}

func durationInRange(raw, key string, lo, hi time.Duration, errs []error) (time.Duration, []error) {
	d, err := time.ParseDuration(raw)
	if err != nil || d < lo || d > hi {
		return 0, append(errs, fmt.Errorf("%s=%q: want duration %v..%v", key, raw, lo, hi))
	}
	return d, errs
}
