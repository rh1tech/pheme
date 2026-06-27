package channel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/broker"
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
	protected.HandleFunc("POST /v1/channels", h.createChannel)
	protected.HandleFunc("GET /v1/channels", h.listChannels)
	protected.HandleFunc("PATCH /v1/channels/{id}", h.updateChannel)
	protected.HandleFunc("DELETE /v1/channels/{id}", h.deleteChannel)
	protected.HandleFunc("POST /v1/channels/{id}/keys", h.createKey)
	protected.HandleFunc("GET /v1/channels/{id}/keys", h.listKeys)
	protected.HandleFunc("DELETE /v1/channels/{id}/keys/{keyId}", h.revokeKey)
	protected.HandleFunc("POST /v1/channels/{id}/notify", h.notifyChannel)
	protected.HandleFunc("POST /v1/devices", h.createDevice)
	protected.HandleFunc("POST /v1/channels/{id}/subscribe", h.subscribe)
	protected.HandleFunc("DELETE /v1/channels/{id}/subscribe", h.unsubscribe)
	protected.HandleFunc("GET /v1/channels/{id}/subscription", h.subscriptionStatus)
	protected.HandleFunc("GET /v1/channels/{id}/messages", h.listMessages)

	if h.Admin != nil {
		h.Admin.Register(protected)
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
	httpx.JSON(w, http.StatusOK, map[string]any{"channels": channels})
}

type updateChannelRequest struct {
	Name             string                  `json:"name"`
	SubscriptionMode domain.SubscriptionMode `json:"subscriptionMode"`
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
	ch, err := h.channelByID(r, channelID)
	if err != nil || ch.OwnerID != uid {
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
		ChannelID:  channelID,
		Title:      in.Title,
		Body:       in.Body,
		Images:     in.Images,
		Data:       in.Data,
		EnqueuedAt: time.Now().UTC(),
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
		UserID:     uid,
		Platform:   req.Platform,
		FCMToken:   req.FCMToken,
		WebPushSub: req.WebPushSub,
		CreatedAt:  time.Now().UTC(),
		LastSeenAt: time.Now().UTC(),
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
	_ = uid
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
	// Open channels activate immediately; approval channels start pending.
	status := domain.SubActive
	if ch.SubscriptionMode == domain.ModeApproval && ch.OwnerID != uid {
		status = domain.SubPending
	}
	sub, err := h.Store.Subscribe(r.Context(), domain.Subscription{
		ChannelID: channelID,
		DeviceID:  req.DeviceID,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not subscribe")
		return
	}
	httpx.JSON(w, http.StatusCreated, sub)
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

func (h *AppHandler) listMessages(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	channelID := r.PathValue("id")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	cursor := r.URL.Query().Get("cursor")
	query := r.URL.Query().Get("q")
	msgs, err := h.Store.MessagesByChannel(r.Context(), channelID, cursor, query, limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load messages")
		return
	}
	var next string
	if len(msgs) == limit {
		next = msgs[len(msgs)-1].ID
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"messages": msgs, "nextCursor": next})
}

// stream is a Server-Sent Events endpoint delivering live messages. It accepts
// the access token via the "token" query parameter because EventSource cannot
// set an Authorization header. Production may expose this as a WebSocket and fan
// out via Redis pub/sub.
func (h *AppHandler) stream(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Tokens.Parse(r.URL.Query().Get("token"), auth.AccessToken); err != nil {
		httpx.Error(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.Error(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	events, cancel := h.Live.Subscribe()
	defer cancel()

	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
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
