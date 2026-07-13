package channel

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// previewChars bounds the message body carried in a channel-list preview. Bodies
// are unbounded, and the chat list would otherwise ship every channel's newest
// message in full just to render a single clamped line.
const previewChars = 200

// lastMessageView is the newest message of a channel, reduced to what the chat
// list renders: a preview line and a timestamp.
type lastMessageView struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	ImageCount int       `json:"imageCount"`
	CreatedAt  time.Time `json:"createdAt"`
}

// channelView is a channel plus its newest message. lastMessage is omitted for a
// channel that has never been notified.
type channelView struct {
	domain.Channel
	LastMessage *lastMessageView `json:"lastMessage,omitempty"`
}

// messageView is a message plus its comment count, so the feed can label the
// comment button without a request per message.
type messageView struct {
	domain.Message
	CommentCount int64 `json:"commentCount"`
}

// previewBody truncates s to at most previewChars runes, on a rune boundary.
func previewBody(s string) string {
	if utf8.RuneCountInString(s) <= previewChars {
		return s
	}
	n := 0
	for i := range s {
		if n == previewChars {
			return s[:i]
		}
		n++
	}
	return s
}

func newLastMessageView(m domain.Message) *lastMessageView {
	return &lastMessageView{
		ID:         m.ID,
		Title:      m.Title,
		Body:       previewBody(m.Body),
		ImageCount: len(m.Images),
		CreatedAt:  m.CreatedAt,
	}
}

// withLastMessages resolves the newest message of every given channel in one
// store call and returns the channels as views. On a store error the channels
// are still returned, without previews — a chat list without preview lines beats
// no chat list at all.
func (h *AppHandler) withLastMessages(ctx context.Context, channels []domain.Channel) []channelView {
	out := make([]channelView, 0, len(channels))
	ids := make([]string, 0, len(channels))
	for _, c := range channels {
		ids = append(ids, c.ID)
	}
	last, err := h.Store.LastMessagesByChannels(ctx, ids)
	if err != nil {
		last = nil
	}
	for _, c := range channels {
		view := channelView{Channel: c}
		if m, ok := last[c.ID]; ok {
			view.LastMessage = newLastMessageView(m)
		}
		out = append(out, view)
	}
	return out
}

// withCommentCounts resolves the comment count of every given message in one
// store call. A store error degrades to zero counts rather than failing the feed.
func (h *AppHandler) withCommentCounts(ctx context.Context, msgs []domain.Message) []messageView {
	out := make([]messageView, 0, len(msgs))
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	counts, err := h.Store.CommentCountsByMessages(ctx, ids)
	if err != nil {
		counts = nil
	}
	for _, m := range msgs {
		out = append(out, messageView{Message: m, CommentCount: counts[m.ID]})
	}
	return out
}
