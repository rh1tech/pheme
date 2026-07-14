package chat

import (
	"errors"
	"io"
	"net/http"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
)

// maxAttachmentBytes caps one uploaded photo.
//
// It is the size of the CIPHERTEXT, which is what the server actually receives — the client is
// expected to downscale before it seals, and this is a backstop rather than a policy. 10 MiB is
// generous for a photo that has been resized to fit a phone screen, and small enough that a member
// cannot use a conversation as free object storage.
const maxAttachmentBytes = 10 << 20

// uploadAttachment stores one encrypted photo and returns its id.
//
// -----------------------------------------------------------------------------------------------
// THE SERVER NEVER SEES THE PHOTO. What arrives here is AES-GCM ciphertext, sealed by the sender
// under a key that exists in exactly one place: inside the MLS-encrypted message that references
// this attachment. So the server stores a blob it cannot open, hands it back to members who ask,
// and learns nothing but a size.
//
// Which is also why the content type is not taken from the request and not stored. The real one —
// image/jpeg, image/png — is a property of the plaintext, and it lives inside the encrypted message
// alongside the key. Accepting a content type here would let the server (and anyone reading its
// logs) learn something about a photo it is not supposed to know, and would tempt a future handler
// into serving it back with that type, which is how an "encrypted" blob store starts sniffing.
// -----------------------------------------------------------------------------------------------
func (h *Handler) uploadAttachment(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Blobs == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "attachments are not available")
		return
	}

	// Read one byte past the cap, so an over-size body is refused rather than silently truncated
	// into a blob that will never decrypt.
	body := http.MaxBytesReader(w, r.Body, maxAttachmentBytes+1)
	data, err := io.ReadAll(body)
	if err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "that photo is too large")
		return
	}
	if len(data) == 0 {
		httpx.Error(w, http.StatusBadRequest, "empty attachment")
		return
	}
	if len(data) > maxAttachmentBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "that photo is too large")
		return
	}

	// Opaque, always. See the note above.
	blobID, err := h.Blobs.Put(r.Context(), data, "application/octet-stream")
	if err != nil {
		h.logger().Error("attachment: store", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "could not store that photo")
		return
	}

	att := domain.Attachment{
		ID:             blobID,
		ConversationID: convID,
		Size:           len(data),
	}
	if err := h.Store.CreateAttachment(r.Context(), att); err != nil {
		// The blob is orphaned rather than left dangling in a conversation nobody can prove it
		// belongs to — without the record, the download handler could not authorise it anyway.
		_ = h.Blobs.Delete(r.Context(), blobID)
		h.logger().Error("attachment: record", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "could not store that photo")
		return
	}

	httpx.JSON(w, http.StatusCreated, map[string]string{"id": blobID})
}

// getAttachment returns one encrypted photo to a member of its conversation.
//
// Membership is checked even though the bytes are useless without a key that never reaches the
// server. That is deliberate: a public endpoint would let anyone who learned an id confirm the photo
// exists, measure it, and pull it repeatedly at our expense. The encryption makes the CONTENT safe;
// it does not make the endpoint free.
func (h *Handler) getAttachment(w http.ResponseWriter, r *http.Request) {
	_, convID, _, ok := h.requireMember(w, r)
	if !ok {
		return
	}
	if h.Blobs == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "attachments are not available")
		return
	}

	attID := r.PathValue("attachmentId")

	// The record is what binds a blob to a conversation. Without this check, any member of ANY
	// conversation could fetch any attachment id — membership of the conversation in the URL would
	// be satisfied while the blob belonged to a completely different one.
	att, err := h.Store.GetAttachment(r.Context(), attID)
	if err != nil || att.ConversationID != convID {
		httpx.Error(w, http.StatusNotFound, "no such photo")
		return
	}

	data, _, err := h.Blobs.Get(r.Context(), attID)
	if errors.Is(err, blob.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "no such photo")
		return
	}
	if err != nil {
		h.logger().Error("attachment: fetch", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "could not fetch that photo")
		return
	}

	// Opaque bytes, served as opaque bytes. The server does not know what this is and must not
	// suggest that it does.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// deleteConversationAttachments removes every photo in a conversation. Called when the conversation
// itself is deleted: the messages go, and an encrypted blob whose key went with them is landfill.
func (h *Handler) deleteConversationAttachments(r *http.Request, convID string) {
	if h.Blobs == nil {
		return
	}

	ids, err := h.Store.ListAttachmentIDs(r.Context(), convID)
	if err != nil {
		h.logger().Error("attachment: list for delete", "error", err)
		return
	}
	for _, id := range ids {
		if err := h.Blobs.Delete(r.Context(), id); err != nil {
			h.logger().Error("attachment: delete blob", "id", id, "error", err)
		}
	}
	if err := h.Store.DeleteAttachments(r.Context(), convID); err != nil {
		h.logger().Error("attachment: delete records", "error", err)
	}
}
