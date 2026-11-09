package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strconv"
)

var topicRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,249}$`)

// Kafka is shared by the signaling server (producer) and cmd/stats (consumer).
type Kafka struct {
	Brokers       []string // host:port; empty disables Kafka
	Topic         string
	TLS           bool
	SASLMechanism string // SCRAM-SHA-256 or SCRAM-SHA-512; empty means no auth
	SASLUser      string
	SASLPassword  Secret
}

func loadKafka(get func(key, def string) string, errs []error) (Kafka, []error) {
	k := Kafka{
		Brokers:      splitList(get("KAFKA_BROKERS", "")),
		Topic:        get("KAFKA_TOPIC", "room-events"),
		SASLUser:     get("KAFKA_SASL_USER", ""),
		SASLPassword: Secret(get("KAFKA_SASL_PASSWORD", "")),
	}
	for _, b := range k.Brokers {
		host, port, err := net.SplitHostPort(b)
		if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
			errs = append(errs, fmt.Errorf("KAFKA_BROKERS: %q: want host:port", b))
		}
	}
	if !topicRe.MatchString(k.Topic) {
		errs = append(errs, fmt.Errorf("KAFKA_TOPIC=%q: invalid topic name", k.Topic))
	}

	raw := get("KAFKA_TLS", "false")
	tlsOn, err := strconv.ParseBool(raw)
	if err != nil {
		errs = append(errs, fmt.Errorf("KAFKA_TLS=%q: want true or false", raw))
	}
	k.TLS = tlsOn

	switch {
	case k.SASLUser == "" && k.SASLPassword == "":
	case k.SASLUser == "" || k.SASLPassword == "":
		errs = append(errs, errors.New("KAFKA_SASL_USER and KAFKA_SASL_PASSWORD must be set together"))
	default:
		k.SASLMechanism = get("KAFKA_SASL_MECHANISM", "SCRAM-SHA-256")
		if k.SASLMechanism != "SCRAM-SHA-256" && k.SASLMechanism != "SCRAM-SHA-512" {
			errs = append(errs, fmt.Errorf("KAFKA_SASL_MECHANISM=%q: want SCRAM-SHA-256 or SCRAM-SHA-512", k.SASLMechanism))
		}
	}
	return k, errs
}

// Stats is the configuration of cmd/stats.
type Stats struct {
	Addr     string
	Group    string
	Token    Secret // bearer token for GET /stats
	Kafka    Kafka
	LogLevel slog.Level
}

// LoadStats reads the cmd/stats configuration from the real environment.
func LoadStats() (Stats, error) {
	return LoadStatsFrom(os.Getenv)
}

func LoadStatsFrom(getenv func(string) string) (Stats, error) {
	var errs []error
	get := withDefault(getenv)
	s := Stats{
		// Loopback by default: put a TLS-terminating proxy in front to expose it.
		Addr:  get("STATS_ADDR", "127.0.0.1:8081"),
		Group: get("KAFKA_GROUP", "stats"),
		Token: Secret(get("STATS_TOKEN", "")),
	}
	s.Kafka, errs = loadKafka(get, errs)
	if len(s.Kafka.Brokers) == 0 {
		errs = append(errs, errors.New("KAFKA_BROKERS: required"))
	}
	if len(s.Token) < 32 {
		errs = append(errs, errors.New("STATS_TOKEN: required (at least 32 chars)"))
	}
	s.LogLevel, errs = logLevel(get("LOG_LEVEL", "info"), errs)
	return s, errors.Join(errs...)
}
