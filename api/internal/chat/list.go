package chat

import (
	"context"
	"net/http"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
)

// memberView is a conversation member plus their public profile, so a client can
// label a direct chat with the other person's name and show group avatars
// without a second round trip. Email is never included.
type memberView struct {
	domain.ConversationMember
	User domain.PublicUser `json:"user"`
}

// lastChatMessageView is a conversation's newest message reduced to what the chat
// list needs. Ciphertext is included because only the client can render a preview
// from it; the server still cannot read it.
type lastChatMessageView struct {
	ID          string    `json:"id"`
	SenderID    string    `json:"senderId"`
	Ciphertext  []byte    `json:"ciphertext"`
	ContentType string    `json:"contentType"`
	CreatedAt   time.Time `json:"createdAt"`
}

// conversationView is a conversation plus what the chat list and header render
// without a second request: its members (hydrated with public profiles) and its
// newest message.
type conversationView struct {
	domain.Conversation
	Members     []memberView         `json:"members"`
	LastMessage *lastChatMessageView `json:"lastMessage,omitempty"`
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
		view := conversationView{Conversation: c, Members: h.membersView(r.Context(), c.ID)}
		if msg, present := last[c.ID]; present {
			view.LastMessage = newLastChatMessageView(msg)
		}
		out = append(out, view)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"conversations": out})
}

func newLastChatMessageView(msg domain.ChatMessage) *lastChatMessageView {
	return &lastChatMessageView{
		ID:          msg.ID,
		SenderID:    msg.SenderID,
		Ciphertext:  msg.Ciphertext,
		ContentType: msg.ContentType,
		CreatedAt:   msg.CreatedAt,
	}
}

// membersView loads a conversation's members and hydrates each with its public
// profile in one UsersByIDs lookup.
func (h *Handler) membersView(ctx context.Context, convID string) []memberView {
	members, err := h.Store.ConversationMembers(ctx, convID)
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.UserID)
	}
	users, err := h.Store.UsersByIDs(ctx, ids)
	if err != nil {
		users = nil
	}
	out := make([]memberView, 0, len(members))
	for _, m := range members {
		pub := domain.PublicUser{ID: m.UserID}
		if u, present := users[m.UserID]; present {
			pub = u.Public()
		}
		out = append(out, memberView{ConversationMember: m, User: pub})
	}
	return out
}
