package chat

import (
	"context"
	"encoding/json"
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

	// Fan out to the conversation's members over the shared SSE stream, naming them.
	//
	// The recipient list is what keeps this affordable. The live bus is one global stream: every
	// event reaches every open connection on every instance, and each connection then decides
	// whether it was entitled to it. Without a list that decision is a database lookup, so the cost
	// of one message is one query PER OPEN CONNECTION — the product of traffic and population,
	// not of conversation size. Measured at a thousand streams and twenty-five messages a second,
	// that was twenty-five thousand queries a second, three-and-a-half second delivery latency, and
	// a fifth of all messages dropped on the floor.
	//
	// With the list it is a slice scan against a list this handler already has to fetch to send
	// push notifications.
	members := h.membersFor(r.Context(), convID)
	h.Live.Publish(live.Event{ConversationID: convID, ChatMessage: &msg, Recipients: userIDsOf(members)})
	h.notifyMembers(convID, uid, msg, members)
	httpx.JSON(w, http.StatusCreated, msg)
}

// How long a background chat push may take before it is abandoned. It outlives the
// request, so it needs its own bound.
const pushTimeout = 15 * time.Second

// notifyMembers pushes a generic notification to the other members' devices.
//
// The notification says who sent a message, never what it said — the server holds
// only ciphertext (push.ChatNotification has no field for content). Delivery runs
// in the background: a slow push service must not slow down sending a message, and
// a failed push must not fail it either, since the message is already stored and
// the live stream has it.
// pruneDeadDevices removes push addresses the push service has declared permanently dead.
//
// Nothing used to. FCM answered UNREGISTERED for a token that had been rotated or whose app had
// been uninstalled, the web push service answered 410 for a dropped subscription, and both were
// recorded in a Result that no one read — so the row stayed and was pushed to on every message for
// the life of the account. One phone had four rows, three of which could never receive anything.
//
// Only on GONE, never on a plain failure: a device must not lose its registration because the
// network was down or a push service had a bad minute.
func (h *Handler) pruneDeadDevices(ctx context.Context, results []push.Result) {
	// The rule for what counts as permanently dead lives in push.GoneDeviceIDs, shared with the
	// channel broadcast dispatcher. It was written here first and not there, so broadcasts retried
	// dead addresses forever while chat did not — the kind of divergence that only shows up as a
	// slow, invisible tax.
	for _, id := range push.GoneDeviceIDs(results) {
		if err := h.Store.DeleteDevice(ctx, id); err != nil {
			h.logger().Error("chat push: prune dead device", "device", id, "error", err)
			continue
		}
		h.logger().Info("chat push: pruned a dead push address", "device", id)
	}
}

func (h *Handler) notifyMembers(convID, senderID string, msg domain.ChatMessage, members []domain.ConversationMember) {
	// Control messages (the MLS Welcome that lets a member join) are protocol
	// traffic, not something a human sent. Never notify for them.
	if h.Push == nil || isControlContent(msg.ContentType) {
		return
	}
	// Queue the fan-out. This never blocks the caller — a saturated push provider must not slow
	// down sending messages — but past the queue's bounds it still returns false, and then the
	// notification really is lost.
	if !messagePush.offer(pushJob{
		h: h, convID: convID, senderID: senderID, msg: msg, members: members,
		queuedAt: time.Now(),
	}) {
		if n, report := messagePushDrops.record(time.Now()); report {
			h.logger().Warn("chat push: queue full, notifications dropped",
				"dropped", n, "over", reportWindow, "conversation", convID)
		}
	}
}

// deliverPush is the fan-out itself, run by a queue worker rather than on the request path.
func (h *Handler) deliverPush(
	convID, senderID string, msg domain.ChatMessage, members []domain.ConversationMember,
) {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()
	log := h.logger()

	// The caller already read the roster to address the live event; re-reading it here was one
	// wholly redundant query per message. Empty means that read failed, so fall back rather
	// than silently notifying nobody.
	if len(members) == 0 {
		var err error
		if members, err = h.Store.ConversationMembers(ctx, convID); err != nil {
			log.Error("chat push: load members", "conversation", convID, "error", err)
			return
		}
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
		results, err := h.Push.SendChat(ctx, push.ChatNotification{
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
		}, group)
		if err != nil {
			log.Error("chat push: send", "conversation", convID, "privacy", string(key.privacy), "error", err)
		}
		h.pruneDeadDevices(ctx, results)
		h.reportSendFailures(results)
	}
}

// reportSendFailures surfaces per-device push failures, which nothing used to look at.
//
// SendChat returns a Result per device and a single error covering the call as a whole. The caller
// logged the second and read the first only to prune permanently dead addresses, so a device whose
// notification simply failed was recorded and then discarded. With every individual send failing
// the call-level error is still nil, which means a push service refusing everything looked
// identical to one accepting everything.
func (h *Handler) reportSendFailures(results []push.Result) {
	failed, sample := 0, ""
	for _, r := range results {
		// Gone is not a failure to report here: the address is permanently dead and pruneDeadDevices
		// has already removed it, so counting it again would report a delivery problem every time a
		// stale registration is cleaned up.
		if r.Gone || r.Status != domain.DeliveryFailed {
			continue
		}
		failed++
		if sample == "" {
			sample = r.Error
		}
	}
	if failed == 0 {
		return
	}
	if n, report := pushSendFailures.recordN(failed, time.Now()); report {
		h.logger().Warn("chat push: the push service refused notifications",
			"failed", n, "over", reportWindow, "example", sample)
	}
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
		// A push address that cannot be traced back to an MLS device does not get ciphertext, even
		// if it says it can render a preview.
		//
		// Revocation is why. Terminating a device removes its push rows by matching mlsDeviceId, so
		// a row that carries none is unmatched by that delete and survives it — and a surviving row
		// with CanRenderPreview set goes on being handed the ciphertext of messages the device has
		// just been forbidden to read. domain.Device.MLSDeviceID has always said this was enforced;
		// it was not, anywhere. Legacy rows and rows registered before the client had minted its MLS
		// identity both land here, and both are exactly the rows that cannot be revoked.
		//
		// The cost of being wrong this way is one generic notification until the device registers
		// again with its identity attached. The cost of being wrong the other way is ciphertext
		// delivered to a device that was revoked.
		accountable := d.MLSDeviceID != ""
		key := pushGroup{privacy: p, rendersPreview: d.CanRenderPreview && accountable}
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
// Inverted, and the inversion is the point: everything is control traffic EXCEPT the one type a
// person actually writes. This used to list the protocol types it knew about and notify for
// anything else, which meant the default for a type nobody had thought about was to buzz every
// member of the conversation.
//
// That default cost real users real notifications. Signing in on a new device posts a history
// request to every conversation the account is in, and a rejoin posts another; neither was on the
// list, so every member's phone lit up because somebody else had opened the app on a new phone.
// application/mls-rejoin does not appear anywhere in this server's source at all — it is a content
// type the client invented, and under the old rule an invented type notified by default.
//
// A protocol message added by a future client cannot do this again. It has to be named
// application/mls and carry something a human typed to reach anybody's lock screen.
//
// The one deliberate loss is a call event, which IS user-visible — it is the "missed call" line in
// the transcript. It stays silent for the same reason it always did: the phone rang when the call
// came in, and buzzing again to report that the call it just announced went unanswered is not a
// notification, it is a nag.
func isControlContent(contentType string) bool {
	return contentType != domain.ContentTypeMLSApplication
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
	h.recordMembershipChange(r, convID, member.UserID, req.UserID, "added")
	httpx.JSON(w, http.StatusCreated, added)
}

// recordMembershipChange writes the plaintext line that makes a roster change visible in the
// conversation, and fans it out live.
//
// Best effort on purpose: the membership change itself has already been committed and reported, and
// failing the request because the note about it could not be written would be the tail wagging the
// dog. A missing line is a cosmetic loss; a failed add is not.
//
// See domain.ContentTypeMembership for why this one message is not encrypted.
func (h *Handler) recordMembershipChange(r *http.Request, convID, actorID, subjectID, action string) {
	payload, err := json.Marshal(map[string]string{
		"action":  action,
		"actorId": actorID,
		"userId":  subjectID,
	})
	if err != nil {
		return
	}
	msg, err := h.Store.AppendChatMessage(r.Context(), domain.ChatMessage{
		ConversationID: convID,
		SenderID:       actorID,
		Ciphertext:     payload,
		ContentType:    domain.ContentTypeMembership,
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		h.logger().Error("membership note", "conversation", convID, "action", action, "error", err)
		return
	}
	h.Live.Publish(live.Event{ConversationID: convID, ChatMessage: &msg})
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
	// "left" when they removed themselves, "removed" when somebody else did — the same event to the
	// roster, but not the same thing to read.
	action := "removed"
	if target == member.UserID {
		action = "left"
	}
	h.recordMembershipChange(r, convID, member.UserID, target, action)
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

// membersFor reads the conversation's roster once, for both the people who should receive the live
// event and the people who should be notified. Those were separate reads of the same rows.
//
// A nil result is deliberately safe. The SSE loop falls back to a per-connection membership lookup
// when the recipient list is empty, and the push path re-reads the roster itself, so a failure here
// costs performance and never delivery — the opposite tradeoff would drop messages whenever the
// database hiccuped.
func (h *Handler) membersFor(ctx context.Context, convID string) []domain.ConversationMember {
	members, err := h.Store.ConversationMembers(ctx, convID)
	if err != nil {
		h.logger().Warn("could not list members for live fanout; falling back to per-connection checks",
			"conversation", convID, "error", err)
		return nil
	}
	return members
}

func userIDsOf(members []domain.ConversationMember) []string {
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	return ids
}
