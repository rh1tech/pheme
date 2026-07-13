package chat

import (
	"context"
	"net/http"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
)

// lastChatMessageView is a conversation's newest message reduced to what the chat
// list needs. Ciphertext is included because only the client can render a preview
// from it; the server still cannot read it.
type lastChatMessageView struct {
	ID          string `json:"id"`
	SenderID    string `json:"senderId"`
	Ciphertext  []byte `json:"ciphertext"`
	ContentType string `json:"contentType"`
	CreatedAt   string `json:"createdAt"`
}

// conversationView is a conversation plus the data the chat list renders without
// a second request: its members (to label a direct chat and show group avatars)
// and its newest message (ordering + preview).
type conversationView struct {
	domain.Conversation
	Members     []domain.ConversationMember `json:"members"`
	LastMessage *lastChatMessageView        `json:"lastMessage,omitempty"`
}

func (h *Handler) listConversations(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	convs, err := h.Store.ConversationsForUser(r.Context(), uid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list conversations")
		return
	}

	ids := make([]string, 0, len(convs))
	for _, c := range convs {
		ids = append(ids, c.ID)
	}
	last, err := h.Store.LastChatMessagesByConversations(r.Context(), ids)
	if err != nil {
		last = nil
	}

	out := make([]conversationView, 0, len(convs))
	for _, c := range convs {
		view := conversationView{Conversation: c, Members: h.membersOf(r.Context(), c.ID)}
		if msg, present := last[c.ID]; present {
			view.LastMessage = &lastChatMessageView{
				ID:          msg.ID,
				SenderID:    msg.SenderID,
				Ciphertext:  msg.Ciphertext,
				ContentType: msg.ContentType,
				CreatedAt:   msg.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
			}
		}
		out = append(out, view)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func (h *Handler) membersOf(ctx context.Context, convID string) []domain.ConversationMember {
	members, err := h.Store.ConversationMembers(ctx, convID)
	if err != nil {
		return nil
	}
	return members
}
