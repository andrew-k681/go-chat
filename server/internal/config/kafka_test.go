package config

import (
	"strings"
	"testing"
)

func TestKafkaDisabledByDefault(t *testing.T) {
	cfg, err := load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Kafka.Brokers) != 0 || cfg.Kafka.Topic != "room-events" {
		t.Errorf("Kafka = %+v", cfg.Kafka)
	}
}

func TestKafkaValid(t *testing.T) {
	cfg, err := load(map[string]string{
		"KAFKA_BROKERS":       "localhost:19092, b2:9092",
		"EVENTS_KEY":          strings.Repeat("e", 32),
		"KAFKA_TLS":           "true",
		"KAFKA_SASL_USER":     "chat",
		"KAFKA_SASL_PASSWORD": "pw",
	})
	if err != nil {
		t.Fatal(err)
	}
	if k := cfg.Kafka; len(k.Brokers) != 2 || !k.TLS || k.SASLMechanism != "SCRAM-SHA-256" {
		t.Errorf("Kafka = %+v", k)
	}
}

func TestKafkaInvalid(t *testing.T) {
	brokers := map[string]string{"KAFKA_BROKERS": "localhost:19092", "EVENTS_KEY": strings.Repeat("e", 32)}
	with := func(kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range brokers {
			m[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	tests := []struct {
		name string
		env  map[string]string
		key  string
	}{
		{"broker without port", with("KAFKA_BROKERS", "localhost"), "KAFKA_BROKERS"},
		{"broker bad port", with("KAFKA_BROKERS", "h:99999"), "KAFKA_BROKERS"},
		{"no events key", with("EVENTS_KEY", ""), "EVENTS_KEY"},
		{"short events key", with("EVENTS_KEY", "short"), "EVENTS_KEY"},
		{"bad topic", with("KAFKA_TOPIC", "a/b"), "KAFKA_TOPIC"},
		{"bad tls", with("KAFKA_TLS", "yes please"), "KAFKA_TLS"},
		{"user without password", with("KAFKA_SASL_USER", "u"), "KAFKA_SASL_PASSWORD"},
		{"plain mechanism", with("KAFKA_SASL_USER", "u", "KAFKA_SASL_PASSWORD", "p", "KAFKA_SASL_MECHANISM", "PLAIN"), "KAFKA_SASL_MECHANISM"},
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

func TestStatsConfig(t *testing.T) {
	get := func(env map[string]string) func(string) string { return func(k string) string { return env[k] } }

	_, err := LoadStatsFrom(get(nil))
	if err == nil || !strings.Contains(err.Error(), "KAFKA_BROKERS") || !strings.Contains(err.Error(), "STATS_TOKEN") {
		t.Fatalf("want brokers and token errors, got %v", err)
	}
	s, err := LoadStatsFrom(get(map[string]string{"KAFKA_BROKERS": "localhost:19092", "STATS_TOKEN": strings.Repeat("t", 32)}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != "127.0.0.1:8081" || s.Group != "stats" {
		t.Errorf("defaults = %+v", s)
	}
}
