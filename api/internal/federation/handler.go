package federation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// UserResolver answers whether a local user exists, by local id. The federation
// handler needs almost nothing from the rest of the system yet; this keeps the
// coupling to exactly what the first endpoints use.
type UserResolver interface {
	UserExists(ctx context.Context, localID string) bool
}

// Handler serves the host-to-host API. Every route except the public directory
// requires a valid nodelist-anchored signature.
type Handler struct {
	Origin string    // this host's own domain
	Lookup keyLookup // the nodelist
	Users  UserResolver
	now    func() time.Time
}

// NewHandler builds the federation handler.
func NewHandler(origin string, lookup keyLookup, users UserResolver) *Handler {
	return &Handler{Origin: origin, Lookup: lookup, Users: users, now: time.Now}
}

// verifiedKey is the context key under which the proven caller is stored.
type verifiedKey struct{}

// Register mounts the federation routes on a mux.
func (h *Handler) Register(mux *http.ServeMux) {
	// Public, unsigned: how a peer discovers this host's endpoints and this
	// host's own signing key id. It reveals only what a peer needs to talk to
	// us, and it is the one thing that cannot require a signature — a caller
	// that does not yet know our endpoints cannot have signed a request to one.
	mux.HandleFunc("GET /.well-known/pheme-federation", h.directory)

	// Signed. The verifier wraps each so a handler only ever runs for a proven
	// peer, and can read who it is from the context.
	mux.Handle("GET /federation/v1/liveness", h.verified(http.HandlerFunc(h.liveness)))
	mux.Handle("POST /federation/v1/user-exists", h.verified(http.HandlerFunc(h.userExists)))
}

// verified authenticates the request against the nodelist and, on success, runs
// next with the proven caller in context. On failure it answers 401 and next
// never runs — so no signed handler can forget to check.
func (h *Handler) verified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read the body once, here, with a hard cap. The signature binds the
		// body's hash, so the handler downstream must see the exact bytes that
		// were verified — it reads them from the context-stashed copy, not from
		// the now-drained r.Body.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "unreadable body")
			return
		}
		v, err := Verify(r, h.Lookup, body, h.now())
		if err != nil {
			// One opaque status for every failure — a stranger, a forger, a
			// stale request and a non-peer all learn the same thing.
			httpx.Error(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := context.WithValue(r.Context(), verifiedKey{}, v)
		ctx = context.WithValue(ctx, bodyKey{}, body)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type bodyKey struct{}

// caller returns the proven peer for a request inside a verified handler.
func caller(r *http.Request) Verified {
	v, _ := r.Context().Value(verifiedKey{}).(Verified)
	return v
}

// verifiedBody returns the exact bytes the signature covered.
func verifiedBody(r *http.Request) []byte {
	b, _ := r.Context().Value(bodyKey{}).([]byte)
	return b
}

// directory is the .well-known discovery document.
func (h *Handler) directory(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"origin": h.Origin,
		"endpoints": map[string]string{
			"liveness":   "/federation/v1/liveness",
			"userExists": "/federation/v1/user-exists",
		},
	})
}

// liveness proves this host is up and talking to a peer that the nodelist
// vouches for. The lightest possible real S2S call — it exercises the whole
// signing path end to end without touching any user data.
func (h *Handler) liveness(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"origin": h.Origin,
		"peer":   caller(r).Origin, // proven, echoed so the caller sees we authenticated it
	})
}

// userExists answers whether a local user is present, so a peer can resolve one
// of our users before starting a conversation. It returns only a boolean: a peer
// learns that an id is or is not a user here, and nothing else about them.
func (h *Handler) userExists(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(verifiedBody(r), &req); err != nil || req.UserID == "" {
		httpx.Error(w, http.StatusBadRequest, "userId required")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"exists": h.Users.UserExists(r.Context(), req.UserID),
	})
}
