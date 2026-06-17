// Command ingest runs the public notification trigger API. It authenticates
// requests by channel API key, rate-limits per channel, and enqueues tasks onto
// the broker for asynchronous delivery.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/channel"
	"github.com/rh1tech/pheme/api/internal/config"
	"github.com/rh1tech/pheme/api/internal/ratelimit"
	"github.com/rh1tech/pheme/api/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()

	// TODO: swap in MongoDB-backed store and RabbitMQ publisher.
	db := store.NewMemory()
	pub := broker.NewMemory(0)
	limiter := ratelimit.NewTokenBucket(20, 40) // 20 req/s, burst 40, per channel

	h := &channel.IngestHandler{Store: db, Publisher: pub, Limiter: limiter}
	mux := http.NewServeMux()
	h.Routes(mux)

	srv := &http.Server{
		Addr:              cfg.IngestAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("ingest API listening", "addr", cfg.IngestAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("ingest server", "error", err)
			os.Exit(1)
		}
	}()

	waitForShutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = pub.Close(ctx)
	_ = db.Close(ctx)
	logger.Info("ingest API stopped")
}

func waitForShutdown() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
