package channel

import (
	"net/http"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// deleteMessage removes a message and everything that hangs off it (images,
// comments, deliveries).
//
// Moderator-only — the owner or a channel-admin — because moderating what the
// channel has published is exactly what a channel-admin is for, unlike the
// channel's own settings, which stay with the owner.
func (h *AppHandler) deleteMessage(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if _, ok := h.canAdminister(r, uid, channelID); !ok {
		httpx.Error(w, http.StatusForbidden, "not allowed")
		return
	}

	messageID := r.PathValue("messageId")
	msg, err := h.Store.MessageByID(r.Context(), messageID)
	// The channel check is not redundant with the permission check above: without
	// it, a moderator of one channel could delete a message belonging to another.
	if err != nil || msg.ChannelID != channelID {
		httpx.Error(w, http.StatusNotFound, "message not found")
		return
	}

	if err := h.Store.DeleteMessage(r.Context(), messageID); err != nil {
		h.writeStoreErr(w, err, "could not delete message")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
