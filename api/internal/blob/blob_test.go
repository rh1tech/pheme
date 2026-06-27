package blob

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestMemoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	data := []byte("hello image bytes")

	id, err := m.Put(ctx, data, "image/jpeg")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if len(id) != 32 {
		t.Fatalf("id length = %d, want 32 (16 random bytes hex)", len(id))
	}

	got, ct, err := m.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("Get returned %q, want %q", got, data)
	}
	if ct != "image/jpeg" {
		t.Fatalf("content type = %q, want image/jpeg", ct)
	}
}

func TestMemoryDistinctIDs(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	id1, _ := m.Put(ctx, []byte("a"), "image/jpeg")
	id2, _ := m.Put(ctx, []byte("b"), "image/jpeg")
	if id1 == id2 {
		t.Fatal("expected distinct ids for separate Puts")
	}
}

func TestMemoryGetMissing(t *testing.T) {
	if _, _, err := NewMemory().Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestMemoryDelete(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	id, _ := m.Put(ctx, []byte("x"), "image/jpeg")
	if err := m.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := m.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete err = %v, want ErrNotFound", err)
	}
	// Deleting a missing id is a no-op, not an error.
	if err := m.Delete(ctx, "missing"); err != nil {
		t.Fatalf("Delete missing: %v", err)
	}
}

func TestMemoryReturnsCopies(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	src := []byte{1, 2, 3}
	id, _ := m.Put(ctx, src, "application/octet-stream")
	src[0] = 9 // mutate caller's slice after Put

	got, _, _ := m.Get(ctx, id)
	if got[0] != 1 {
		t.Fatal("stored bytes were aliased to the caller's slice")
	}
	got[1] = 8 // mutate returned slice

	again, _, _ := m.Get(ctx, id)
	if again[1] != 2 {
		t.Fatal("returned slice was aliased to stored bytes")
	}
}
