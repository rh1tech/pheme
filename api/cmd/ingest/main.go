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

	"github.com/rh1tech/pheme/api/internal/bootstrap"
	"github.com/rh1tech/pheme/api/internal/channel"
	"github.com/rh1tech/pheme/api/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	b := bootstrap.New(cfg, logger)

	ctx := context.Background()
	db, err := b.Store(ctx)
	if err != nil {
		logger.Error("store init", "error", err)
		os.Exit(1)
	}
	blobs, err := b.Blob(ctx)
	if err != nil {
		logger.Error("blob init", "error", err)
		os.Exit(1)
	}
	pub, err := b.Publisher()
	if err != nil {
		logger.Error("publisher init", "error", err)
		os.Exit(1)
	}
	limiter := b.Limiter()

	h := &channel.IngestHandler{Store: db, Publisher: pub, Limiter: limiter, Blob: blobs, Dedup: b.Dedup()}
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

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = pub.Close(shutdownCtx)
	_ = db.Close(shutdownCtx)
	_ = b.Close()
	logger.Info("ingest API stopped")
}
