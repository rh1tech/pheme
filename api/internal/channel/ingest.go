package channel

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/ratelimit"
	"github.com/rh1tech/pheme/api/internal/store"
)

// IngestHandler serves the public trigger endpoint authenticated by API key.
type IngestHandler struct {
	Store     store.Store
	Publisher broker.Publisher
	Limiter   ratelimit.Limiter
	Blob      blob.Store
}

// Routes registers the ingest endpoints on a mux.
func (h *IngestHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Health("ingest"))
	mux.HandleFunc("POST /v1/ingest/{channelId}/notify", h.notify)
}

func (h *IngestHandler) notify(w http.ResponseWriter, r *http.Request) {
	publicID := r.PathValue("channelId")

	apiKey := strings.TrimSpace(r.Header.Get("X-Api-Key"))
	if apiKey == "" {
		httpx.Error(w, http.StatusUnauthorized, "missing X-Api-Key")
		return
	}

	ch, err := h.Store.ChannelByPublicID(r.Context(), publicID)
	if err != nil {
		// Avoid leaking whether the channel exists.
		httpx.Error(w, http.StatusUnauthorized, "invalid channel or key")
		return
	}

	if !h.validKey(r, ch.ID, apiKey) {
		httpx.Error(w, http.StatusUnauthorized, "invalid channel or key")
		return
	}

	if ch.Status == domain.ChannelDisabled {
		httpx.Error(w, http.StatusForbidden, "channel is disabled")
		return
	}

	if h.Limiter != nil && !h.Limiter.Allow(ch.ID) {
		httpx.Error(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	in, ok := decodeNotify(w, r, h.Blob)
	if !ok {
		return
	}
	if strings.TrimSpace(in.Title) == "" && strings.TrimSpace(in.Body) == "" && len(in.Images) == 0 {
		httpx.Error(w, http.StatusBadRequest, "title, body or an image is required")
		return
	}

	task := domain.NotifyTask{
		ChannelID:      ch.ID,
		Title:          in.Title,
		Body:           in.Body,
		Images:         in.Images,
		Data:           in.Data,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key")),
		EnqueuedAt:     time.Now().UTC(),
	}
	if err := h.Publisher.Publish(r.Context(), task); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "could not enqueue notification")
		return
	}

	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (h *IngestHandler) validKey(r *http.Request, channelID, presented string) bool {
	keys, err := h.Store.APIKeysByChannel(r.Context(), channelID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false
	}
	for _, k := range keys {
		if k.RevokedAt != nil {
			continue
		}
		if auth.EqualAPIKey(presented, k.HashedKey) {
			return true
		}
	}
	return false
}
