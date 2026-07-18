package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/chat"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/store"
)

// AppHandler serves the authenticated user-facing API. Identity comes from the
// JWT access token validated by the auth middleware.
type AppHandler struct {
	Store          store.Store
	Live           live.Bus
	Tokens         *auth.TokenManager
	Publisher      broker.Publisher
	Blob           blob.Store
	Admin          *AdminHandler
	Chat           *chat.Handler
	VAPIDPublicKey string
}

// Routes registers the App API endpoints on a mux. Protected endpoints are
// wrapped with JWT middleware; health is public and the SSE stream authenticates
// via a token query parameter (EventSource cannot set headers).
func (h *AppHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Health("app"))
	mux.HandleFunc("GET /v1/meta", h.meta)
	mux.HandleFunc("GET /v1/stream", h.stream)
	// Public so devices and <img>/notification fetches need no bearer token; the
	// blob id is unguessable. This mirrors message history, already readable by
	// any authenticated user.
	mux.HandleFunc("GET /v1/images/{id}", h.serveImage)

	protected := http.NewServeMux()
	// Profile (self).
	protected.HandleFunc("GET /v1/me", h.me)
	protected.HandleFunc("PATCH /v1/me", h.updateProfile)
	protected.HandleFunc("POST /v1/me/avatar", h.uploadAvatar)
	protected.HandleFunc("DELETE /v1/me/avatar", h.deleteAvatar)
	protected.HandleFunc("POST /v1/channels", h.createChannel)
	protected.HandleFunc("GET /v1/channels", h.listChannels)
	// Literal segments; Go 1.22 mux prefers them over the {id} wildcard.
	protected.HandleFunc("POST /v1/channels/join", h.joinChannel)
	protected.HandleFunc("GET /v1/channels/joined", h.listJoinedChannels)
	protected.HandleFunc("GET /v1/channels/{id}", h.getChannel)
	protected.HandleFunc("PATCH /v1/channels/{id}", h.updateChannel)
	protected.HandleFunc("DELETE /v1/channels/{id}", h.deleteChannel)
	protected.HandleFunc("DELETE /v1/channels/{id}/messages/{messageId}", h.deleteMessage)
	protected.HandleFunc("POST /v1/channels/{id}/avatar", h.uploadChannelAvatar)
	protected.HandleFunc("DELETE /v1/channels/{id}/avatar", h.deleteChannelAvatar)
	protected.HandleFunc("POST /v1/channels/{id}/keys", h.createKey)
	protected.HandleFunc("GET /v1/channels/{id}/keys", h.listKeys)
	protected.HandleFunc("DELETE /v1/channels/{id}/keys/{keyId}", h.revokeKey)
	protected.HandleFunc("POST /v1/channels/{id}/notify", h.notifyChannel)
	protected.HandleFunc("POST /v1/devices", h.createDevice)
	protected.HandleFunc("POST /v1/channels/{id}/subscribe", h.subscribe)
	protected.HandleFunc("DELETE /v1/channels/{id}/subscribe", h.unsubscribe)
	protected.HandleFunc("GET /v1/channels/{id}/subscription", h.subscriptionStatus)
	// Membership (per-user), approvals queue, and subscriber management.
	protected.HandleFunc("GET /v1/channels/{id}/membership", h.membership)
	protected.HandleFunc("DELETE /v1/channels/{id}/membership", h.leaveChannel)
	protected.HandleFunc("GET /v1/channels/{id}/approvals", h.listApprovals)
	protected.HandleFunc("POST /v1/channels/{id}/approvals/{userId}", h.approveMember)
	protected.HandleFunc("DELETE /v1/channels/{id}/approvals/{userId}", h.denyMember)
	protected.HandleFunc("GET /v1/channels/{id}/members", h.listMembers)
	protected.HandleFunc("PATCH /v1/channels/{id}/members/{userId}", h.updateMember)
	protected.HandleFunc("DELETE /v1/channels/{id}/members/{userId}", h.removeMember)
	protected.HandleFunc("GET /v1/channels/{id}/messages", h.listMessages)
	protected.HandleFunc("GET /v1/channels/{id}/messages/{messageId}", h.getMessage)
	// Comments on a message.
	protected.HandleFunc("GET /v1/channels/{id}/messages/{messageId}/comments", h.listComments)
	protected.HandleFunc("POST /v1/channels/{id}/messages/{messageId}/comments", h.postComment)
	protected.HandleFunc("DELETE /v1/channels/{id}/messages/{messageId}/comments/{commentId}", h.deleteComment)

	if h.Admin != nil {
		h.Admin.Register(protected)
	}
	if h.Chat != nil {
		h.Chat.Register(protected)
	}

	mux.Handle("/v1/", h.Tokens.Middleware(protected))
}

// meta exposes public client configuration, such as the Web Push VAPID public
// key, so the web client always matches the server's keys.
func (h *AppHandler) meta(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"vapidPublicKey": h.VAPIDPublicKey,
	})
}

// requireUser resolves the calling user from the JWT context.
func (h *AppHandler) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	uid, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return "", false
	}
	return uid, true
}

type createChannelRequest struct {
	Name             string                  `json:"name"`
	PublicID         string                  `json:"publicId"`
	SubscriptionMode domain.SubscriptionMode `json:"subscriptionMode"`
}

func (h *AppHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req createChannelRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "name is required")
		return
	}
	mode := req.SubscriptionMode
	if mode != domain.ModeOpen && mode != domain.ModeApproval {
		mode = domain.ModeApproval
	}
	publicID := req.PublicID
	if publicID == "" {
		publicID = "ch_" + auth.HashAPIKey(req.Name + time.Now().String())[:16]
	}
	ch, err := h.Store.CreateChannel(r.Context(), domain.Channel{
		PublicID:         publicID,
		OwnerID:          uid,
		Name:             req.Name,
		SubscriptionMode: mode,
		Status:           domain.ChannelActive,
		CreatedAt:        time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not create channel")
		return
	}
	httpx.JSON(w, http.StatusCreated, ch)
}

func (h *AppHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channels, err := h.Store.ChannelsByOwner(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list channels")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"channels": h.withLastMessages(r.Context(), channels)})
}

type updateChannelRequest struct {
	Name             string                  `json:"name"`
	SubscriptionMode domain.SubscriptionMode `json:"subscriptionMode"`
	// Alias is the phetag. A nil pointer leaves it unchanged; an empty string
	// clears it; a non-empty string sets it (owner-only, validated, unique).
	Alias *string `json:"alias,omitempty"`
}

func (h *AppHandler) updateChannel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.ownsChannel(r, uid, channelID) {
		httpx.Error(w, http.StatusForbidden, "not your channel")
		return
	}
	var req updateChannelRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.SubscriptionMode != "" &&
		req.SubscriptionMode != domain.ModeOpen && req.SubscriptionMode != domain.ModeApproval {
		httpx.Error(w, http.StatusBadRequest, "invalid subscriptionMode")
		return
	}
	ch, err := h.Store.UpdateChannel(r.Context(), channelID, req.Name, req.SubscriptionMode)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not update channel")
		return
	}
	if req.Alias != nil {
		alias := strings.TrimSpace(*req.Alias)
		if alias != "" {
			if verr := domain.ValidateAlias(alias); verr != nil {
				httpx.Error(w, http.StatusBadRequest, verr.Error())
				return
			}
		}
		ch, err = h.Store.SetChannelAlias(r.Context(), channelID, alias)
		if err != nil {
			if err == store.ErrAliasTaken {
				httpx.Error(w, http.StatusConflict, "that phetag is already taken")
				return
			}
			httpx.Error(w, http.StatusInternalServerError, "could not update phetag")
			return
		}
	}
	httpx.JSON(w, http.StatusOK, ch)
}

func (h *AppHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.ownsChannel(r, uid, channelID) {
		httpx.Error(w, http.StatusForbidden, "not your channel")
		return
	}
	if err := h.Store.DeleteChannel(r.Context(), channelID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not delete channel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AppHandler) createKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.ownsChannel(r, uid, channelID) {
		httpx.Error(w, http.StatusForbidden, "not your channel")
		return
	}
	plaintext, hash, prefix := auth.GenerateAPIKey()
	k, err := h.Store.CreateAPIKey(r.Context(), domain.APIKey{
		ChannelID: channelID,
		HashedKey: hash,
		Prefix:    prefix,
		Label:     r.URL.Query().Get("label"),
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not create key")
		return
	}
	// The plaintext key is returned exactly once.
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"id":     k.ID,
		"prefix": k.Prefix,
		"key":    plaintext,
		"note":   "store this key now; it will not be shown again",
	})
}

func (h *AppHandler) listKeys(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.ownsChannel(r, uid, channelID) {
		httpx.Error(w, http.StatusForbidden, "not your channel")
		return
	}
	keys, err := h.Store.APIKeysByChannel(r.Context(), channelID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list keys")
		return
	}
	// APIKey.HashedKey is json:"-", so the secret hash is never serialised.
	httpx.JSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (h *AppHandler) revokeKey(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	keyID := r.PathValue("keyId")
	if !h.ownsChannel(r, uid, channelID) {
		httpx.Error(w, http.StatusForbidden, "not your channel")
		return
	}
	// Ensure the key belongs to this channel before revoking.
	keys, err := h.Store.APIKeysByChannel(r.Context(), channelID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load keys")
		return
	}
	found := false
	for _, k := range keys {
		if k.ID == keyID {
			found = true
			break
		}
	}
	if !found {
		httpx.Error(w, http.StatusNotFound, "key not found")
		return
	}
	if err := h.Store.RevokeAPIKey(r.Context(), keyID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not revoke key")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked", "id": keyID})
}

// notifyChannel lets a channel owner send a message from the authenticated UI,
// without an API key. It accepts JSON (text only) or multipart/form-data (text +
// images), then enqueues the same NotifyTask the public ingest endpoint produces,
// so delivery follows the identical pipeline.
func (h *AppHandler) notifyChannel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	ch, ok := h.canAdminister(r, uid, channelID)
	if !ok {
		httpx.Error(w, http.StatusForbidden, "not your channel")
		return
	}
	if ch.Status == domain.ChannelDisabled {
		httpx.Error(w, http.StatusForbidden, "channel is disabled")
		return
	}
	in, ok := decodeNotify(w, r, h.Blob)
	if !ok {
		return
	}
	if in.Title == "" && in.Body == "" && len(in.Images) == 0 {
		httpx.Error(w, http.StatusBadRequest, "title, body or an image is required")
		return
	}
	if h.Publisher == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "sending is not enabled")
		return
	}
	task := domain.NotifyTask{
		ChannelID:       channelID,
		Title:           in.Title,
		Body:            in.Body,
		Images:          in.Images,
		Data:            in.Data,
		CommentsAllowed: in.CommentsAllowed,
		EnqueuedAt:      time.Now().UTC(),
	}
	if err := h.Publisher.Publish(r.Context(), task); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "could not enqueue message")
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// serveImage streams a processed image blob by id. Public by design (see Routes).
func (h *AppHandler) serveImage(w http.ResponseWriter, r *http.Request) {
	if h.Blob == nil {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}
	data, contentType, err := h.Blob.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}
	httpx.Binary(w, contentType, data)
}

type createDeviceRequest struct {
	Platform   domain.Platform `json:"platform"`
	FCMToken   string          `json:"fcmToken,omitempty"`
	WebPushSub string          `json:"webPushSub,omitempty"`
	// VoIPToken is the iOS PushKit token. A separate token from FCMToken, and both are sent: the FCM
	// one carries messages, and this one carries calls, because only it can reach PushKit.
	VoIPToken string `json:"voipToken,omitempty"`
	// CanRenderPreview is the client declaring that this build can decrypt a message and draw the
	// notification itself. Absent from every older client, which is the answer. See
	// domain.Device.CanRenderPreview for why guessing it is not survivable.
	CanRenderPreview bool `json:"canRenderPreview,omitempty"`
}

func (h *AppHandler) createDevice(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req createDeviceRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	d, err := h.Store.CreateDevice(r.Context(), domain.Device{
		UserID:           uid,
		Platform:         req.Platform,
		FCMToken:         req.FCMToken,
		WebPushSub:       req.WebPushSub,
		VoIPToken:        req.VoIPToken,
		CanRenderPreview: req.CanRenderPreview,
		CreatedAt:        time.Now().UTC(),
		LastSeenAt:       time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not register device")
		return
	}
	httpx.JSON(w, http.StatusCreated, d)
}

type subscribeRequest struct {
	DeviceID string `json:"deviceId"`
}

func (h *AppHandler) subscribe(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	var req subscribeRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.DeviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}
	ch, err := h.channelByID(r, channelID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "channel not found")
		return
	}
	// Subscribing also establishes the caller's per-user membership (open →
	// active, approval → pending), and the device subscription inherits the
	// resulting status so push delivery stays consistent with approval state.
	mem, err := h.join(r.Context(), uid, ch, req.DeviceID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not subscribe")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"status": mem.Status, "membership": mem})
}

func (h *AppHandler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	channelID := r.PathValue("id")
	deviceID := r.URL.Query().Get("deviceId")
	if deviceID == "" {
		httpx.Error(w, http.StatusBadRequest, "deviceId is required")
		return
	}
	if err := h.Store.Unsubscribe(r.Context(), channelID, deviceID); err != nil && err != store.ErrNotFound {
		httpx.Error(w, http.StatusInternalServerError, "could not unsubscribe")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// subscriptionStatus reports whether a given device is subscribed to the
// channel: status is "active", "pending" or "none".
func (h *AppHandler) subscriptionStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	channelID := r.PathValue("id")
	deviceID := r.URL.Query().Get("deviceId")
	if deviceID == "" {
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "none"})
		return
	}
	sub, err := h.Store.SubscriptionForDevice(r.Context(), channelID, deviceID)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "none"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": sub.Status})
}

func contains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// canReadChannel reports whether uid may read a channel's content: the owner, or
// a member whose membership is active. Pending, blocked, and non-members are
// denied, as is a missing channel. It is evaluated against live store state on
// every call, so a block (or removal) takes effect on the next request or live
// event — there is no cached grant to outlive the change.
func (h *AppHandler) canReadChannel(ctx context.Context, uid, channelID string) bool {
	ch, err := h.Store.ChannelByID(ctx, channelID)
	if err != nil {
		return false
	}
	rel, ok := h.relationFor(ctx, uid, ch)
	return ok && rel.Status == domain.MemberActive
}

// canReadMessages is the request-scoped form of canReadChannel for HTTP handlers.
// Callers respond 404 on false, so channel existence is never leaked to outsiders.
func (h *AppHandler) canReadMessages(r *http.Request, uid, channelID string) bool {
	return h.canReadChannel(r.Context(), uid, channelID)
}

func (h *AppHandler) listMessages(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.canReadMessages(r, uid, channelID) {
		httpx.Error(w, http.StatusNotFound, "channel not found")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	cursor := r.URL.Query().Get("cursor")
	query := r.URL.Query().Get("q")

	// `around` centres the window on one message, so a search hit can be read in
	// the conversation it came from instead of on its own. It replaces the cursor
	// (there is nothing to page from) and the query (the window is context, not
	// more matches).
	var (
		msgs []domain.Message
		err  error
	)
	if around := r.URL.Query().Get("around"); around != "" {
		msgs, err = h.Store.MessagesAround(r.Context(), channelID, around, limit)
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "message not found")
			return
		}
	} else {
		msgs, err = h.Store.MessagesByChannel(r.Context(), channelID, cursor, query, limit)
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load messages")
		return
	}

	// The cursor always walks backwards in time, so it is the oldest message of
	// the window — which is exactly what "load older" needs, `around` or not.
	var next string
	if len(msgs) == limit {
		next = msgs[len(msgs)-1].ID
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"messages":   h.withCommentCounts(r.Context(), msgs),
		"nextCursor": next,
	})
}

// getMessage returns a single message by id (for the message-detail view and
// notification deep-links). Like listMessages, it is readable only by the owner
// or an active member; the message must belong to the channel in the path.
func (h *AppHandler) getMessage(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.canReadMessages(r, uid, channelID) {
		httpx.Error(w, http.StatusNotFound, "message not found")
		return
	}
	msg, err := h.Store.MessageByID(r.Context(), r.PathValue("messageId"))
	if err != nil || msg.ChannelID != channelID {
		httpx.Error(w, http.StatusNotFound, "message not found")
		return
	}
	httpx.JSON(w, http.StatusOK, h.withCommentCounts(r.Context(), []domain.Message{msg})[0])
}

// streamHeartbeat is how often an idle stream emits a comment line. Without it an
// idle connection is indistinguishable from a dead one: intermediaries (nginx,
// Cloudflare, a carrier NAT) drop a silent connection after their own timeout, and
// the client only finds out the next time it tries to receive an event — which for
// a ringing call is far too late. A comment costs a handful of bytes and keeps the
// path warm.
const streamHeartbeat = 25 * time.Second

// stream is a Server-Sent Events endpoint delivering live messages. It accepts
// the access token via the "token" query parameter because EventSource cannot
// set an Authorization header. Production may expose this as a WebSocket and fan
// out via Redis pub/sub.
//
// Each event is authorized per channel against live store state before it is
// forwarded, so a client only ever receives channels it is an active member of,
// and a block (or removal) silences an already-open connection on the next event
// rather than only on reconnect.
//
// The stream ends when the access token it was opened with expires. It has to: the
// token is checked once, at connect, so a stream that outlived its token would be a
// session that never ends — a signed-out user would keep receiving events until they
// closed the tab. Ending it is safe because the client reconnects with a fresh token
// (see useEventStream), and cheap because a reconnect is one request every 15 minutes.
func (h *AppHandler) stream(w http.ResponseWriter, r *http.Request) {
	claims, err := h.Tokens.ParseClaims(r.URL.Query().Get("token"), auth.AccessToken)
	if err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	uid := claims.Subject
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.Error(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Defeats nginx's proxy buffering, which would otherwise hold events back until
	// its buffer filled — turning a live stream into a batched one.
	w.Header().Set("X-Accel-Buffering", "no")

	events, cancel := h.Live.Subscribe()
	defer cancel()

	var expiry <-chan time.Time
	if exp := claims.ExpiresAt; exp != nil {
		t := time.NewTimer(time.Until(exp.Time))
		defer t.Stop()
		expiry = t.C
	}

	beat := time.NewTicker(streamHeartbeat)
	defer beat.Stop()

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-expiry:
			return
		case <-beat.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-events:
			if !ok {
				return
			}
			// Two event shapes on one stream. A conversation event is authorised
			// by live membership; a channel event by channel read access. Either
			// check re-runs per event, so a removal silences an open stream at once.
			if e.ConversationID != "" {
				// A deletion authorises against the captured member list: the
				// membership rows the usual check needs are already gone.
				if len(e.Recipients) > 0 {
					if !contains(e.Recipients, uid) {
						continue
					}
				} else if _, err := h.Store.ConversationMembership(r.Context(), e.ConversationID, uid); err != nil {
					continue // not a member of this conversation
				}
			} else if !h.canReadChannel(r.Context(), uid, e.ChannelID) {
				continue // not (or no longer) an active member of this channel
			}
			// The recipient list travels between processes (the Redis bus marshals the
			// event to JSON) but must not travel to the browser: it is an authorisation
			// list, and telling one subscriber who else was on it leaks the membership of
			// a conversation they have just been removed from. `e` is this loop's own
			// copy, so clearing it here affects nobody else's delivery.
			e.Recipients = nil
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", mustJSON(e))
			flusher.Flush()
		}
	}
}

func (h *AppHandler) ownsChannel(r *http.Request, uid, channelID string) bool {
	ch, err := h.channelByID(r, channelID)
	return err == nil && ch.OwnerID == uid
}

func (h *AppHandler) channelByID(r *http.Request, channelID string) (domain.Channel, error) {
	return h.Store.ChannelByID(r.Context(), channelID)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
