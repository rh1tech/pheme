package federation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// UserResolver answers questions a peer may ask about this host's users: whether
// a local id exists, and — so a person on another host can be addressed as
// `username@thishost` rather than by an opaque id — what local id a username maps
// to. It stays deliberately small; both answers reveal only what addressing a
// user for a conversation genuinely needs.
type UserResolver interface {
	UserExists(ctx context.Context, localID string) bool
	// ResolveUsername maps a (case-insensitive) username to its local id, display
	// name, and canonical username. ok is false when no such user exists here.
	ResolveUsername(ctx context.Context, usernameLower string) (id, displayName, username string, ok bool)
}

// Handler serves the host-to-host API. Every route except the public directory
// requires a valid nodelist-anchored signature.
type Handler struct {
	Origin        string    // this host's own domain
	Lookup        keyLookup // the nodelist
	Users         UserResolver
	Channels      ChannelService      // optional; channel routes mount only when set
	KeyPackages   KeyPackageService   // optional; key-package routes mount only when set
	Conversations ConversationService // optional; cross-host chat routes mount only when set
	now           func() time.Time
}

// NewHandler builds the federation handler.
func NewHandler(origin string, lookup keyLookup, users UserResolver) *Handler {
	return &Handler{Origin: origin, Lookup: lookup, Users: users, now: time.Now}
}

// WithChannels enables the cross-host channel routes.
func (h *Handler) WithChannels(c ChannelService) *Handler {
	h.Channels = c
	return h
}

// WithKeyPackages enables the cross-host key-package claim route.
func (h *Handler) WithKeyPackages(k KeyPackageService) *Handler {
	h.KeyPackages = k
	return h
}

// WithConversations enables the cross-host encrypted-chat routes.
func (h *Handler) WithConversations(c ConversationService) *Handler {
	h.Conversations = c
	return h
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
	mux.Handle("POST /federation/v1/resolve-user", h.verified(http.HandlerFunc(h.resolveUser)))

	if h.Channels != nil {
		h.registerChannels(mux)
	}
	if h.KeyPackages != nil {
		h.registerKeyPackages(mux)
	}
	if h.Conversations != nil {
		h.registerConversations(mux)
	}
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
	endpoints := map[string]string{
		"liveness":    "/federation/v1/liveness",
		"userExists":  "/federation/v1/user-exists",
		"resolveUser": "/federation/v1/resolve-user",
	}
	if h.Channels != nil {
		endpoints["channelSubscribe"] = "/federation/v1/channel-subscribe"
		endpoints["channelDelivery"] = "/federation/v1/channel-delivery"
	}
	if h.KeyPackages != nil {
		endpoints["claimKeyPackages"] = "/federation/v1/claim-key-packages"
	}
	if h.Conversations != nil {
		endpoints["conversationRelay"] = "/federation/v1/conversation-relay"
		endpoints["conversationSubmitMessage"] = "/federation/v1/conversation-submit-message"
		endpoints["conversationSubmitCommit"] = "/federation/v1/conversation-submit-commit"
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"origin":    h.Origin,
		"endpoints": endpoints,
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

// resolveUser maps one of this host's usernames to its local id, so a peer whose
// user typed `username@thishost` can turn that into an id it can add to a
// conversation. It returns the id and display name for a match and 404 for none —
// the same information a peer would get by knowing the id already, no more.
func (h *Handler) resolveUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(verifiedBody(r), &req); err != nil || req.Username == "" {
		httpx.Error(w, http.StatusBadRequest, "username required")
		return
	}
	id, displayName, username, ok := h.Users.ResolveUsername(r.Context(), strings.ToLower(strings.TrimSpace(req.Username)))
	if !ok {
		httpx.Error(w, http.StatusNotFound, "no such user")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"userId":      id,
		"displayName": displayName,
		"username":    username,
	})
}
