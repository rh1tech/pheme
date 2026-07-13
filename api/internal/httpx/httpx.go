// Package httpx provides small HTTP helpers shared by the Pheme API servers.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "error", err)
	}
}

// Error writes a JSON error envelope: {"error": {"message": msg}}.
func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]any{"error": map[string]string{"message": msg}})
}

// DefaultMaxBodyBytes bounds any JSON request body decoded through Decode.
//
// It is deliberately the DEFAULT rather than an opt-in: an unbounded decode is a
// memory-exhaustion vector, and the endpoints most exposed to it are the
// unauthenticated ones (register, login, password reset), which are exactly the ones
// nobody remembers to opt in. Handlers that legitimately carry more — a batch of MLS
// KeyPackages, say — call DecodeLimited with their own ceiling. A megabyte is orders
// of magnitude beyond what any JSON body here needs.
const DefaultMaxBodyBytes = 1 << 20

// Decode reads a JSON request body into v, returning false (and writing a 400)
// on failure. The body is capped at DefaultMaxBodyBytes.
func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return DecodeLimited(w, r, v, DefaultMaxBodyBytes)
}

// DecodeLimited is Decode with a hard ceiling on the request body.
//
// Any size check made after decoding is too late: the decoder has already
// buffered the whole body into memory, so an oversized request costs the server
// the allocation before it is rejected. This caps the read itself.
func DecodeLimited(w http.ResponseWriter, r *http.Request, v any, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			Error(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		Error(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// Binary writes raw bytes with the given content type. Blob ids are content-
// stable, so processed images are served immutable and long-cached.
func Binary(w http.ResponseWriter, contentType string, data []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.Error("write binary response", "error", err)
	}
}

// Health returns a handler reporting service liveness.
func Health(service string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"status": "ok", "service": service})
	}
}
