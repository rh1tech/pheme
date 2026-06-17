package channel

import (
	"net/http"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/store"
)

// AdminHandler serves the admin/control-panel API. All endpoints require the
// caller to hold the admin role; they are mounted on the JWT-protected mux and
// each verifies the role from the request context.
type AdminHandler struct {
	Store store.Store
}

// Register adds the admin endpoints to an already JWT-protected mux.
func (h *AdminHandler) Register(protected *http.ServeMux) {
	protected.HandleFunc("GET /v1/admin/stats", h.stats)
	protected.HandleFunc("GET /v1/admin/users", h.listUsers)
	protected.HandleFunc("DELETE /v1/admin/users/{id}", h.deleteUser)
	protected.HandleFunc("GET /v1/admin/channels", h.listChannels)
	protected.HandleFunc("DELETE /v1/admin/channels/{id}", h.deleteChannel)
	protected.HandleFunc("GET /v1/admin/channels/{id}/keys", h.listKeys)
	protected.HandleFunc("DELETE /v1/admin/channels/{id}/keys/{keyId}", h.revokeKey)
}

func (h *AdminHandler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !auth.IsAdmin(r.Context()) {
		httpx.Error(w, http.StatusForbidden, "admin access required")
		return false
	}
	return true
}

func (h *AdminHandler) stats(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	stats, err := h.Store.AdminStats(r.Context(), 5, 10)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load stats")
		return
	}
	httpx.JSON(w, http.StatusOK, stats)
}

// userSummary is a user enriched with the number of channels they own.
type userSummary struct {
	domain.User
	ChannelCount int `json:"channelCount"`
}

func (h *AdminHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list users")
		return
	}
	channels, err := h.Store.ListAllChannels(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list users")
		return
	}
	counts := map[string]int{}
	for _, c := range channels {
		counts[c.OwnerID]++
	}
	out := make([]userSummary, 0, len(users))
	for _, u := range users {
		out = append(out, userSummary{User: u, ChannelCount: counts[u.ID]})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": out})
}

func (h *AdminHandler) deleteUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	targetID := r.PathValue("id")
	if uid, _ := auth.UserIDFromContext(r.Context()); uid == targetID {
		httpx.Error(w, http.StatusBadRequest, "cannot delete your own account")
		return
	}
	if err := h.Store.DeleteUser(r.Context(), targetID); err != nil {
		if err == store.ErrNotFound {
			httpx.Error(w, http.StatusNotFound, "user not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "could not delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// channelSummary is a channel enriched with its owner's email.
type channelSummary struct {
	domain.Channel
	OwnerEmail string `json:"ownerEmail"`
}

func (h *AdminHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	channels, err := h.Store.ListAllChannels(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list channels")
		return
	}
	users, err := h.Store.ListUsers(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list channels")
		return
	}
	emails := map[string]string{}
	for _, u := range users {
		emails[u.ID] = u.Email
	}
	out := make([]channelSummary, 0, len(channels))
	for _, c := range channels {
		out = append(out, channelSummary{Channel: c, OwnerEmail: emails[c.OwnerID]})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (h *AdminHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if err := h.Store.DeleteChannel(r.Context(), r.PathValue("id")); err != nil {
		if err == store.ErrNotFound {
			httpx.Error(w, http.StatusNotFound, "channel not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "could not delete channel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) listKeys(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	keys, err := h.Store.APIKeysByChannel(r.Context(), r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list keys")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (h *AdminHandler) revokeKey(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if err := h.Store.RevokeAPIKey(r.Context(), r.PathValue("keyId")); err != nil {
		if err == store.ErrNotFound {
			httpx.Error(w, http.StatusNotFound, "key not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "could not revoke key")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked", "id": r.PathValue("keyId")})
}
