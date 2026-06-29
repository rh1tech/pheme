package channel

import (
	"errors"
	"net/http"
	"strconv"
	"time"

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
	protected.HandleFunc("POST /v1/admin/users", h.createUser)
	protected.HandleFunc("PATCH /v1/admin/users/{id}", h.updateUser)
	protected.HandleFunc("POST /v1/admin/users/{id}/reset-password", h.resetUserPassword)
	protected.HandleFunc("DELETE /v1/admin/users/{id}", h.deleteUser)
	protected.HandleFunc("GET /v1/admin/channels", h.listChannels)
	protected.HandleFunc("PATCH /v1/admin/channels/{id}", h.updateChannel)
	protected.HandleFunc("DELETE /v1/admin/channels/{id}", h.deleteChannel)
	protected.HandleFunc("GET /v1/admin/channels/{id}/messages", h.channelMessages)
	protected.HandleFunc("GET /v1/admin/channels/{id}/keys", h.listKeys)
	protected.HandleFunc("DELETE /v1/admin/channels/{id}/keys/{keyId}", h.revokeKey)
	protected.HandleFunc("GET /v1/admin/comments", h.listComments)
	protected.HandleFunc("DELETE /v1/admin/comments/{id}", h.deleteComment)
}

func (h *AdminHandler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !auth.IsAdmin(r.Context()) {
		httpx.Error(w, http.StatusForbidden, "admin access required")
		return false
	}
	return true
}

// pageParams reads q, page (1-based) and limit query parameters with defaults.
func pageParams(r *http.Request) (q string, offset, limit, page int) {
	q = r.URL.Query().Get("q")
	page = 1
	if v, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && v > 0 {
		page = v
	}
	limit = 20
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = v
	}
	offset = (page - 1) * limit
	return q, offset, limit, page
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
	q, offset, limit, page := pageParams(r)
	users, total, err := h.Store.AdminListUsers(r.Context(), q, offset, limit)
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
		if u.Status == "" {
			u.Status = domain.UserActive
		}
		out = append(out, userSummary{User: u, ChannelCount: counts[u.ID]})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"users": out, "total": total, "page": page, "limit": limit})
}

type createUserRequest struct {
	Email    string      `json:"email"`
	Password string      `json:"password"`
	Role     domain.Role `json:"role"`
}

// createUser lets an admin add a user directly, bypassing the email-verification
// flow. The account is created active with the requested role (defaulting to
// "user"). The email must be valid and unused; the password must satisfy the
// same strength policy as self-service registration.
func (h *AdminHandler) createUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req createUserRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	email, ok := normalizeEmail(req.Email)
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "a valid email is required")
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	role := req.Role
	if role == "" {
		role = domain.RoleUser
	}
	if role != domain.RoleUser && role != domain.RoleAdmin {
		httpx.Error(w, http.StatusBadRequest, "invalid role")
		return
	}
	if _, err := h.Store.UserByEmail(r.Context(), email); err == nil {
		httpx.Error(w, http.StatusConflict, "email already registered")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusInternalServerError, "could not create user")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not create user")
		return
	}
	u, err := h.Store.CreateUser(r.Context(), domain.User{
		Email:        email,
		PasswordHash: hash,
		Role:         role,
		Status:       domain.UserActive,
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not create user")
		return
	}
	httpx.JSON(w, http.StatusCreated, userSummary{User: u, ChannelCount: 0})
}

type updateUserRequest struct {
	Role   *domain.Role       `json:"role,omitempty"`
	Status *domain.UserStatus `json:"status,omitempty"`
}

func (h *AdminHandler) updateUser(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	targetID := r.PathValue("id")
	if uid, _ := auth.UserIDFromContext(r.Context()); uid == targetID {
		httpx.Error(w, http.StatusBadRequest, "cannot change your own role or status")
		return
	}
	var req updateUserRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Role != nil {
		if *req.Role != domain.RoleUser && *req.Role != domain.RoleAdmin {
			httpx.Error(w, http.StatusBadRequest, "invalid role")
			return
		}
		if err := h.Store.UpdateUserRole(r.Context(), targetID, *req.Role); err != nil {
			h.writeStoreErr(w, err, "could not update role")
			return
		}
	}
	if req.Status != nil {
		if *req.Status != domain.UserActive && *req.Status != domain.UserBlocked {
			httpx.Error(w, http.StatusBadRequest, "invalid status")
			return
		}
		if err := h.Store.UpdateUserStatus(r.Context(), targetID, *req.Status); err != nil {
			h.writeStoreErr(w, err, "could not update status")
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

type resetUserPasswordRequest struct {
	NewPassword string `json:"newPassword"`
}

// resetUserPassword lets an admin set a new password for any user directly. The
// new password must satisfy the same strength policy as self-service flows.
func (h *AdminHandler) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req resetUserPasswordRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		httpx.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not reset password")
		return
	}
	if err := h.Store.UpdateUserPassword(r.Context(), r.PathValue("id"), hash); err != nil {
		h.writeStoreErr(w, err, "could not reset password")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
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
		h.writeStoreErr(w, err, "could not delete user")
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
	q, offset, limit, page := pageParams(r)
	channels, total, err := h.Store.AdminListChannels(r.Context(), q, offset, limit)
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
		if c.Status == "" {
			c.Status = domain.ChannelActive
		}
		out = append(out, channelSummary{Channel: c, OwnerEmail: emails[c.OwnerID]})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"channels": out, "total": total, "page": page, "limit": limit})
}

type updateChannelStatusRequest struct {
	Status domain.ChannelStatus `json:"status"`
}

func (h *AdminHandler) updateChannel(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req updateChannelStatusRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Status != domain.ChannelActive && req.Status != domain.ChannelDisabled {
		httpx.Error(w, http.StatusBadRequest, "invalid status")
		return
	}
	ch, err := h.Store.UpdateChannelStatus(r.Context(), r.PathValue("id"), req.Status)
	if err != nil {
		h.writeStoreErr(w, err, "could not update channel")
		return
	}
	httpx.JSON(w, http.StatusOK, ch)
}

func (h *AdminHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if err := h.Store.DeleteChannel(r.Context(), r.PathValue("id")); err != nil {
		h.writeStoreErr(w, err, "could not delete channel")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) channelMessages(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	channelID := r.PathValue("id")
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	msgs, err := h.Store.MessagesByChannel(r.Context(), channelID, r.URL.Query().Get("cursor"), r.URL.Query().Get("q"), limit)
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
		h.writeStoreErr(w, err, "could not revoke key")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked", "id": r.PathValue("keyId")})
}

func (h *AdminHandler) writeStoreErr(w http.ResponseWriter, err error, msg string) {
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}
	httpx.Error(w, http.StatusInternalServerError, msg)
}
