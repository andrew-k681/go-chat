// Command server is the go_chat signaling server: it creates rooms, hands out
// ICE servers and relays WebRTC signaling. Configuration comes from env vars
// (see internal/config).
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/andrew-k681/go-chat/server/internal/config"
	"github.com/andrew-k681/go-chat/server/internal/events"
	"github.com/andrew-k681/go-chat/server/internal/hub"
	"github.com/andrew-k681/go-chat/server/internal/ice"
	"github.com/andrew-k681/go-chat/server/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config:\n%w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	var static fs.FS
	if cfg.StaticDir != "" {
		root, err := os.OpenRoot(cfg.StaticDir) // confines file access, incl. symlinks
		if err != nil {
			return err
		}
		defer root.Close()
		static = root.FS()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := hub.Options{
		MaxPeers:      cfg.MaxPeers,
		MaxRooms:      10_000,
		MaxRoomsPerIP: 20,
		RoomTTL:       cfg.RoomTTL,
	}
	var producer *events.Producer
	if len(cfg.Kafka.Brokers) > 0 {
		if producer, err = events.NewProducer(cfg.Kafka, cfg.EventsKey, log); err != nil {
			return fmt.Errorf("kafka: %w", err)
		}
		opts.OnEvent = producer.Emit
	}
	h := hub.New(opts)
	go h.Run(ctx)

	api := server.New(h, ice.NewProvider(cfg), cfg.AllowOrigins, log)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.Handler(static),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Addr, "origins", cfg.AllowOrigins,
		"turn", len(cfg.TURNURLs) > 0, "static", cfg.StaticDir != "", "kafka", producer != nil)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	h.Close() // WebSockets are hijacked, so Shutdown alone won't close them
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if producer != nil {
		api.Wait(shutdownCtx) // the last PeerLeft events are emitted as sockets close
		producer.Close(shutdownCtx)
		log.Info("room events flushed", "dropped_total", producer.Dropped())
	}
	return nil
}
