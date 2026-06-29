package channel

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/httpx"
	"github.com/rh1tech/pheme/api/internal/imaging"
)

const (
	// maxImages is the maximum number of images attached to a single message.
	maxImages = 10
	// maxImageBytes is the largest accepted upload per image, before processing.
	maxImageBytes = 10 << 20 // 10 MiB
	// maxNotifyBytes caps the whole multipart request (images + fields + overhead).
	maxNotifyBytes = maxImages*maxImageBytes + (5 << 20)
	// multipartMemory is how much of a multipart form is buffered in memory; the
	// remainder spills to temp files (cleaned up via RemoveAll).
	multipartMemory = 16 << 20
)

// notifyInput is the parsed payload of a notify request, from either a JSON body
// (text only) or a multipart/form-data body (text + processed images).
type notifyInput struct {
	Title           string
	Body            string
	Data            map[string]string
	Images          []domain.MessageImage
	CommentsAllowed bool
}

// isMultipart reports whether the request carries a multipart/form-data body.
func isMultipart(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data")
}

// decodeNotify parses a notify request. Multipart bodies are processed for images
// (each validated, downscaled and re-encoded as JPEG, then stored in blobs);
// otherwise the legacy JSON shape {title, body, data} is decoded. It writes an
// HTTP error and returns ok=false on failure.
func decodeNotify(w http.ResponseWriter, r *http.Request, blobs blob.Store) (notifyInput, bool) {
	if !isMultipart(r) {
		var req struct {
			Title string            `json:"title"`
			Body  string            `json:"body"`
			Data  map[string]string `json:"data,omitempty"`
			// CommentsAllowed is a pointer so an absent field defaults to true
			// (comments on) rather than Go's false zero value.
			CommentsAllowed *bool `json:"commentsAllowed,omitempty"`
		}
		if !httpx.Decode(w, r, &req) {
			return notifyInput{}, false
		}
		return notifyInput{
			Title:           req.Title,
			Body:            req.Body,
			Data:            req.Data,
			CommentsAllowed: req.CommentsAllowed == nil || *req.CommentsAllowed,
		}, true
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxNotifyBytes)
	if err := r.ParseMultipartForm(multipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.Error(w, http.StatusRequestEntityTooLarge, "upload too large")
			return notifyInput{}, false
		}
		httpx.Error(w, http.StatusBadRequest, "invalid multipart form")
		return notifyInput{}, false
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()

	in := notifyInput{
		Title: r.FormValue("title"),
		Body:  r.FormValue("body"),
		// Default-on: only an explicit "false"/"0" disables comments; an absent or
		// empty field leaves them allowed.
		CommentsAllowed: !isFalsey(r.FormValue("commentsAllowed")),
	}
	if raw := strings.TrimSpace(r.FormValue("data")); raw != "" {
		var data map[string]string
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid data field (expected JSON object)")
			return notifyInput{}, false
		}
		in.Data = data
	}

	files := r.MultipartForm.File["images"]
	if len(files) > maxImages {
		httpx.Error(w, http.StatusBadRequest, "too many images (max 10)")
		return notifyInput{}, false
	}
	for _, fh := range files {
		img, ok := processUpload(w, fh, blobs, r)
		if !ok {
			return notifyInput{}, false
		}
		in.Images = append(in.Images, img)
	}
	return in, true
}

// processUpload validates one uploaded file, processes it to JPEG, stores it, and
// returns its reference. It writes an HTTP error and returns ok=false on failure.
func processUpload(w http.ResponseWriter, fh *multipart.FileHeader, blobs blob.Store, r *http.Request) (domain.MessageImage, bool) {
	if fh.Size > maxImageBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "image exceeds 10 MB")
		return domain.MessageImage{}, false
	}
	f, err := fh.Open()
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "could not read image")
		return domain.MessageImage{}, false
	}
	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil || int64(len(raw)) > maxImageBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "image exceeds 10 MB")
		return domain.MessageImage{}, false
	}
	out, width, height, err := imaging.Process(raw, imaging.MaxDim, imaging.Quality)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "unsupported or corrupt image")
		return domain.MessageImage{}, false
	}
	id, err := blobs.Put(r.Context(), out, imaging.ContentType)
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "could not store image")
		return domain.MessageImage{}, false
	}
	return domain.MessageImage{ID: id, Width: width, Height: height}, true
}

// isFalsey reports whether a form value explicitly disables a default-on flag.
func isFalsey(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return true
	default:
		return false
	}
}
