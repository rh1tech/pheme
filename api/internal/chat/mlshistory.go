package chat

import (
	"io"
	"net/http"

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
	_, _, _, ok := h.requireMember(w, r)
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
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": blobID})
}

// getHistory hands a sealed transcript blob to the joining device, then deletes it. One-shot: the
// handoff exists only until the requester fetches it, so ephemeral sync blobs never accumulate. A
// requester that needs a retry simply re-requests, producing a fresh offer.
func (h *Handler) getHistory(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := h.requireMember(w, r)
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
	data, _, err := h.Blobs.Get(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "history not found")
		return
	}
	// Best-effort delete after the read — the handoff is consumed.
	_ = h.Blobs.Delete(r.Context(), id)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
