// Package blob stores and serves opaque binary objects (processed message
// images) behind a small driver interface. It ships an in-memory implementation
// for local development and tests and a MongoDB GridFS implementation for
// production. A future object-storage driver (S3/MinIO) can satisfy the same
// interface with no caller changes.
package blob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// ErrNotFound is returned when a requested blob does not exist.
var ErrNotFound = errors.New("blob not found")

// Store persists opaque binary objects keyed by an unguessable id.
type Store interface {
	// Put stores data and returns a freshly generated, unguessable id.
	Put(ctx context.Context, data []byte, contentType string) (string, error)
	// Get returns the bytes and content type for an id, or ErrNotFound.
	Get(ctx context.Context, id string) ([]byte, string, error)
	// Delete removes a blob. Deleting a missing id is not an error.
	Delete(ctx context.Context, id string) error
	// Close releases any underlying resources.
	Close(ctx context.Context) error
}

// newID returns a 32-character hex id from 16 random bytes. Using a random id
// (rather than a sequential/ObjectID) keeps the public image URL unguessable.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
