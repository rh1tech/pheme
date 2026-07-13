package channel

import (
	"errors"
	"net/http"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// uploadChannelAvatar accepts a multipart "avatar" file and points the channel's
// AvatarID at it. The image goes through the same pipeline as message images and
// user avatars (validate, EXIF-orient, downscale, re-encode JPEG); the blob it
// replaces is removed by the store.
//
// Owner-only, like the channel's other settings: a channel-admin moderates its
// members and messages, but does not restyle the channel itself.
func (h *AppHandler) uploadChannelAvatar(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.ownsChannel(r, uid, channelID) {
		httpx.Error(w, http.StatusForbidden, "not your channel")
		return
	}
	if h.Blob == nil {
		httpx.Error(w, http.StatusServiceUnavailable, "image storage is not enabled")
		return
	}
	if !isMultipart(r) {
		httpx.Error(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.Error(w, http.StatusRequestEntityTooLarge, "image exceeds 10 MB")
			return
		}
		httpx.Error(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	files := r.MultipartForm.File["avatar"]
	if len(files) == 0 {
		httpx.Error(w, http.StatusBadRequest, "an avatar file is required")
		return
	}
	img, ok := processUpload(w, files[0], h.Blob, r)
	if !ok {
		return
	}

	ch, err := h.Store.SetChannelAvatar(r.Context(), channelID, img.ID)
	if err != nil {
		h.writeStoreErr(w, err, "could not save avatar")
		return
	}
	httpx.JSON(w, http.StatusOK, ch)
}

// deleteChannelAvatar clears the channel's avatar and removes the underlying blob.
func (h *AppHandler) deleteChannelAvatar(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	channelID := r.PathValue("id")
	if !h.ownsChannel(r, uid, channelID) {
		httpx.Error(w, http.StatusForbidden, "not your channel")
		return
	}
	ch, err := h.Store.SetChannelAvatar(r.Context(), channelID, "")
	if err != nil {
		h.writeStoreErr(w, err, "could not remove avatar")
		return
	}
	httpx.JSON(w, http.StatusOK, ch)
}
