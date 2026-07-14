// Command app runs the authenticated user-facing API: auth, channels, API keys,
// devices, subscriptions, message history, and a live event stream.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/bootstrap"
	"github.com/rh1tech/pheme/api/internal/channel"
	"github.com/rh1tech/pheme/api/internal/chat"
	"github.com/rh1tech/pheme/api/internal/config"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/store"
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
	maybeSeedAdmin(ctx, db, cfg.SeedAdminEmail, cfg.SeedAdminPassword, logger)

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
	codes := b.Codes()
	sender, err := b.Mailer()
	if err != nil {
		logger.Error("mailer init", "error", err)
		os.Exit(1)
	}
	// Chat messages are pushed straight from this process (unlike channel messages,
	// which the dispatcher fans out from the broker): a conversation message is a
	// direct member-to-member send with no ingest queue in front of it.
	pusher, err := b.Push(ctx)
	if err != nil {
		logger.Error("push init", "error", err)
		os.Exit(1)
	}

	adminEmails := make(map[string]bool, len(cfg.AdminEmails))
	for _, e := range cfg.AdminEmails {
		adminEmails[e] = true
	}

	mux := http.NewServeMux()
	(&channel.AuthHandler{
		Store:        db,
		Tokens:       tokens,
		Codes:        codes,
		Mailer:       sender,
		AdminEmails:  adminEmails,
		Logger:       logger,
		CodeTTL:      cfg.CodeTTL,
		CodeCooldown: cfg.CodeCooldown,
	}).Routes(mux)
	(&channel.AppHandler{
		Store:     db,
		Live:      bus,
		Tokens:    tokens,
		Publisher: pub,
		Blob:      blobs,
		Admin:     &channel.AdminHandler{Store: db},
		Chat: &chat.Handler{
			Store: db,
			Live:  bus,
			Push:  pusher,
			ICE: chat.ICEConfig{
				URLs:   cfg.TURNURLs,
				Secret: cfg.TURNSecret,
				TTL:    cfg.TURNTTL,
			},
			// The first rate limiter on the app service. Calling gives an authenticated
			// user two ways to make the server work for free — relaying signals and minting
			// TURN credentials — and neither should be unbounded.
			Mailbox: b.CallMailbox(),
			Limiter: b.Limiter(),
			Logger:  logger,
		},
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

// maybeSeedAdmin ensures an initial admin account exists when seed credentials
// are configured. It is a no-op when either credential is empty or the email is
// already registered, so it is safe to run on every startup. This bootstraps the
// first admin without the email-verification flow (and powers the E2E suite).
func maybeSeedAdmin(ctx context.Context, db store.Store, email, password string, logger *slog.Logger) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" || password == "" {
		return
	}
	if _, err := db.UserByEmail(ctx, email); err == nil {
		return // already present
	} else if !errors.Is(err, store.ErrNotFound) {
		logger.Error("seed admin lookup", "error", err)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		logger.Error("seed admin hash", "error", err)
		return
	}
	if _, err := db.CreateUser(ctx, domain.User{
		Email:        email,
		PasswordHash: hash,
		Role:         domain.RoleAdmin,
		Status:       domain.UserActive,
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		logger.Error("seed admin create", "error", err)
		return
	}
	logger.Info("seeded initial admin", "email", email)
}

// withCORS allows the local web dev server to call the API during development.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
