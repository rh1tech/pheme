package chat

import (
	"io"
	"net/http"
	"strings"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// A history handoff is a device's decrypted transcript for ONE conversation, sealed under a key
// derived from that conversation's MLS group (which the server cannot derive) and handed to a
// device that just joined and holds none of the past. It is text plus photo metadata, never the
// photos — so the cap matches the transcript backup's, and the server only ever sees ciphertext.
const maxHistoryBlobBytes = 128 * 1024 * 1024

// uploadHistory stores a sealed transcript blob for a joining device to fetch. Members only —
// the whole point is that only someone entitled to be in the conversation can offer, or later
// fetch, its history. The bytes are opaque: sealed under a group-derived key off the server.
func (h *Handler) uploadHistory(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Blobs == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "history sync is not available")
		return
	}

	// One byte past the cap, so an over-size body is refused rather than truncated into a blob
	// that will never open.
	body := http.MaxBytesReader(w, r.Body, maxHistoryBlobBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "history too large")
		return
	}
	if len(data) == 0 {
		httpx.Error(w, http.StatusBadRequest, "empty history")
		return
	}
	if len(data) > maxHistoryBlobBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "history too large")
		return
	}

	blobID, err := h.Blobs.Put(r.Context(), data, "application/octet-stream")
	if err != nil {
		h.logger().Error("history: store", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "could not store history")
		return
	}
	// The id names the conversation it belongs to, so the fetch can prove the blob is this
	// conversation's rather than merely that the caller is in SOME conversation. See historyID.
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": historyID(convID, blobID)})
}

// historyID binds a history blob to its conversation.
//
// A blob store has no notion of who owns what — Get takes an id and returns bytes. So without
// something tying the blob to a conversation, membership of the conversation in the URL is
// satisfied while the blob belongs to a completely different one, and a member of ANY conversation
// who learned an id could fetch another conversation's handoff. Worse than read: the fetch DELETES
// the blob, so it would also destroy a handoff the rightful device had not collected yet.
//
// Attachments solve this with a stored record and an explicit att.ConversationID != convID check.
// History has no such record — nothing is persisted at upload but the bytes — so the binding lives
// in the id instead. The id is opaque to clients: both of them pass it straight back in a URL path
// and never parse it.
func historyID(convID, blobID string) string { return convID + "." + blobID }

// splitHistoryID returns the blob id if the history id belongs to convID.
func splitHistoryID(convID, id string) (string, bool) {
	prefix := convID + "."
	if !strings.HasPrefix(id, prefix) {
		return "", false
	}
	blobID := strings.TrimPrefix(id, prefix)
	if blobID == "" {
		return "", false
	}
	return blobID, true
}

// getHistory hands a sealed transcript blob to the joining device, then deletes it. One-shot: the
// handoff exists only until the requester fetches it, so ephemeral sync blobs never accumulate. A
// requester that needs a retry simply re-requests, producing a fresh offer.
func (h *Handler) getHistory(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Blobs == nil {
		httpx.Error(w, http.StatusNotFound, "history not found")
		return
	}
	id := r.PathValue("historyId")
	if id == "" {
		httpx.Error(w, http.StatusBadRequest, "history id is required")
		return
	}
	// The blob must belong to THIS conversation. requireMember proved the caller is in the
	// conversation named in the URL; it says nothing about where the blob came from.
	//
	// An id minted before this binding existed no longer resolves. That is deliberate and
	// self-healing: a handoff is one-shot and short-lived, and a fetch that fails simply produces
	// a fresh offer, which is the same path a retry has always taken.
	blobID, ok := splitHistoryID(convID, id)
	if !ok {
		httpx.Error(w, http.StatusNotFound, "history not found")
		return
	}
	data, _, err := h.Blobs.Get(r.Context(), blobID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "history not found")
		return
	}
	// Best-effort delete after the read — the handoff is consumed.
	_ = h.Blobs.Delete(r.Context(), blobID)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
