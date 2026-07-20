package httpx

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The helpers every handler in this API is built on: how a body is read, how big it is allowed to
// be, and what an error looks like on the wire. Nothing here had a test.
//
// The body limit is the part that matters most. It is the DEFAULT rather than an opt-in precisely
// because the endpoints most exposed to an unbounded decode are the unauthenticated ones — register,
// login, password reset — which are exactly the ones nobody remembers to opt in. And it caps the
// READ rather than checking a size afterwards: a check made after decoding is too late, because the
// decoder has already buffered the whole body into memory, which is the allocation the attacker
// wanted.

func decodeInto(t *testing.T, body string, v any, limit int64) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	ok := DecodeLimited(rec, req, v, limit)
	return rec, ok
}

func TestDecodeReadsAValidBody(t *testing.T) {
	var out struct {
		Email string `json:"email"`
		Age   int    `json:"age"`
	}
	rec, ok := decodeInto(t, `{"email":"a@b.test","age":7}`, &out, DefaultMaxBodyBytes)
	if !ok {
		t.Fatalf("decode refused a valid body: %s", rec.Body)
	}
	if out.Email != "a@b.test" || out.Age != 7 {
		t.Errorf("decoded %+v", out)
	}
}

func TestDecodeRejectsMalformedJSON(t *testing.T) {
	for _, body := range []string{"", "{", `{"email":}`, "not json at all", `{"age":"seven"}`} {
		var out struct {
			Age int `json:"age"`
		}
		rec, ok := decodeInto(t, body, &out, DefaultMaxBodyBytes)
		if ok {
			t.Errorf("decode accepted %q", body)
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("decode of %q answered %d, want 400", body, rec.Code)
		}
	}
}

// countingBody records how many bytes were actually pulled off the wire.
type countingBody struct {
	r    io.Reader
	read int
}

func (c *countingBody) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

func (c *countingBody) Close() error { return nil }

// THE ONE THAT MATTERS. An oversized body is refused by the READ, not after it has been buffered.
//
// This is asserted by counting the bytes actually pulled off the wire, because that is the only
// observable difference. An earlier version checked the status code and that the value had not
// been populated — and passed against a deliberate implementation that read the whole body into
// memory and only then compared its length, which is precisely the allocation the cap exists to
// prevent. Both versions answer 413 and leave the value empty; only one of them declines to read
// the megabytes.
func TestDecodeCapsTheBodyRatherThanCheckingAfterwards(t *testing.T) {
	const limit = 512
	// Comfortably over the limit, and valid JSON, so the only reason to refuse it is its size.
	huge := `{"blob":"` + strings.Repeat("x", limit*8) + `"}`

	body := &countingBody{r: strings.NewReader(huge)}
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Body = body
	rec := httptest.NewRecorder()

	var out struct {
		Blob string `json:"blob"`
	}
	if DecodeLimited(rec, req, &out, limit) {
		t.Fatal("a body several times the limit was accepted")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized body answered %d, want 413 — a 400 tells the caller their JSON is "+
			"malformed when the problem is its size", rec.Code)
	}

	// A little slack: the reader is allowed to notice it has gone past the limit, and reads happen
	// in buffered chunks. What it must not do is consume the whole body.
	if int64(body.read) > limit*2 {
		t.Errorf("%d bytes of a %d-byte body were read against a %d-byte limit; the limit is a "+
			"check made after the allocation rather than a cap on it", body.read, len(huge), limit)
	}
}

// A body exactly at the limit is allowed. An off-by-one here would refuse legitimate requests at
// a size the documentation says is fine.
func TestDecodeAllowsABodyAtTheLimit(t *testing.T) {
	var out struct {
		V string `json:"v"`
	}
	body := `{"v":"` + strings.Repeat("y", 20) + `"}`
	rec, ok := decodeInto(t, body, &out, int64(len(body)))
	if !ok {
		t.Errorf("a body exactly at the limit (%d bytes) was refused: %s", len(body), rec.Body)
	}
}

// Decode applies the default ceiling without being asked. This is the property that protects the
// endpoints nobody remembered to opt in.
func TestDecodeAppliesTheDefaultCeiling(t *testing.T) {
	var out struct {
		Blob string `json:"blob"`
	}
	huge := `{"blob":"` + strings.Repeat("z", DefaultMaxBodyBytes+1024) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(huge))
	rec := httptest.NewRecorder()

	if Decode(rec, req, &out) {
		t.Fatal("Decode accepted a body over the default ceiling; every endpoint that did not opt " +
			"in to a limit is an unbounded allocation")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("answered %d, want 413", rec.Code)
	}
}

// The error envelope is a contract: clients read error.message. A change in shape would break
// every error display in both clients at once.
func TestErrorEnvelopeShape(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, http.StatusForbidden, "not allowed")

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q, want application/json", ct)
	}
	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("the error envelope is not JSON: %v (%s)", err, rec.Body)
	}
	if got.Error.Message != "not allowed" {
		t.Errorf("error.message = %q, want the message given", got.Error.Message)
	}
}

// A failed decode writes that same envelope, so a client parsing errors does not need a special
// case for "the body was wrong".
func TestDecodeFailureWritesTheErrorEnvelope(t *testing.T) {
	var out struct{}
	rec, _ := decodeInto(t, "{oops", &out, DefaultMaxBodyBytes)

	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("a decode failure did not produce the error envelope: %s", rec.Body)
	}
	if got.Error.Message == "" {
		t.Error("the envelope carried no message")
	}
}

func TestJSONWritesStatusAndBody(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, http.StatusCreated, map[string]string{"id": "abc"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != "abc" {
		t.Errorf("body = %v", got)
	}
}

// A nil value writes the status and nothing else — used for 204-style answers that still want the
// JSON content type.
func TestJSONWithNilWritesNoBody(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, http.StatusNoContent, nil)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

// Binary serves images. The cache headers are the whole reason it exists separately: blob ids are
// content-stable, so a processed image can be cached forever, and getting this wrong means every
// avatar is refetched on every page load.
func TestBinarySetsLengthAndImmutableCaching(t *testing.T) {
	data := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'j', 'p', 'e', 'g'}
	rec := httptest.NewRecorder()
	Binary(rec, "image/jpeg", data)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("content type = %q", ct)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(data)) {
		t.Errorf("content length = %q, want %d", got, len(data))
	}
	cache := rec.Header().Get("Cache-Control")
	if !strings.Contains(cache, "immutable") || !strings.Contains(cache, "max-age=") {
		t.Errorf("cache control = %q; without immutable caching every avatar is refetched on every "+
			"page load", cache)
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Error("the bytes came back changed")
	}
}

// Health answers with a status code and nothing else.
//
// It used to return {"status":"ok","service":"app"}, so that an operator hitting
// one proxy could tell WHICH process answered. That cost is real but small: app
// and ingest sit on different upstream ports behind different nginx locations,
// so the URL you asked for already told you which one replied. What the body
// bought in return was a single unauthenticated request that identified the host
// as a Pheme deployment — the cheapest possible signal for anyone assembling a
// blocklist. The status code carries the liveness; the body only carried the
// giveaway.
func TestHealthAnswersWithoutIdentifyingTheService(t *testing.T) {
	rec := httptest.NewRecorder()
	Health()(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.Bytes(); len(body) != 0 {
		t.Errorf("health body = %q, want empty — a body here fingerprints the host", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want unset", ct)
	}
}

// Decode must not leave the body half-read for a handler that goes on to read it — and must not
// panic on a request with no body at all, which is what a GET routed here would look like.
func TestDecodeOnAnEmptyBodyIsARejectionNotAPanic(t *testing.T) {
	var out struct{}
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	rec := httptest.NewRecorder()

	if DecodeLimited(rec, req, &out, DefaultMaxBodyBytes) {
		t.Error("decode accepted a request with no body")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if _, err := io.ReadAll(req.Body); err != nil && err != io.EOF {
		t.Errorf("the body is unreadable afterwards: %v", err)
	}
}
