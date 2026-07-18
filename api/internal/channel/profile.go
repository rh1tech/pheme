package channel

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/store"
)

// me returns the authenticated user's own account, including profile fields.
func (h *AppHandler) me(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	u, err := h.Store.UserByID(r.Context(), uid)
	if err != nil {
		h.writeStoreErr(w, err, "could not load profile")
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

// Every field is a pointer so that ABSENT and BLANK stay different requests. A client that sends
// only the field it changed must not have the rest cleared underneath it — which is exactly what
// happened to anyone who used the notification-privacy setting on mobile: it PATCHes that one
// field, and the display name, bio, phone and website went with it.
type updateProfileRequest struct {
	Username            *string `json:"username"`
	DisplayName         *string `json:"displayName"`
	Bio                 *string `json:"bio"`
	Phone               *string `json:"phone"`
	Website             *string `json:"website"`
	NotificationPrivacy *string `json:"notificationPrivacy"`
}

// deref returns the trimmed value of an optional field, and whether it was supplied at all.
func deref(v *string) (string, bool) {
	if v == nil {
		return "", false
	}
	return strings.TrimSpace(*v), true
}

// maxBioLen / maxFieldLen bound free-text profile fields so a single document
// stays small and the UI predictable.
const (
	maxBioLen   = 500
	maxFieldLen = 200
)

// updateProfile edits the caller's username and contact fields. Username is
// optional; when non-empty it is validated and must be unique system-wide
// (case-insensitively). An empty username clears any existing handle.
func (h *AppHandler) updateProfile(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	var req updateProfileRequest
	if !httpx.Decode(w, r, &req) {
		return
	}
	username, usernameGiven := deref(req.Username)
	if username != "" {
		if err := domain.ValidateUsername(username); err != nil {
			httpx.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		// Pre-check for a clearer 409 than the store's duplicate-key backstop.
		if existing, err := h.Store.UserByUsername(r.Context(), strings.ToLower(username)); err == nil && existing.ID != uid {
			httpx.Error(w, http.StatusConflict, "that username is already taken")
			return
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusInternalServerError, "could not update profile")
			return
		}
	}
	bio, _ := deref(req.Bio)
	displayName, displayNameGiven := deref(req.DisplayName)
	phone, _ := deref(req.Phone)
	website, _ := deref(req.Website)
	if len(bio) > maxBioLen {
		httpx.Error(w, http.StatusBadRequest, "bio is too long")
		return
	}
	if len(displayName) > maxFieldLen || len(phone) > maxFieldLen || len(website) > maxFieldLen {
		httpx.Error(w, http.StatusBadRequest, "a profile field is too long")
		return
	}
	// An account has to be callable something, so refuse to clear the last name it has. Only when
	// the caller actually ASKED to blank the display name — a partial update that never mentions it
	// is not a request to erase anything, which is the whole reason these fields are pointers now.
	if displayNameGiven && displayName == "" {
		clearingUsername := usernameGiven && username == ""
		current, err := h.Store.UserByID(r.Context(), uid)
		if err != nil {
			h.writeStoreErr(w, err, "could not update profile")
			return
		}
		keepsUsername := current.Username != "" && !clearingUsername
		if username == "" && !keepsUsername {
			httpx.Error(w, http.StatusBadRequest, "choose a display name or a username")
			return
		}
	}
	// Reject non-http(s) website values so a client rendering the profile link
	// cannot be tricked into a javascript:/data: scheme (stored-XSS guard).
	if site := website; site != "" {
		u, perr := url.Parse(site)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") {
			httpx.Error(w, http.StatusBadRequest, "website must be an http or https URL")
			return
		}
	}
	// Reject an unrecognised value rather than storing it. A setting this server
	// cannot interpret would be read back as the zero value — which is the most
	// revealing option — so a typo would quietly turn a user's lock screen on.
	var privacy *domain.NotificationPrivacy
	if req.NotificationPrivacy != nil {
		p := domain.NotificationPrivacy(strings.TrimSpace(*req.NotificationPrivacy))
		if !p.Valid() {
			httpx.Error(w, http.StatusBadRequest, "unknown notification privacy setting")
			return
		}
		privacy = &p
	}
	u, err := h.Store.UpdateUserProfile(r.Context(), uid, domain.UserProfileUpdate{
		Username:            req.Username,
		DisplayName:         req.DisplayName,
		Bio:                 req.Bio,
		Phone:               req.Phone,
		Website:             req.Website,
		NotificationPrivacy: privacy,
	})
	if err != nil {
		if errors.Is(err, store.ErrUsernameTaken) {
			httpx.Error(w, http.StatusConflict, "that username is already taken")
			return
		}
		h.writeStoreErr(w, err, "could not update profile")
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

// uploadAvatar accepts a multipart "avatar" file, processes it through the same
// pipeline as message images (validate, EXIF-orient, downscale, re-encode JPEG),
// stores it in the blob store and points the user's AvatarID at it. The previous
// avatar blob, if any, is removed by the store.
func (h *AppHandler) uploadAvatar(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
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
	u, err := h.Store.SetUserAvatar(r.Context(), uid, img.ID)
	if err != nil {
		h.writeStoreErr(w, err, "could not save avatar")
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}

// deleteAvatar clears the caller's avatar and removes the underlying blob.
func (h *AppHandler) deleteAvatar(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	u, err := h.Store.SetUserAvatar(r.Context(), uid, "")
	if err != nil {
		h.writeStoreErr(w, err, "could not remove avatar")
		return
	}
	httpx.JSON(w, http.StatusOK, u)
}
