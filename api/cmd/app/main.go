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
	startDebugServer(logger)

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
	// Session revocation is the deny list behind "terminate this device": the middleware
	// refuses a token whose session has been revoked, and the set is hydrated from the store
	// so a revocation outlives a restart.
	revoker := auth.NewSessionRevoker(db)
	if err := revoker.Hydrate(ctx); err != nil {
		logger.Error("session revoker hydrate", "error", err)
		os.Exit(1)
	}
	// Per-user cutoffs too, for the devices a session id cannot name. A revocation that did not
	// survive a restart would be undone by the next deploy.
	if err := revoker.HydrateUsers(ctx); err != nil {
		logger.Error("user revocation hydrate", "error", err)
		os.Exit(1)
	}
	tokens.UseRevoker(revoker)
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
		Store:       db,
		Tokens:      tokens,
		Codes:       codes,
		Mailer:      sender,
		AdminEmails: adminEmails,
		Logger:      logger,
		// The refresh endpoint is public, so it checks revocation itself — otherwise a
		// terminated session could refresh its way back in past the middleware.
		Revoker:      revoker,
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
			// The same blob store the channel images use. What it holds here is ciphertext
			// the server cannot open — the key is inside the encrypted message.
			Blobs: blobs,
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
			// Terminating a device revokes its login; the deny entry is kept for a refresh
			// token's lifetime, past which the token expires on its own.
			Revoker:    revoker,
			SessionTTL: cfg.RefreshTokenTTL,
		},
		VAPIDPublicKey: cfg.VAPIDPublicKey,
	}).Routes(mux)

	// An empty allowlist is legal — a deployment whose SPA is same-origin with the
	// API needs no CORS at all — but it is far more often a stack.env that predates
	// PHEME_CORS_ORIGINS. Left unsaid, the symptom is every browser request failing
	// with an opaque CORS error and nothing in the server log to explain it.
	if len(cfg.CORSOrigins) == 0 {
		logger.Warn("no PHEME_CORS_ORIGINS configured: browsers on any other origin will be refused")
	}

	srv := &http.Server{
		Addr:              cfg.AppAddr,
		Handler:           withCORS(cfg.CORSOrigins, mux),
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

// withCORS allows the configured web origins to call the API from a browser.
//
// It echoes the request's Origin only when it is on the allowlist; anything else
// gets no CORS headers at all. The previous blanket "*" let any page on the
// internet call the API, and told an unauthenticated prober that this host serves
// a browser API for some other origin — which plain static hosting never does.
func withCORS(origins []string, next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		allowed[o] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.ToLower(strings.TrimSpace(r.Header.Get("Origin")))
		if _, ok := allowed[origin]; ok && origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			// The response body is the same either way, but the headers are not, so
			// a shared cache must key on Origin or it will serve one origin's
			// allowance to another.
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
