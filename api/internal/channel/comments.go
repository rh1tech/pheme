package channel

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
)

// maxCommentLen bounds a single comment body.
const maxCommentLen = 2000

// commentView embeds a comment with its author's public profile (never the
// email), so the client can render the author without a second request.
type commentView struct {
	domain.Comment
	Author domain.PublicUser `json:"author"`
}

// hydrateComments resolves each comment's author into a commentView in one users
// lookup. Authors missing from the store (e.g. a deleted account) fall back to a
// bare PublicUser carrying only the id.
func (h *AppHandler) hydrateComments(ctx context.Context, comments []domain.Comment) []commentView {
	ids := make([]string, 0, len(comments))
	seen := map[string]struct{}{}
	for _, c := range comments {
		if _, ok := seen[c.UserID]; !ok {
			seen[c.UserID] = struct{}{}
			ids = append(ids, c.UserID)
		}
	}
	users, err := h.Store.UsersByIDs(ctx, ids)
	if err != nil {
		users = map[string]domain.User{}
	}
	out := make([]commentView, 0, len(comments))
	for _, c := range comments {
		author := domain.PublicUser{ID: c.UserID}
		if u, ok := users[c.UserID]; ok {
			author = u.Public()
		}
		out = append(out, commentView{Comment: c, Author: author})
	}
	return out
}

// messageInChannel loads a message and confirms it belongs to channelID. Returns
// ok=false (without writing) when missing or mismatched, so callers respond 404.
func (h *AppHandler) messageInChannel(r *http.Request, channelID, messageID string) (domain.Message, bool) {
	msg, err := h.Store.MessageByID(r.Context(), messageID)
	if err != nil || msg.ChannelID != channelID {
		return domain.Message{}, false
	}
	return msg, true
}

// listComments returns a message's comments newest-first (active members/owner
// only). 404 is used for both an unreadable channel and a missing message so
// existence is never leaked to outsiders.
func (h *AppHandler) listComments(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.canReadMessages(r, uid, channelID) {
		httpx.Error(w, http.StatusNotFound, "message not found")
		return
	}
	if _, ok := h.messageInChannel(r, channelID, r.PathValue("messageId")); !ok {
		httpx.Error(w, http.StatusNotFound, "message not found")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	cursor := r.URL.Query().Get("cursor")
	comments, err := h.Store.CommentsByMessage(r.Context(), r.PathValue("messageId"), cursor, limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not load comments")
		return
	}
	var next string
	if len(comments) == limit {
		next = comments[len(comments)-1].ID
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"comments": h.hydrateComments(r.Context(), comments), "nextCursor": next,
	})
}

type createCommentRequest struct {
	Body string `json:"body"`
}

// postComment adds a comment to a message. The caller must be an active member
// (or the owner) of the channel, the message must belong to the channel, and the
// message must permit comments. Comments are posted instantly (no pre-moderation).
func (h *AppHandler) postComment(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.canReadChannel(r.Context(), uid, channelID) {
		httpx.Error(w, http.StatusForbidden, "you must be an active member to comment")
		return
	}
	msg, ok := h.messageInChannel(r, channelID, r.PathValue("messageId"))
	if !ok {
		httpx.Error(w, http.StatusNotFound, "message not found")
		return
	}
	if !msg.CommentsAllowed {
		httpx.Error(w, http.StatusForbidden, "comments are closed for this message")
		return
	}
	var req createCommentRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		httpx.Error(w, http.StatusBadRequest, "comment body is required")
		return
	}
	if len(body) > maxCommentLen {
		httpx.Error(w, http.StatusBadRequest, "comment is too long")
		return
	}
	c, err := h.Store.CreateComment(r.Context(), domain.Comment{
		MessageID: msg.ID,
		ChannelID: channelID,
		UserID:    uid,
		Body:      body,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not post comment")
		return
	}
	views := h.hydrateComments(r.Context(), []domain.Comment{c})
	httpx.JSON(w, http.StatusCreated, views[0])
}

// deleteComment removes a comment. Allowed for the comment's author, or for the
// channel owner / a channel admin (moderation within their channel).
func (h *AppHandler) deleteComment(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	commentID := r.PathValue("commentId")
	c, err := h.Store.CommentByID(r.Context(), commentID)
	if err != nil || c.ChannelID != channelID || c.MessageID != r.PathValue("messageId") {
		httpx.Error(w, http.StatusNotFound, "comment not found")
		return
	}
	if c.UserID != uid {
		if _, allowed := h.canAdminister(r, uid, channelID); !allowed {
			httpx.Error(w, http.StatusForbidden, "not allowed")
			return
		}
	}
	if err := h.Store.DeleteComment(r.Context(), commentID); err != nil {
		h.writeStoreErr(w, err, "could not delete comment")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
