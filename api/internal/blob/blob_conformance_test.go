package blob

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
)

// One set of assertions for both blob stores. Production stores images in GridFS; the tests
// exercised the in-memory one.
//
// Blobs are avatars and message images — user content, addressed by an id that appears in URLs. Two
// properties matter beyond "it round-trips": ids must be unguessable, because the image endpoint is
// unauthenticated and an id IS the capability to read it; and content types must survive, because a
// browser told the wrong one will refuse to render the image or, worse, execute it.
//
// The GridFS half is skipped unless PHEME_TEST_MONGO_URI is set:
//
//	docker run -d --rm -p 27117:27017 mongo:7
//	PHEME_TEST_MONGO_URI=mongodb://localhost:27117 go test ./internal/blob/

func eachBlobStore(t *testing.T, fn func(t *testing.T, s Store)) {
	t.Helper()

	t.Run("memory", func(t *testing.T) { fn(t, NewMemory()) })

	uri := os.Getenv("PHEME_TEST_MONGO_URI")
	if uri == "" {
		t.Log("PHEME_TEST_MONGO_URI not set — skipping the implementation that runs in production")
		return
	}
	t.Run("gridfs", func(t *testing.T) {
		ctx := context.Background()
		// t.Name() is a PATH — "Parent/child" — and Mongo refuses a database name containing a
		// slash. Anything outside [A-Za-z0-9_] becomes an underscore.
		db := sanitiseDBName("phemeblob_" + t.Name())
		// A small pool: this opens a client per subtest, and the default of 100 sockets each is
		// what put a shared host's mongod under strain.
		s, err := NewGridFS(ctx, poolLimited(t, uri), db)
		if err != nil {
			t.Fatalf("connect to gridfs: %v", err)
		}
		t.Cleanup(func() { _ = s.Close(context.Background()) })
		fn(t, s)
	})
}

// poolLimited adds this suite's connection settings without assuming the URI carries none of its
// own. This was plain concatenation, which works for the bare host:port CI provides and breaks for
// everything else: an authenticated URI already ends in "/?authSource=admin", and appending to that
// produced "…/?authSource=admin/?maxPoolSize=4", which the driver reports as an authentication
// failure against a database named "admin/?maxPoolSize=4". So the GridFS half — the implementation
// production actually runs — could not be exercised locally at all, and misdirected anyone who
// tried. The store package's conformance harness had the identical fault.
func poolLimited(t *testing.T, uri string) string {
	t.Helper()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatalf("PHEME_TEST_MONGO_URI is not a URI: %v", err)
	}
	q := u.Query()
	// A small pool: this opens a client per subtest, and the default of 100 sockets each is what
	// put a shared host's mongod under strain. Set rather than appended, so a caller's own value is
	// replaced instead of duplicated.
	q.Set("maxPoolSize", "4")
	q.Set("serverSelectionTimeoutMS", "5000")
	u.RawQuery = q.Encode()
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

func TestBlobConformance_RoundTrip(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		want := []byte("\x89PNG\r\n\x1a\n fake image bytes")

		id, err := s.Put(ctx, want, "image/png")
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		if id == "" {
			t.Fatal("Put returned an empty id")
		}

		got, contentType, err := s.Get(ctx, id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("got %d bytes back, want the %d that went in", len(got), len(want))
		}
		// A browser told the wrong type refuses to render the image — or renders something it
		// should not.
		if contentType != "image/png" {
			t.Errorf("contentType = %q, want image/png", contentType)
		}
	})
}

// The image endpoint is UNAUTHENTICATED: the id is the capability. Sequential or short ids would
// let anyone walk the whole store.
func TestBlobConformance_IDsAreUnguessable(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		seen := map[string]bool{}

		for i := 0; i < 20; i++ {
			id, err := s.Put(ctx, []byte("same bytes every time"), "image/png")
			if err != nil {
				t.Fatalf("put %d: %v", i, err)
			}
			if seen[id] {
				t.Fatalf("Put returned the id %q twice; identical content must not collide", id)
			}
			seen[id] = true
			if len(id) < 16 {
				t.Errorf("id %q is only %d characters — an id IS the capability to read the blob, "+
					"and the endpoint serving it is unauthenticated", id, len(id))
			}
		}
	})
}

func TestBlobConformance_MissingIsNotFound(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		// Not empty bytes with a nil error: a caller would serve a zero-length image as though it
		// were real.
		if _, _, err := s.Get(ctx, "definitely-not-a-blob"); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get on a missing id = %v, want ErrNotFound", err)
		}
	})
}

func TestBlobConformance_DeleteRemovesAndIsIdempotent(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		id, err := s.Put(ctx, []byte("bytes"), "image/jpeg")
		if err != nil {
			t.Fatalf("put: %v", err)
		}

		if err := s.Delete(ctx, id); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("a deleted blob is still readable: %v", err)
		}
		// Deleting a missing id is not an error — cascade deletes run over ids that may already be
		// gone, and failing there would abort the rest of the cascade.
		if err := s.Delete(ctx, id); err != nil {
			t.Errorf("deleting twice = %v, want nil", err)
		}
		if err := s.Delete(ctx, "never-existed"); err != nil {
			t.Errorf("deleting an unknown id = %v, want nil", err)
		}
	})
}

// Deleting one blob must not disturb another. A cascade that took its neighbours with it would
// silently empty a channel's history.
func TestBlobConformance_DeleteIsScopedToOneBlob(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		keep, err := s.Put(ctx, []byte("keep me"), "image/png")
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		gone, err := s.Put(ctx, []byte("delete me"), "image/png")
		if err != nil {
			t.Fatalf("put: %v", err)
		}

		if err := s.Delete(ctx, gone); err != nil {
			t.Fatalf("delete: %v", err)
		}
		got, _, err := s.Get(ctx, keep)
		if err != nil {
			t.Fatalf("the surviving blob is unreadable: %v", err)
		}
		if string(got) != "keep me" {
			t.Errorf("the surviving blob came back as %q", got)
		}
	})
}

// Empty content is a real input — a zero-byte upload — and must round-trip rather than being
// mistaken for absence.
func TestBlobConformance_EmptyContentRoundTrips(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		id, err := s.Put(ctx, []byte{}, "image/png")
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		got, ct, err := s.Get(ctx, id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d bytes, want 0", len(got))
		}
		if ct != "image/png" {
			t.Errorf("contentType = %q, want it preserved even for empty content", ct)
		}
	})
}

// Something the size of a real photo, not a token. A store that works on twenty bytes and truncates
// at a megabyte would pass every other test here.
func TestBlobConformance_LargeContentRoundTrips(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		want := make([]byte, 2*1024*1024)
		for i := range want {
			want[i] = byte(i % 251)
		}

		id, err := s.Put(ctx, want, "image/jpeg")
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		got, _, err := s.Get(ctx, id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("a %d-byte blob came back as %d bytes", len(want), len(got))
		}
	})
}

// Uploads happen concurrently — several images in one message, several people at once — and each
// must get its own id and its own bytes.
func TestBlobConformance_ConcurrentPutsDoNotCollide(t *testing.T) {
	eachBlobStore(t, func(t *testing.T, s Store) {
		ctx := context.Background()
		const n = 8

		var wg sync.WaitGroup
		ids := make([]string, n)
		errs := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ids[i], errs[i] = s.Put(ctx, []byte{byte(i)}, "image/png")
			}(i)
		}
		wg.Wait()

		seen := map[string]bool{}
		for i, id := range ids {
			if errs[i] != nil {
				t.Fatalf("concurrent put %d: %v", i, errs[i])
			}
			if seen[id] {
				t.Fatalf("two concurrent puts produced the same id %q", id)
			}
			seen[id] = true

			got, _, err := s.Get(ctx, id)
			if err != nil {
				t.Fatalf("get %d: %v", i, err)
			}
			if len(got) != 1 || got[0] != byte(i) {
				t.Errorf("blob %d came back as %v — concurrent puts crossed their contents", i, got)
			}
		}
	})
}

// sanitiseDBName turns a test name into something Mongo will accept as a database.
func sanitiseDBName(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return string(out)
}
