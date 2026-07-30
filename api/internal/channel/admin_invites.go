package channel

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/invite"
	"github.com/rh1tech/pheme/api/internal/store"
)

// maxInviteNote bounds the free-text label on an invite. Long enough for "Anna, from the
// Tuesday meeting", short enough that the field is not a place to store documents.
const maxInviteNote = 200

// maxInviteTTLDays is the longest expiry an admin may set. An invitation that outlives the
// memory of who it was for is indistinguishable from an open registration endpoint.
const maxInviteTTLDays = 365

// inviteSummary is an invite as the admin panel sees it: the row, plus the one word that
// says what became of it, plus — ONLY in the response to the request that created it — the
// code itself. Nothing stored can reproduce that code, which is the point.
type inviteSummary struct {
	domain.Invite
	Status domain.InviteStatus `json:"status"`
	Code   string              `json:"code,omitempty"`
}

func summarizeInvite(i domain.Invite, now time.Time) inviteSummary {
	return inviteSummary{Invite: i, Status: i.Status(now)}
}

func (h *AdminHandler) listInvites(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	q, offset, limit, page := pageParams(r)
	invites, total, err := h.Store.AdminListInvites(r.Context(), q, offset, limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list invites")
		return
	}
	now := time.Now().UTC()
	out := make([]inviteSummary, 0, len(invites))
	for _, i := range invites {
		out = append(out, summarizeInvite(i, now))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"invites": out, "total": total, "page": page, "limit": limit})
}

type createInviteRequest struct {
	Note string `json:"note"`
	// ExpiresInDays is optional. Zero or absent means the invitation does not expire.
	ExpiresInDays int `json:"expiresInDays"`
}

// createInvite mints one invitation and returns it WITH its code.
//
// This is the only response that ever carries the code: the store keeps a hash, so an admin
// who loses the link has to issue a new one. That is a deliberate trade — a panel that could
// re-display invitations would turn any glance at a shared screen, or any stolen admin
// session, into a supply of accounts.
func (h *AdminHandler) createInvite(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	var req createInviteRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	note := strings.TrimSpace(req.Note)
	if len(note) > maxInviteNote {
		httpx.Error(w, http.StatusBadRequest, "note is too long")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > maxInviteTTLDays {
		httpx.Error(w, http.StatusBadRequest, "expiry must be between 0 and 365 days")
		return
	}

	code, err := invite.GenerateCode()
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not create invite")
		return
	}
	now := time.Now().UTC()
	createdBy, _ := auth.UserIDFromContext(r.Context())
	inv := domain.Invite{
		CodeHash:  invite.HashCode(code),
		Prefix:    invite.Prefix(code),
		Note:      note,
		CreatedBy: createdBy,
		CreatedAt: now,
	}
	if req.ExpiresInDays > 0 {
		at := now.AddDate(0, 0, req.ExpiresInDays)
		inv.ExpiresAt = &at
	}
	created, err := h.Store.CreateInvite(r.Context(), inv)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not create invite")
		return
	}
	out := summarizeInvite(created, now)
	out.Code = code
	httpx.JSON(w, http.StatusCreated, out)
}

// revokeInvite withdraws an unspent invitation. Revoking one that was already used is
// accepted and changes nothing — the account it created is a separate matter, ended by
// blocking the user, not by unpicking the invitation that let them in.
func (h *AdminHandler) revokeInvite(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	err := h.Store.RevokeInvite(r.Context(), id, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "invite not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not revoke invite")
		return
	}
	inv, err := h.Store.InviteByID(r.Context(), id)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked"})
		return
	}
	httpx.JSON(w, http.StatusOK, summarizeInvite(inv, time.Now().UTC()))
}
