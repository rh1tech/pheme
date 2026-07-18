package chat

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/push"
)

// Bytes on the wire per message. The server never inspects the content, but a
// bound keeps a single message (and thus the store) from being abused.
const maxCiphertextBytes = 256 * 1024

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) {
	_, convID, member, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	cursor := r.URL.Query().Get("cursor")
	// member.ClearedAt is this member's private clear-history watermark: the store
	// returns only messages newer than it, so cleared history stays hidden on every
	// device this user signs in on, without touching anyone else's view.
	msgs, err := h.Store.ChatMessagesByConversation(r.Context(), convID, cursor, limit, member.ClearedAt)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load messages")
		return
	}
	// The cursor walks backward in time: it is the oldest message of the page,
	// what "load older" needs — identical to the channel feed.
	var next string
	if len(msgs) == limit {
		next = msgs[len(msgs)-1].ID
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"messages": msgs, "nextCursor": next})
}

type postMessageRequest struct {
	// Ciphertext is base64 in the JSON body (Go decodes []byte from base64). The
	// server stores it verbatim and never reads it.
	Ciphertext  []byte `json:"ciphertext"`
	ContentType string `json:"contentType"`
}

func (h *Handler) postMessage(w http.ResponseWriter, r *http.Request) {
	uid, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	var req postMessageRequest
	// The ciphertext arrives base64-encoded (~4/3 of its size), so the body ceiling
	// is the byte limit with room for the encoding and the surrounding JSON.
	if !httpx.DecodeLimited(w, r, &req, 2*maxCiphertextBytes) {
		return
	}
	if len(req.Ciphertext) == 0 {
		httpx.Error(w, http.StatusBadRequest, "ciphertext is required")
		return
	}
	if len(req.Ciphertext) > maxCiphertextBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "message too large")
		return
	}
	contentType := req.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	// A Welcome or a Commit may only be posted through the MLS commit endpoint, which
	// weighs it against the conversation's epoch and refuses it if another member got
	// there first. Letting one in through the ordinary message path would put a Commit
	// into the log that the group never agreed to — and every member who applied it would
	// be forked off the conversation. This route relays whatever it is given, so it must
	// not be given these.
	if contentType == contentTypeMLSWelcome || contentType == contentTypeMLSCommit {
		httpx.Error(w, http.StatusBadRequest, "post MLS commits to /mls/commit")
		return
	}

	msg, err := h.Store.AppendChatMessage(r.Context(), domain.ChatMessage{
		ConversationID: convID,
		SenderID:       uid,
		Ciphertext:     req.Ciphertext,
		ContentType:    contentType,
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not send message")
		return
	}

	// Fan out to the conversation's members over the shared SSE stream.
	h.Live.Publish(live.Event{ConversationID: convID, ChatMessage: &msg})
	h.notifyMembers(convID, uid, msg)
	httpx.JSON(w, http.StatusCreated, msg)
}

// How long a background chat push may take before it is abandoned. It outlives the
// request, so it needs its own bound.
const pushTimeout = 15 * time.Second

// How many chat pushes may be in flight at once, process-wide.
//
// Each one holds a connection to the push provider for up to pushTimeout. Without a
// ceiling, sending a message — which used to be O(1) work — would fan out into an
// unbounded number of goroutines, so anyone able to post quickly could exhaust file
// descriptors here and hammer the provider. Past the ceiling a push is dropped rather
// than queued: a notification is a courtesy, and a stale one is worth less than a
// responsive server. The message itself is already stored and on the live stream.
const maxConcurrentPushes = 64

var pushSlots = make(chan struct{}, maxConcurrentPushes)

// notifyMembers pushes a generic notification to the other members' devices.
//
// The notification says who sent a message, never what it said — the server holds
// only ciphertext (push.ChatNotification has no field for content). Delivery runs
// in the background: a slow push service must not slow down sending a message, and
// a failed push must not fail it either, since the message is already stored and
// the live stream has it.
func (h *Handler) notifyMembers(convID, senderID string, msg domain.ChatMessage) {
	// Control messages (the MLS Welcome that lets a member join) are protocol
	// traffic, not something a human sent. Never notify for them.
	if h.Push == nil || isControlContent(msg.ContentType) {
		return
	}
	// Take a slot, or drop the notification. Never block the caller: this runs on the
	// request path, and a saturated push provider must not slow down sending messages.
	select {
	case pushSlots <- struct{}{}:
	default:
		h.logger().Warn("chat push: at capacity, notification dropped", "conversation", convID)
		return
	}
	go func() {
		defer func() { <-pushSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()
		log := h.logger()

		members, err := h.Store.ConversationMembers(ctx, convID)
		if err != nil {
			log.Error("chat push: load members", "conversation", convID, "error", err)
			return
		}
		recipients := make([]string, 0, len(members))
		for _, m := range members {
			if m.UserID != senderID { // never notify the sender of their own message
				recipients = append(recipients, m.UserID)
			}
		}
		if len(recipients) == 0 {
			return
		}

		devices, err := h.Store.DevicesForUsers(ctx, recipients)
		if err != nil {
			log.Error("chat push: load devices", "conversation", convID, "error", err)
			return
		}
		if len(devices) == 0 {
			return
		}

		name, avatarID := h.senderIdentity(ctx, senderID)
		// One send per privacy setting, not one per conversation. What a push may say is the
		// RECIPIENT's decision, and two members of the same chat can decide differently, so the
		// devices are split by their owner's setting and each group gets the payload it asked
		// for. Almost always this is a single group — the loop costs nothing when everyone
		// agrees, and is the only correct thing to do when they do not.
		// The conversation's groups, loaded ONCE and only if some recipient actually wants a
		// preview. Everybody else's notification does not depend on it, and a chat where nobody
		// opted in should not pay for a lookup it will not use.
		var groupIDs []string
		groupsLoaded := false
		loadGroupIDs := func() []string {
			if groupsLoaded {
				return groupIDs
			}
			groupsLoaded = true
			state, err := h.Store.MLSGroupState(ctx, convID)
			if err != nil {
				// Not fatal. Without the ids the device falls back to the mapping it learned by
				// opening the chat, and failing that to the generic notification.
				log.Warn("chat push: load group state for preview", "conversation", convID, "error", err)
				return nil
			}
			if state.GroupID != "" {
				groupIDs = append(groupIDs, state.GroupID)
			}
			// Newest first: a retired group still decrypts its own old messages, but the message
			// that just arrived is overwhelmingly likely to belong to the current one.
			groupIDs = append(groupIDs, state.PriorGroupIDs...)
			return groupIDs
		}

		for key, group := range h.devicesByPrivacy(ctx, recipients, devices) {
			var preview []string
			if key.privacy.ShowsPreview() && key.rendersPreview {
				preview = loadGroupIDs()
			}
			if _, err := h.Push.SendChat(ctx, push.ChatNotification{
				ConversationID:       convID,
				MessageID:            msg.ID,
				SenderName:           name,
				SenderAvatarID:       avatarID,
				Privacy:              key.privacy,
				DeviceRendersPreview: key.rendersPreview,
				// Passed to every group; the payload builder attaches it only for the group that
				// asked for previews. Deciding here instead would mean each caller re-deriving
				// the same rule, and one of them eventually getting it wrong.
				Ciphertext:  msg.Ciphertext,
				ContentType: msg.ContentType,
				GroupIDs:    preview,
			}, group); err != nil {
				log.Error("chat push: send", "conversation", convID, "privacy", string(key.privacy), "error", err)
			}
		}
	}()
}

// devicesByPrivacy groups recipients' devices by their owner's notification privacy setting.
//
// A user whose profile cannot be loaded is treated as the most private option rather than the
// default one. The failure mode matters: guessing "show everything" for a user we know nothing
// about puts a name on a lock screen that its owner may have explicitly asked to keep off it,
// and a transient database error is not a good reason to override that. Guessing the other way
// costs them one vague notification.
// pushGroup is the set of recipients a single payload can serve: everyone who wants the same
// thing AND runs a build that can render it.
type pushGroup struct {
	privacy        domain.NotificationPrivacy
	rendersPreview bool
}

func (h *Handler) devicesByPrivacy(
	ctx context.Context, recipients []string, devices []domain.Device,
) map[pushGroup][]domain.Device {
	// One lookup for the whole conversation, not one per member: this runs on every message
	// sent, and a busy group chat would otherwise turn each one into a query per recipient.
	users, err := h.Store.UsersByIDs(ctx, recipients)
	if err != nil {
		h.logger().Warn("chat push: load recipients, assuming private", "error", err)
		users = nil
	}

	groups := make(map[pushGroup][]domain.Device, 2)
	for _, d := range devices {
		// A recipient we could not load — the whole lookup failed, or UsersByIDs omitted them —
		// is treated as the most private option rather than the default one. Guessing "show
		// everything" for a user we know nothing about puts a name on a lock screen its owner may
		// have explicitly asked to keep off it, and a transient database error is not a good
		// reason to override that. Guessing the other way costs them one vague notification.
		p := domain.NotificationPrivacyGeneric
		if u, ok := users[d.UserID]; ok {
			// Effective(), not the raw value: a legacy account stores "" and every consumer would
			// otherwise have to remember to resolve it. Normalising here also stops a conversation
			// that mixes legacy and explicit-sender recipients from splitting into two groups and
			// sending the same payload twice.
			p = u.NotificationPrivacy.Effective()
		}
		key := pushGroup{privacy: p, rendersPreview: d.CanRenderPreview}
		groups[key] = append(groups[key], d)
	}
	return groups
}

// senderIdentity resolves the name and avatar a notification may show for the sender. Both
// come from the user's profile, never from the message — the server cannot read the message.
//
// Whether they are actually shown is not decided here: that is the recipient's setting, applied
// when the payload is built. This only answers what there is to show.
func (h *Handler) senderIdentity(ctx context.Context, userID string) (name, avatarID string) {
	u, err := h.Store.UserByID(ctx, userID)
	if err != nil {
		return "", ""
	}
	switch {
	case u.DisplayName != "":
		name = u.DisplayName
	case u.Username != "":
		name = "@" + u.Username
	}
	return name, u.AvatarID
}

// isControlContent reports whether a content type must not raise a push notification.
//
// The MLS types are protocol traffic and nobody wrote them. A call event is different: it is
// user-visible, and it is the "missed call" line in the transcript — but the phone was rung
// when the call came in, and buzzing it again to say the call it just announced went
// unanswered is not a notification, it is a nag.
func isControlContent(contentType string) bool {
	switch contentType {
	case contentTypeMLSWelcome, contentTypeMLSCommit, contentTypeMLSDevice, contentTypeCallEvent:
		return true
	default:
		return false
	}
}

// The MLS protocol content types, defined once in domain (the store orders a catch-up
// by them, so they cannot live only here).
//
// Note what mls-device replaced: a "rejoin" message that asked the conversation's
// creator to DESTROY the group and build a new one. That is what made this bug
// catastrophic rather than merely annoying — every rebuild threw away the key material
// for every message anyone had ever sent. A device that is missing from the group now
// gets ADDED to it; the group is not torn down around it.
const (
	contentTypeMLSWelcome = domain.ContentTypeMLSWelcome
	contentTypeMLSCommit  = domain.ContentTypeMLSCommit
	contentTypeMLSDevice  = domain.ContentTypeMLSDevice
	contentTypeCallEvent  = domain.ContentTypeCallEvent
)

type addMemberRequest struct {
	UserID string `json:"userId"`
}

func (h *Handler) addMember(w http.ResponseWriter, r *http.Request) {
	_, convID, member, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	conv, err := h.Store.ConversationByID(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "conversation not found")
		return
	}
	// Direct chats are a fixed pair; only a group admin may add.
	if conv.Kind != domain.ConversationGroup {
		httpx.Error(w, http.StatusBadRequest, "cannot add members to a direct chat")
		return
	}
	if member.Role != domain.RoleAdmin {
		httpx.Error(w, http.StatusForbidden, "only a group admin can add members")
		return
	}

	var req addMemberRequest
	if !httpx.DecodeLimited(w, r, &req, maxSmallBodyBytes) {
		return
	}
	if _, err := h.Store.UserByID(r.Context(), req.UserID); err != nil {
		httpx.Error(w, http.StatusNotFound, "user not found")
		return
	}
	added, err := h.Store.AddConversationMember(r.Context(), domain.ConversationMember{
		ConversationID: convID,
		UserID:         req.UserID,
		Role:           domain.RoleUser,
		JoinedAt:       time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not add member")
		return
	}
	httpx.JSON(w, http.StatusCreated, added)
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	_, convID, member, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	target := r.PathValue("userId")
	// A member may remove themselves (leave); only an admin may remove others.
	if target != member.UserID && member.Role != domain.RoleAdmin {
		httpx.Error(w, http.StatusForbidden, "only a group admin can remove members")
		return
	}
	if err := h.Store.RemoveConversationMember(r.Context(), convID, target); err != nil {
		httpx.Error(w, http.StatusNotFound, "member not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setRoleRequest struct {
	Role domain.Role `json:"role"`
}

// setMemberRole promotes or demotes a group member. Admins only, groups only.
func (h *Handler) setMemberRole(w http.ResponseWriter, r *http.Request) {
	_, convID, member, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if member.Role != domain.RoleAdmin {
		httpx.Error(w, http.StatusForbidden, "only a group admin can change roles")
		return
	}
	conv, err := h.Store.ConversationByID(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "conversation not found")
		return
	}
	if conv.Kind != domain.ConversationGroup {
		httpx.Error(w, http.StatusBadRequest, "a direct chat has no roles")
		return
	}
	var req setRoleRequest
	if !httpx.DecodeLimited(w, r, &req, maxSmallBodyBytes) {
		return
	}
	if req.Role != domain.RoleAdmin && req.Role != domain.RoleUser {
		httpx.Error(w, http.StatusBadRequest, "role must be 'admin' or 'user'")
		return
	}
	target := r.PathValue("userId")
	if err := h.Store.SetConversationMemberRole(r.Context(), convID, target, req.Role); err != nil {
		httpx.Error(w, http.StatusNotFound, "member not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type receiptRequest struct {
	// Watermarks, not message ids: the client says how far it has got, and an omitted
	// (zero) field leaves that watermark where it is.
	Delivered time.Time `json:"delivered"`
	Read      time.Time `json:"read"`
}

// reportReceipt advances the caller's delivered/read watermarks in a conversation.
//
// The caller reports its own position and nobody else's, which is the only thing it is in a
// position to know. Neither watermark ever moves backwards (the store enforces it), so a
// duplicate or out-of-order report — two devices, a retry, a catch-up after being offline —
// is harmless.
//
// A read implies delivery: you cannot read what never arrived. Reporting only `read` would
// otherwise leave a message double-ticked but not single-ticked, which is nonsense, so read
// carries delivered up with it.
func (h *Handler) reportReceipt(w http.ResponseWriter, r *http.Request) {
	uid, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	var req receiptRequest
	if !httpx.DecodeLimited(w, r, &req, maxSmallBodyBytes) {
		return
	}
	if req.Delivered.IsZero() && req.Read.IsZero() {
		httpx.Error(w, http.StatusBadRequest, "delivered or read is required")
		return
	}
	delivered := req.Delivered
	if req.Read.After(delivered) {
		delivered = req.Read
	}

	receipt, err := h.Store.SetConversationReceipt(r.Context(), convID, uid, delivered, req.Read)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not record receipt")
		return
	}
	// Tell the conversation, so the sender's ticks move now rather than on their next fetch.
	h.Live.Publish(live.Event{ConversationID: convID, Receipt: &receipt})
	httpx.JSON(w, http.StatusOK, receipt)
}

// clearMessages clears the caller's own history of a conversation, keeping the
// conversation itself. It sets a per-member watermark rather than deleting the shared
// message log: a chat message is a single MLS-encrypted row read by every member, so
// deleting rows would erase the history for everyone. The watermark hides everything
// up to now from THIS member, on all their devices, and leaves other members untouched.
// Any member may clear their own history — direct or group, no special role needed.
func (h *Handler) clearMessages(w http.ResponseWriter, r *http.Request) {
	uid, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if err := h.Store.ClearConversationHistory(r.Context(), convID, uid, time.Now().UTC()); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not clear history")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteConversation removes a conversation for everyone in it. Either party may
// delete a direct chat; only an admin may delete a group. Membership is captured
// before the delete so the live event can still reach the (now ex-) members.
func (h *Handler) deleteConversation(w http.ResponseWriter, r *http.Request) {
	_, convID, member, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	conv, err := h.Store.ConversationByID(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "conversation not found")
		return
	}
	if conv.Kind == domain.ConversationGroup && member.Role != domain.RoleAdmin {
		httpx.Error(w, http.StatusForbidden, "only a group admin can delete the group")
		return
	}

	members, err := h.Store.ConversationMembers(r.Context(), convID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not delete conversation")
		return
	}
	recipients := make([]string, 0, len(members))
	for _, m := range members {
		recipients = append(recipients, m.UserID)
	}

	// The photos go with the messages. Their keys lived inside the messages, so a blob left behind
	// is not merely orphaned — it is landfill nobody, including us, will ever be able to open.
	//
	// Before the conversation row goes, because the attachment records are keyed on it.
	h.deleteConversationAttachments(r, convID)

	if err := h.Store.DeleteConversation(r.Context(), convID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not delete conversation")
		return
	}
	h.Live.Publish(live.Event{
		ConversationID:      convID,
		ConversationDeleted: true,
		Recipients:          recipients,
	})
	w.WriteHeader(http.StatusNoContent)
}
