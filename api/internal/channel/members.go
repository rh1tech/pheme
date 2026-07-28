package channel

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/store"
)

// canAdminister reports whether uid may moderate the channel — the owner, or an
// active member holding the per-channel admin role. The channel is returned so
// callers can reuse it. A missing channel yields (zero, false).
func (h *AppHandler) canAdminister(r *http.Request, uid, channelID string) (domain.Channel, bool) {
	ch, err := h.channelByID(r, channelID)
	if err != nil {
		return domain.Channel{}, false
	}
	if ch.OwnerID == uid {
		return ch, true
	}
	mem, err := h.Store.MembershipForUser(r.Context(), channelID, uid)
	if err == nil && mem.Status == domain.MemberActive && mem.Role == domain.RoleAdmin {
		return ch, true
	}
	return ch, false
}

// join creates (or returns) the caller's membership in ch and, when a deviceID is
// supplied, subscribes that device with a status that matches the membership.
// Open channels (and the owner) become active immediately; approval channels make
// non-owners pending. Re-joining preserves an existing membership's status.
func (h *AppHandler) join(ctx context.Context, uid string, ch domain.Channel, deviceID string) (domain.ChannelMember, error) {
	status := domain.MemberActive
	if ch.SubscriptionMode == domain.ModeApproval && ch.OwnerID != uid {
		status = domain.MemberPending
	}
	mem, err := h.Store.UpsertMember(ctx, domain.ChannelMember{
		ChannelID: ch.ID,
		UserID:    uid,
		Role:      domain.RoleUser,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return domain.ChannelMember{}, err
	}
	if deviceID != "" {
		if _, err := h.Store.Subscribe(ctx, domain.Subscription{
			ChannelID: ch.ID,
			DeviceID:  deviceID,
			Status:    subStatusFor(mem.Status),
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			return domain.ChannelMember{}, err
		}
	}
	return mem, nil
}

// subStatusFor maps a membership status to the device-subscription status that
// drives push delivery.
func subStatusFor(s domain.MemberStatus) domain.SubscriptionStatus {
	switch s {
	case domain.MemberActive:
		return domain.SubActive
	case domain.MemberBlocked:
		return domain.SubBlocked
	default:
		return domain.SubPending
	}
}

// resolveChannelRef looks up a channel by its public trigger ID first, then by
// its alias (phetag, case-insensitive). The "ch_" prefix is reserved for trigger
// IDs (see domain.ValidateAlias), so the two namespaces never overlap.
func (h *AppHandler) resolveChannelRef(ctx context.Context, ref string) (domain.Channel, error) {
	if ch, err := h.Store.ChannelByPublicID(ctx, ref); err == nil {
		return ch, nil
	}
	return h.Store.ChannelByAlias(ctx, strings.ToLower(ref))
}

type joinChannelRequest struct {
	Ref      string `json:"ref"`
	DeviceID string `json:"deviceId"`
}

// joinChannel adds the caller as a member of a channel referenced by its trigger
// ID or phetag. In approval mode the membership starts pending.
func (h *AppHandler) joinChannel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req joinChannelRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(req.Ref)
	if ref == "" {
		httpx.Error(w, http.StatusBadRequest, "a channel ID or phetag is required")
		return
	}
	ch, err := h.resolveChannelRef(r.Context(), ref)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "channel not found")
		return
	}
	if ch.Status == domain.ChannelDisabled {
		httpx.Error(w, http.StatusForbidden, "channel is disabled")
		return
	}
	mem, err := h.join(r.Context(), uid, ch, req.DeviceID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not join channel")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"channel": ch, "membership": mem})
}

// channelRelation is the caller's relationship to a channel.
type channelRelation struct {
	IsOwner bool                `json:"isOwner"`
	Role    domain.Role         `json:"role,omitempty"`
	Status  domain.MemberStatus `json:"status"`
}

// relationFor resolves how uid relates to ch: owner, member (with role/status),
// or none. ok is false only when the user is neither owner nor member.
func (h *AppHandler) relationFor(ctx context.Context, uid string, ch domain.Channel) (channelRelation, bool) {
	if ch.OwnerID == uid {
		return channelRelation{IsOwner: true, Role: domain.RoleAdmin, Status: domain.MemberActive}, true
	}
	mem, err := h.Store.MembershipForUser(ctx, ch.ID, uid)
	if err != nil {
		return channelRelation{Status: domain.MemberStatus("none")}, false
	}
	return channelRelation{Role: mem.Role, Status: mem.Status}, true
}

// getChannel returns a single channel the caller owns or is a member of, plus the
// caller's relationship to it. Non-members get 404 (existence is not leaked).
func (h *AppHandler) getChannel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	ch, err := h.channelByID(r, r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "channel not found")
		return
	}
	rel, ok := h.relationFor(r.Context(), uid, ch)
	if !ok {
		httpx.Error(w, http.StatusNotFound, "channel not found")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"channel": ch, "isOwner": rel.IsOwner, "role": rel.Role, "status": rel.Status,
	})
}

// membership reports the caller's role/status in a channel without 404ing for
// non-members (status "none"), mirroring subscriptionStatus.
func (h *AppHandler) membership(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	ch, err := h.channelByID(r, r.PathValue("id"))
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "channel not found")
		return
	}
	rel, _ := h.relationFor(r.Context(), uid, ch)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"isOwner": rel.IsOwner, "role": rel.Role, "status": rel.Status,
	})
}

// joinedChannel embeds a channel with the caller's membership role/status. The
// member status is exposed as "memberStatus" so it does not collide with the
// channel's own "status" (active|disabled) field.
type joinedChannel struct {
	channelView
	Role         domain.Role         `json:"role"`
	MemberStatus domain.MemberStatus `json:"memberStatus"`
}

// listJoinedChannels returns the channels the caller has joined (does not own).
func (h *AppHandler) listJoinedChannels(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channels, err := h.Store.ChannelsForMember(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list channels")
		return
	}
	// Owned channels surface under "your channels"; a self-join must not also list
	// them here, or they appear twice on the client.
	joined := make([]domain.Channel, 0, len(channels))
	members := make(map[string]domain.ChannelMember, len(channels))
	for _, c := range channels {
		if c.OwnerID == uid {
			continue
		}
		mem, err := h.Store.MembershipForUser(r.Context(), c.ID, uid)
		if err != nil {
			continue
		}
		joined = append(joined, c)
		members[c.ID] = mem
	}
	views := h.withLastMessages(r.Context(), joined)
	out := make([]joinedChannel, 0, len(views))
	for _, v := range views {
		mem := members[v.ID]
		out = append(out, joinedChannel{channelView: v, Role: mem.Role, MemberStatus: mem.Status})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"channels": out})
}

// leaveChannel removes the caller's own membership (and silences their devices).
// The owner cannot leave; they delete the channel instead.
func (h *AppHandler) leaveChannel(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if ch, err := h.channelByID(r, channelID); err == nil && ch.OwnerID == uid {
		httpx.Error(w, http.StatusBadRequest, "the owner cannot leave; delete the channel instead")
		return
	}
	if err := h.Store.RemoveMember(r.Context(), channelID, uid); err != nil && err != store.ErrNotFound {
		httpx.Error(w, http.StatusInternalServerError, "could not leave channel")
		return
	}
	_ = h.Store.SetSubscriptionStatusForUser(r.Context(), channelID, uid, domain.SubBlocked)
	w.WriteHeader(http.StatusNoContent)
}

// memberView embeds a membership with the user's email for display.
type memberView struct {
	domain.ChannelMember
	// Flattened rather than an embedded PublicProfile: both structs carry `json:"id"`, and Go drops
	// BOTH sides of a tag collision at the same depth rather than picking one — so embedding it
	// would have silently deleted the member's own id from every response.
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	AvatarID    string `json:"avatarId,omitempty"`
}

// withProfiles decorates members with their PUBLIC identity, resolved via a single users lookup.
//
// It used to attach the email address, which meant subscribing to a channel handed your email to
// whoever runs it — and a channel owner is an ordinary user, not an operator. Nobody agreed to that
// by pressing Subscribe. The name and handle are what somebody published about themselves, and they
// are what an owner needs to recognise a subscriber; the address they signed up with is not.
//
// Server administrators still see emails — see admin_handler.go. That is a different surface with a
// different audience: there the email IS the account identifier, and the person reading it operates
// the instance.
func (h *AppHandler) withProfiles(ctx context.Context, members []domain.ChannelMember) []memberView {
	out := make([]memberView, 0, len(members))

	// Only the members being listed are fetched. This used to call ListUsers, which reads EVERY
	// user on the server, to decorate a page of at most two hundred — so the cost of viewing one
	// channel's members grew with the size of the whole user base, on a page an administrator
	// refreshes. The output was always correctly scoped to members; it was the query that was not.
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	users, err := h.Store.UsersByIDs(ctx, ids)
	if err != nil {
		// The profile is a decoration on a list that is useful without it. Losing it must not cost
		// the owner the member list itself.
		slog.Default().Warn("could not load member profiles", "channelMembers", len(members), "error", err)
		users = nil
	}
	for _, m := range members {
		p := users[m.UserID].PublicProfileOf()
		out = append(out, memberView{
			ChannelMember: m,
			Username:      p.Username,
			DisplayName:   p.DisplayName,
			AvatarID:      p.AvatarID,
		})
	}
	return out
}

// listApprovals returns the pending-membership queue (owner/admin only).
func (h *AppHandler) listApprovals(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if _, ok := h.canAdminister(r, uid, channelID); !ok {
		httpx.Error(w, http.StatusForbidden, "not allowed")
		return
	}
	members, total, err := h.Store.ListMembers(r.Context(), channelID, domain.MemberPending, 0, 200)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load approvals")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"members": h.withProfiles(r.Context(), members), "total": total})
}

// listMembers returns a channel's subscribers, lazily paginated by offset/limit
// (owner/admin only).
func (h *AppHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if _, ok := h.canAdminister(r, uid, channelID); !ok {
		httpx.Error(w, http.StatusForbidden, "not allowed")
		return
	}
	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 200 {
		limit = v
	}
	members, total, err := h.Store.ListMembers(r.Context(), channelID, "", offset, limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load members")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"members": h.withProfiles(r.Context(), members), "total": total, "offset": offset, "limit": limit,
	})
}

// approveMember activates a pending membership and unblocks the user's devices.
func (h *AppHandler) approveMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	targetID := r.PathValue("userId")
	if _, ok := h.canAdminister(r, uid, channelID); !ok {
		httpx.Error(w, http.StatusForbidden, "not allowed")
		return
	}
	if err := h.Store.UpdateMemberStatus(r.Context(), channelID, targetID, domain.MemberActive); err != nil {
		h.writeStoreErr(w, err, "could not approve member")
		return
	}
	_ = h.Store.SetSubscriptionStatusForUser(r.Context(), channelID, targetID, domain.SubActive)
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// denyMember rejects a pending request: the membership is removed and the user's
// devices are silenced.
func (h *AppHandler) denyMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	targetID := r.PathValue("userId")
	if _, ok := h.canAdminister(r, uid, channelID); !ok {
		httpx.Error(w, http.StatusForbidden, "not allowed")
		return
	}
	if err := h.Store.RemoveMember(r.Context(), channelID, targetID); err != nil {
		h.writeStoreErr(w, err, "could not deny member")
		return
	}
	_ = h.Store.SetSubscriptionStatusForUser(r.Context(), channelID, targetID, domain.SubBlocked)
	w.WriteHeader(http.StatusNoContent)
}

type updateMemberRequest struct {
	Role   *domain.Role         `json:"role,omitempty"`
	Status *domain.MemberStatus `json:"status,omitempty"`
}

// updateMember changes a subscriber's per-channel role and/or status (ban/unban),
// propagating status changes to the user's device subscriptions (owner/admin only).
func (h *AppHandler) updateMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	targetID := r.PathValue("userId")
	ch, ok := h.canAdminister(r, uid, channelID)
	if !ok {
		httpx.Error(w, http.StatusForbidden, "not allowed")
		return
	}
	if targetID == ch.OwnerID {
		httpx.Error(w, http.StatusBadRequest, "cannot modify the channel owner")
		return
	}
	var req updateMemberRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Role != nil {
		if *req.Role != domain.RoleUser && *req.Role != domain.RoleAdmin {
			httpx.Error(w, http.StatusBadRequest, "invalid role")
			return
		}
		if err := h.Store.UpdateMemberRole(r.Context(), channelID, targetID, *req.Role); err != nil {
			h.writeStoreErr(w, err, "could not update member")
			return
		}
	}
	if req.Status != nil {
		if *req.Status != domain.MemberActive && *req.Status != domain.MemberPending && *req.Status != domain.MemberBlocked {
			httpx.Error(w, http.StatusBadRequest, "invalid status")
			return
		}
		if err := h.Store.UpdateMemberStatus(r.Context(), channelID, targetID, *req.Status); err != nil {
			h.writeStoreErr(w, err, "could not update member")
			return
		}
		_ = h.Store.SetSubscriptionStatusForUser(r.Context(), channelID, targetID, subStatusFor(*req.Status))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// removeMember deletes a subscriber's membership and silences their devices
// (owner/admin only).
func (h *AppHandler) removeMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	targetID := r.PathValue("userId")
	ch, ok := h.canAdminister(r, uid, channelID)
	if !ok {
		httpx.Error(w, http.StatusForbidden, "not allowed")
		return
	}
	if targetID == ch.OwnerID {
		httpx.Error(w, http.StatusBadRequest, "cannot remove the channel owner")
		return
	}
	if err := h.Store.RemoveMember(r.Context(), channelID, targetID); err != nil {
		h.writeStoreErr(w, err, "could not remove member")
		return
	}
	_ = h.Store.SetSubscriptionStatusForUser(r.Context(), channelID, targetID, domain.SubBlocked)
	w.WriteHeader(http.StatusNoContent)
}

// writeStoreErr maps a store error to an HTTP response: ErrNotFound → 404,
// otherwise 500 with the given message.
func (h *AppHandler) writeStoreErr(w http.ResponseWriter, err error, msg string) {
	if err == store.ErrNotFound {
		httpx.Error(w, http.StatusNotFound, "not found")
		return
	}
	httpx.Error(w, http.StatusInternalServerError, msg)
}
