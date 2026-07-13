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
	_, convID, _, ok := h.requireMember(w, r)
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
	msgs, err := h.Store.ChatMessagesByConversation(r.Context(), convID, cursor, limit)
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
	if !httpx.Decode(w, r, &req) {
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
	go func() {
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

		if _, err := h.Push.SendChat(ctx, push.ChatNotification{
			ConversationID: convID,
			MessageID:      msg.ID,
			SenderName:     h.senderName(ctx, senderID),
		}, devices); err != nil {
			log.Error("chat push: send", "conversation", convID, "error", err)
		}
	}()
}

// senderName resolves the display name shown in the notification. It comes from
// the user's profile, never from the message.
func (h *Handler) senderName(ctx context.Context, userID string) string {
	u, err := h.Store.UserByID(ctx, userID)
	if err != nil {
		return ""
	}
	if u.DisplayName != "" {
		return u.DisplayName
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return ""
}

// isControlContent reports whether a content type is MLS protocol traffic rather
// than a user-visible message.
func isControlContent(contentType string) bool {
	return contentType == contentTypeMLSWelcome
}

// The content type clients use for a relayed MLS Welcome. Mirrors MLS_WELCOME in
// web/src/lib/mls.ts. The server does not interpret the bytes — it only needs to
// know this is protocol traffic, so it does not notify a human about it.
const contentTypeMLSWelcome = "application/mls-welcome"

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
	if !httpx.Decode(w, r, &req) {
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
