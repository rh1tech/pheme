package channel

import (
	"net/http"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
)

// adminComment is a comment enriched for moderation: the author's email and id,
// the channel name, and the message title, so the panel is readable at a glance.
type adminComment struct {
	domain.Comment
	AuthorEmail  string `json:"authorEmail"`
	AuthorID     string `json:"authorId"`
	ChannelName  string `json:"channelName"`
	MessageTitle string `json:"messageTitle"`
}

// listComments returns all comments newest-first with search and pagination
// (admin only). It hydrates author email, channel name, and message title via a
// single pass over the page's referenced ids.
func (h *AdminHandler) listComments(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	q, offset, limit, page := pageParams(r)
	comments, total, err := h.Store.AdminListComments(r.Context(), q, offset, limit)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list comments")
		return
	}

	userIDs := make([]string, 0, len(comments))
	seen := map[string]struct{}{}
	for _, c := range comments {
		if _, ok := seen[c.UserID]; !ok {
			seen[c.UserID] = struct{}{}
			userIDs = append(userIDs, c.UserID)
		}
	}
	users, err := h.Store.UsersByIDs(r.Context(), userIDs)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "could not list comments")
		return
	}

	out := make([]adminComment, 0, len(comments))
	channelNames := map[string]string{}
	messageTitles := map[string]string{}
	for _, c := range comments {
		if _, ok := channelNames[c.ChannelID]; !ok {
			if ch, cerr := h.Store.ChannelByID(r.Context(), c.ChannelID); cerr == nil {
				channelNames[c.ChannelID] = ch.Name
			} else {
				channelNames[c.ChannelID] = ""
			}
		}
		if _, ok := messageTitles[c.MessageID]; !ok {
			if msg, merr := h.Store.MessageByID(r.Context(), c.MessageID); merr == nil {
				messageTitles[c.MessageID] = msg.Title
			} else {
				messageTitles[c.MessageID] = ""
			}
		}
		out = append(out, adminComment{
			Comment:      c,
			AuthorEmail:  users[c.UserID].Email,
			AuthorID:     c.UserID,
			ChannelName:  channelNames[c.ChannelID],
			MessageTitle: messageTitles[c.MessageID],
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"comments": out, "total": total, "page": page, "limit": limit})
}

// deleteComment removes any comment (admin moderation). Banning the author is a
// separate action via PATCH /v1/admin/users/{id} (status=blocked).
func (h *AdminHandler) deleteComment(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	if err := h.Store.DeleteComment(r.Context(), r.PathValue("id")); err != nil {
		h.writeStoreErr(w, err, "could not delete comment")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
