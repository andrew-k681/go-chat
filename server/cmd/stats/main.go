// Command stats consumes room events from Kafka and serves aggregates on
// GET /stats (bearer token). Configuration comes from env vars (see
// internal/config/kafka.go).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andrew-k681/go-chat/server/internal/config"
	"github.com/andrew-k681/go-chat/server/internal/stats"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "stats:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadStats()
	if err != nil {
		return fmt.Errorf("config:\n%w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := stats.NewConsumer(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	agg := stats.NewAggregator()
	consumeErr := make(chan error, 1)
	go func() { consumeErr <- stats.Consume(ctx, client, agg, log) }()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           stats.Handler(agg, cfg.Token),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       time.Minute,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	log.Info("stats listening", "addr", cfg.Addr, "topic", cfg.Kafka.Topic, "group", cfg.Group)

	select {
	case err := <-serveErr:
		return err
	case err := <-consumeErr:
		if ctx.Err() == nil {
			return fmt.Errorf("consumer stopped: %w", err)
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
