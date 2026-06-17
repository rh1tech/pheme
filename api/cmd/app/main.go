// Command app runs the authenticated user-facing API: auth, channels, API keys,
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
	bus, err := b.Live()
	if err != nil {
		logger.Error("live init", "error", err)
		os.Exit(1)
	}
	pub, err := b.Publisher()
	if err != nil {
		logger.Error("publisher init", "error", err)
		os.Exit(1)
	}
	tokens := b.Tokens()

	adminEmails := make(map[string]bool, len(cfg.AdminEmails))
	for _, e := range cfg.AdminEmails {
		adminEmails[e] = true
	}

	mux := http.NewServeMux()
	(&channel.AuthHandler{Store: db, Tokens: tokens, AdminEmails: adminEmails}).Routes(mux)
	(&channel.AppHandler{
		Store:          db,
		Live:           bus,
		Tokens:         tokens,
		Publisher:      pub,
		Admin:          &channel.AdminHandler{Store: db},
		VAPIDPublicKey: cfg.VAPIDPublicKey,
	}).Routes(mux)

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = pub.Close(shutdownCtx)
	_ = db.Close(shutdownCtx)
	_ = b.Close()
	logger.Info("app API stopped")
}

// withCORS allows the local web dev server to call the API during development.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
