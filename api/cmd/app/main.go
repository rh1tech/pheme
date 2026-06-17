// Command app runs the authenticated user-facing API: channels, API keys,
// devices, subscriptions, message history, and a live event stream.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rh1tech/pheme/api/internal/channel"
	"github.com/rh1tech/pheme/api/internal/config"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()

	// TODO: swap in MongoDB-backed store and Redis-backed live bus.
	db := store.NewMemory()
	bus := live.NewMemoryBus()

	h := &channel.AppHandler{Store: db, Live: bus}
	mux := http.NewServeMux()
	h.Routes(mux)

	srv := &http.Server{
		Addr:              cfg.AppAddr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("app API listening", "addr", cfg.AppAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("app server", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = db.Close(ctx)
	logger.Info("app API stopped")
}

// withCORS allows the local web dev server to call the API during development.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-User-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
