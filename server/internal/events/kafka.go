package events

import (
	"crypto/tls"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/andrew-k681/go-chat/server/internal/config"
)

// ClientOpts are the connection options shared by producer and consumer.
func ClientOpts(k config.Kafka) []kgo.Opt {
	opts := []kgo.Opt{kgo.SeedBrokers(k.Brokers...)}
	if k.TLS {
		opts = append(opts, kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	auth := scram.Auth{User: k.SASLUser, Pass: k.SASLPassword.Reveal()}
	switch k.SASLMechanism {
	case "SCRAM-SHA-256":
		opts = append(opts, kgo.SASL(auth.AsSha256Mechanism()))
	case "SCRAM-SHA-512":
		opts = append(opts, kgo.SASL(auth.AsSha512Mechanism()))
	}
	return opts
}
