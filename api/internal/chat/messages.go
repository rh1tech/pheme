package chat

import (
	"net/http"
	"strconv"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/live"
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

	// Fan out to the conversation's members over the shared SSE stream. Unlike a
	// channel notify, this is synchronous and has no push step yet (push lands in
	// the E2EE phase, as generic "New message" text).
	h.Live.Publish(live.Event{ConversationID: convID, ChatMessage: &msg})
	httpx.JSON(w, http.StatusCreated, msg)
}

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
