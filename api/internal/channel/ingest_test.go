package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/idempotency"
	"github.com/rh1tech/pheme/api/internal/store"
)

// The ingest endpoint: the one door into this system that is open to the internet and authenticated
// by a bearer secret rather than a session. It had no tests at all.
//
// Its job is to say no. A key for one channel must not post to another; a revoked key must stop
// working the moment it is revoked; a disabled channel must not accept anything; and an unknown
// channel must be indistinguishable from a wrong key, or the endpoint becomes a way to enumerate
// every channel on the server. Only after all of that may anything reach the queue.

type capturePublisher struct {
	tasks []domain.NotifyTask
	err   error
}

func (c *capturePublisher) Close(context.Context) error { return nil }

func (c *capturePublisher) Publish(_ context.Context, task domain.NotifyTask) error {
	if c.err != nil {
		return c.err
	}
	c.tasks = append(c.tasks, task)
	return nil
}

// denyLimiter refuses everything, standing in for a channel that has blown its rate limit.
type denyLimiter struct{}

func (denyLimiter) Allow(string) bool { return false }

type ingestFixture struct {
	h       *IngestHandler
	mux     *http.ServeMux
	pub     *capturePublisher
	channel domain.Channel
	key     string // plaintext, as a caller would hold it
}

func newIngest(t *testing.T) *ingestFixture {
	t.Helper()
	st := store.NewMemory(nil)
	pub := &capturePublisher{}
	h := &IngestHandler{Store: st, Publisher: pub, Blob: blob.NewMemory()}
	mux := http.NewServeMux()
	h.Routes(mux)

	ch, err := st.CreateChannel(context.Background(), domain.Channel{
		PublicID:  "chan-public",
		OwnerID:   "owner",
		Name:      "News",
		Status:    domain.ChannelActive,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	plaintext, hash, prefix := auth.GenerateAPIKey()
	if _, err := st.CreateAPIKey(context.Background(), domain.APIKey{
		ChannelID: ch.ID, HashedKey: hash, Prefix: prefix, Label: "test", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return &ingestFixture{h: h, mux: mux, pub: pub, channel: ch, key: plaintext}
}

// notify posts a JSON notification as the holder of apiKey would.
func (f *ingestFixture) notify(publicID, apiKey string, body any) *httptest.ResponseRecorder {
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/"+publicID+"/notify", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("X-Api-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestIngestAcceptsAndEnqueuesAValidNotification(t *testing.T) {
	f := newIngest(t)

	rec := f.notify("chan-public", f.key, map[string]any{"title": "Hello", "body": "World"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("notify = %d, want 202; body %s", rec.Code, rec.Body)
	}
	if len(f.pub.tasks) != 1 {
		t.Fatalf("enqueued %d tasks, want 1", len(f.pub.tasks))
	}
	task := f.pub.tasks[0]
	if task.ChannelID != f.channel.ID {
		t.Errorf("task channel = %q, want %q", task.ChannelID, f.channel.ID)
	}
	if task.Title != "Hello" || task.Body != "World" {
		t.Errorf("task content = %q/%q", task.Title, task.Body)
	}
	// Comments default ON when the field is absent. A pointer-less bool here would silently
	// disable comments on every notification sent by an older client.
	if !task.CommentsAllowed {
		t.Error("comments were disabled by an absent commentsAllowed field")
	}
	if task.EnqueuedAt.IsZero() {
		t.Error("the task carries no enqueue time")
	}
}

// The channel is resolved by its PUBLIC id, which is what a caller is given. Accepting the internal
// id as well would leak the difference between the two.
func TestIngestUsesThePublicIDNotTheInternalOne(t *testing.T) {
	f := newIngest(t)

	rec := f.notify(f.channel.ID, f.key, map[string]any{"title": "Hello"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("posting to the internal channel id = %d, want 401", rec.Code)
	}
}

func TestIngestRequiresAKey(t *testing.T) {
	f := newIngest(t)

	rec := f.notify("chan-public", "", map[string]any{"title": "Hello"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("notify with no key = %d, want 401", rec.Code)
	}
	if len(f.pub.tasks) != 0 {
		t.Error("an unauthenticated notification was enqueued")
	}
}

// An unknown channel and a wrong key must answer IDENTICALLY. Any difference turns this endpoint
// into a way to discover which channels exist.
func TestIngestDoesNotRevealWhetherAChannelExists(t *testing.T) {
	f := newIngest(t)

	wrongKey := f.notify("chan-public", "pk_totally-wrong", map[string]any{"title": "Hi"})
	noSuchChannel := f.notify("no-such-channel", f.key, map[string]any{"title": "Hi"})

	if wrongKey.Code != http.StatusUnauthorized {
		t.Errorf("wrong key = %d, want 401", wrongKey.Code)
	}
	if noSuchChannel.Code != http.StatusUnauthorized {
		t.Errorf("unknown channel = %d, want 401", noSuchChannel.Code)
	}
	if wrongKey.Body.String() != noSuchChannel.Body.String() {
		t.Errorf("the two answers differ:\n  wrong key: %s\n  unknown channel: %s\n"+
			"that difference enumerates the channels on this server",
			wrongKey.Body, noSuchChannel.Body)
	}
}

// A key belongs to ONE channel. If it worked on another, one customer could post into another
// customer's channel — the worst thing this endpoint could do.
func TestIngestKeyDoesNotWorkOnAnotherChannel(t *testing.T) {
	f := newIngest(t)

	other, err := f.h.Store.CreateChannel(context.Background(), domain.Channel{
		PublicID: "other-public", OwnerID: "someone-else", Name: "Theirs",
		Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create other channel: %v", err)
	}

	rec := f.notify(other.PublicID, f.key, map[string]any{"title": "intruder"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a key for one channel posted to another: %d", rec.Code)
	}
	if len(f.pub.tasks) != 0 {
		t.Error("a cross-channel notification was enqueued")
	}
}

// Revoking a key must take effect immediately. A cached or ignored RevokedAt means a leaked key
// cannot actually be turned off.
func TestIngestRefusesARevokedKey(t *testing.T) {
	f := newIngest(t)

	if rec := f.notify("chan-public", f.key, map[string]any{"title": "before"}); rec.Code != http.StatusAccepted {
		t.Fatalf("before revoking = %d", rec.Code)
	}

	keys, err := f.h.Store.APIKeysByChannel(context.Background(), f.channel.ID)
	if err != nil || len(keys) == 0 {
		t.Fatalf("read keys: %v (%d)", err, len(keys))
	}
	if err := f.h.Store.RevokeAPIKey(context.Background(), keys[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	if rec := f.notify("chan-public", f.key, map[string]any{"title": "after"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("a revoked key still works: %d — revocation would be cosmetic", rec.Code)
	}
	if len(f.pub.tasks) != 1 {
		t.Errorf("enqueued %d tasks, want only the pre-revocation one", len(f.pub.tasks))
	}
}

// A second, live key keeps working after a sibling is revoked — rotation depends on it.
func TestIngestASecondKeyStillWorksAfterTheFirstIsRevoked(t *testing.T) {
	f := newIngest(t)

	second, hash, prefix := auth.GenerateAPIKey()
	if _, err := f.h.Store.CreateAPIKey(context.Background(), domain.APIKey{
		ChannelID: f.channel.ID, HashedKey: hash, Prefix: prefix, Label: "rotated",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create second key: %v", err)
	}
	keys, _ := f.h.Store.APIKeysByChannel(context.Background(), f.channel.ID)
	for _, k := range keys {
		if k.Label == "test" {
			if err := f.h.Store.RevokeAPIKey(context.Background(), k.ID); err != nil {
				t.Fatalf("revoke: %v", err)
			}
		}
	}

	if rec := f.notify("chan-public", second, map[string]any{"title": "rotated"}); rec.Code != http.StatusAccepted {
		t.Errorf("the rotated key = %d, want 202 — key rotation would be a self-inflicted outage", rec.Code)
	}
	if rec := f.notify("chan-public", f.key, map[string]any{"title": "old"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("the revoked key = %d, want 401", rec.Code)
	}
}

// A disabled channel is authenticated but refused, and with its OWN status — the caller holds a
// valid key and deserves to know the channel is off rather than suspect their credentials.
func TestIngestRefusesADisabledChannel(t *testing.T) {
	f := newIngest(t)

	if _, err := f.h.Store.UpdateChannelStatus(context.Background(), f.channel.ID, domain.ChannelDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	rec := f.notify("chan-public", f.key, map[string]any{"title": "Hello"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled channel = %d, want 403; body %s", rec.Code, rec.Body)
	}
	if len(f.pub.tasks) != 0 {
		t.Error("a disabled channel enqueued a notification")
	}
}

// The rate limiter is checked AFTER authentication, so an unauthenticated flood cannot consume a
// real channel's budget, and it answers 429 so a caller can back off correctly.
func TestIngestHonoursTheRateLimiter(t *testing.T) {
	f := newIngest(t)
	f.h.Limiter = denyLimiter{}

	rec := f.notify("chan-public", f.key, map[string]any{"title": "Hello"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited notify = %d, want 429", rec.Code)
	}
	if len(f.pub.tasks) != 0 {
		t.Error("a rate-limited notification was enqueued")
	}

	// A bad key must not reach the limiter at all: otherwise anyone could exhaust a channel's
	// budget by guessing at its key.
	if rec := f.notify("chan-public", "pk_wrong", map[string]any{"title": "x"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("an unauthenticated request was rate-limited (%d) instead of rejected; guessing at "+
			"a key would starve the real caller", rec.Code)
	}
}

// Something must actually be delivered. An empty notification would reach every subscriber's lock
// screen as a blank alert.
func TestIngestRejectsAnEmptyNotification(t *testing.T) {
	f := newIngest(t)

	for _, body := range []map[string]any{
		{},
		{"title": "", "body": ""},
		{"title": "   ", "body": "\t\n"},
		{"data": map[string]string{"k": "v"}}, // data alone is not a notification
	} {
		rec := f.notify("chan-public", f.key, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("notify %v = %d, want 400", body, rec.Code)
		}
	}
	if len(f.pub.tasks) != 0 {
		t.Error("an empty notification was enqueued")
	}
}

// A body-only notification is legitimate — plenty of callers send no title.
func TestIngestAcceptsBodyOnlyAndTitleOnly(t *testing.T) {
	f := newIngest(t)

	if rec := f.notify("chan-public", f.key, map[string]any{"body": "just a body"}); rec.Code != http.StatusAccepted {
		t.Errorf("body-only = %d, want 202", rec.Code)
	}
	if rec := f.notify("chan-public", f.key, map[string]any{"title": "just a title"}); rec.Code != http.StatusAccepted {
		t.Errorf("title-only = %d, want 202", rec.Code)
	}
}

// commentsAllowed:false must survive the trip. It is the one field where the absent case and the
// false case mean different things.
func TestIngestCarriesCommentsAllowedThrough(t *testing.T) {
	f := newIngest(t)

	if rec := f.notify("chan-public", f.key, map[string]any{
		"title": "no comments", "commentsAllowed": false,
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("notify = %d", rec.Code)
	}
	if len(f.pub.tasks) != 1 {
		t.Fatalf("enqueued %d tasks", len(f.pub.tasks))
	}
	if f.pub.tasks[0].CommentsAllowed {
		t.Error("commentsAllowed:false was ignored")
	}
}

func TestIngestPassesDataThrough(t *testing.T) {
	f := newIngest(t)

	if rec := f.notify("chan-public", f.key, map[string]any{
		"title": "deep link", "data": map[string]string{"url": "https://example.com/1"},
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("notify = %d", rec.Code)
	}
	if got := f.pub.tasks[0].Data["url"]; got != "https://example.com/1" {
		t.Errorf("data[url] = %q, want the URL that was sent", got)
	}
}

// The Idempotency-Key header reaches the task. The worker deduplicates on it, so a header dropped
// here means a retried request sends the notification twice.
func TestIngestCarriesTheIdempotencyKey(t *testing.T) {
	f := newIngest(t)

	buf, _ := json.Marshal(map[string]any{"title": "once"})
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/chan-public/notify", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", f.key)
	req.Header.Set("Idempotency-Key", "  order-42  ")
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("notify = %d, body %s", rec.Code, rec.Body)
	}
	if got := f.pub.tasks[0].IdempotencyKey; got != "order-42" {
		t.Errorf("idempotency key = %q, want it trimmed and carried through", got)
	}
}

func TestIngestRejectsMalformedJSON(t *testing.T) {
	f := newIngest(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/chan-public/notify",
		strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", f.key)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed JSON = %d, want 400", rec.Code)
	}
}

// A broker that is down must answer 503 and NOT 202. A caller told "accepted" will not retry, and
// the notification is simply lost.
func TestIngestReportsAFailedEnqueue(t *testing.T) {
	f := newIngest(t)
	f.pub.err = context.DeadlineExceeded

	rec := f.notify("chan-public", f.key, map[string]any{"title": "Hello"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a failed enqueue answered %d, want 503 — a 202 would silently lose the notification",
			rec.Code)
	}
}

// An image alone is a valid notification, and it must be stored and referenced rather than carried
// as bytes through the queue.
func TestIngestAcceptsAMultipartImage(t *testing.T) {
	f := newIngest(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("title", "with a picture")
	part, err := mw.CreateFormFile("images", "pic.png")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/chan-public/notify", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Api-Key", f.key)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("multipart notify = %d, want 202; body %s", rec.Code, rec.Body)
	}
	if len(f.pub.tasks) != 1 || len(f.pub.tasks[0].Images) != 1 {
		t.Fatalf("task images = %+v", f.pub.tasks)
	}
	img := f.pub.tasks[0].Images[0]
	if img.ID == "" {
		t.Error("the stored image has no id")
	}
	if img.Width != 8 || img.Height != 8 {
		t.Errorf("image dimensions = %dx%d, want 8x8", img.Width, img.Height)
	}
}

// A file that is not an image must be refused rather than stored and served back to subscribers.
func TestIngestRejectsAnUploadThatIsNotAnImage(t *testing.T) {
	f := newIngest(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("images", "payload.png")
	_, _ = part.Write([]byte("this is not a picture"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/chan-public/notify", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-Api-Key", f.key)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("a non-image upload = %d, want 400", rec.Code)
	}
	if len(f.pub.tasks) != 0 {
		t.Error("a notification with an unusable image was enqueued")
	}
}

func TestIngestHealthz(t *testing.T) {
	f := newIngest(t)

	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz = %d, want 200", rec.Code)
	}
}

// Idempotency: making a retried request safe to send twice.
//
// The endpoint has always accepted an Idempotency-Key header, and the architecture document has
// always promised what that header universally means. Nothing implemented it: the key was read,
// attached to the task, carried through the broker and used by no one. A caller retrying a request
// that timed out — which is the entire reason the header exists — woke every subscriber's phone a
// second time.

// notifyWithKey posts a notification carrying an Idempotency-Key.
func (f *ingestFixture) notifyWithKey(publicID, apiKey, idemKey string, body any) *httptest.ResponseRecorder {
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/ingest/"+publicID+"/notify", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", apiKey)
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestIngestDeduplicatesARetriedRequest(t *testing.T) {
	f := newIngest(t)
	f.h.Dedup = idempotency.NewMemory()

	first := f.notifyWithKey("chan-public", f.key, "order-42", map[string]any{"title": "Shipped"})
	second := f.notifyWithKey("chan-public", f.key, "order-42", map[string]any{"title": "Shipped"})

	// Both are accepted. The caller cannot tell, and does not need to: "we already have this" and
	// "we have taken this" are the same answer for a notification.
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d and %d, want 202 for both", first.Code, second.Code)
	}
	if len(f.pub.tasks) != 1 {
		t.Errorf("a retried request enqueued %d notifications; every subscriber's phone goes off "+
			"twice for one event", len(f.pub.tasks))
	}
}

// Two different keys are two different requests. Collapsing them would silently swallow real
// notifications, which is worse than the duplicate the header exists to prevent.
func TestIngestDistinctKeysAreDistinctNotifications(t *testing.T) {
	f := newIngest(t)
	f.h.Dedup = idempotency.NewMemory()

	f.notifyWithKey("chan-public", f.key, "order-1", map[string]any{"title": "One"})
	f.notifyWithKey("chan-public", f.key, "order-2", map[string]any{"title": "Two"})

	if len(f.pub.tasks) != 2 {
		t.Errorf("two distinct keys produced %d notifications, want 2", len(f.pub.tasks))
	}
}

// Keys are scoped per channel. Two customers both numbering their orders from one must not
// silence each other.
func TestIngestIdempotencyIsScopedToTheChannel(t *testing.T) {
	f := newIngest(t)
	f.h.Dedup = idempotency.NewMemory()

	other, err := f.h.Store.CreateChannel(context.Background(), domain.Channel{
		PublicID: "other-public", OwnerID: "someone-else", Name: "Theirs",
		Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create other channel: %v", err)
	}
	plaintext, hash, prefix := auth.GenerateAPIKey()
	if _, err := f.h.Store.CreateAPIKey(context.Background(), domain.APIKey{
		ChannelID: other.ID, HashedKey: hash, Prefix: prefix, Label: "theirs",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create their key: %v", err)
	}

	f.notifyWithKey("chan-public", f.key, "order-1", map[string]any{"title": "Mine"})
	f.notifyWithKey(other.PublicID, plaintext, "order-1", map[string]any{"title": "Theirs"})

	if len(f.pub.tasks) != 2 {
		t.Errorf("the same key in two channels produced %d notifications, want 2 — one customer's "+
			"key silenced another's", len(f.pub.tasks))
	}
}

// No key means no promise. A caller that sends the same thing twice without one gets it twice,
// which is the only thing the server can honestly do.
func TestIngestWithoutAKeyDoesNotDeduplicate(t *testing.T) {
	f := newIngest(t)
	f.h.Dedup = idempotency.NewMemory()

	f.notify("chan-public", f.key, map[string]any{"title": "Same"})
	f.notify("chan-public", f.key, map[string]any{"title": "Same"})

	if len(f.pub.tasks) != 2 {
		t.Errorf("two requests with no idempotency key produced %d notifications, want 2", len(f.pub.tasks))
	}
}

// The check fails OPEN. The dedup store being unreachable is the server's problem, and refusing
// over it would turn a Redis hiccup into undelivered notifications — a duplicate is the lesser
// fault than a silence.
func TestIngestAcceptsTheRequestWhenTheDedupStoreIsDown(t *testing.T) {
	f := newIngest(t)
	f.h.Dedup = brokenDedup{}

	rec := f.notifyWithKey("chan-public", f.key, "order-7", map[string]any{"title": "Shipped"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("notify with a broken dedup store = %d, want 202", rec.Code)
	}
	if len(f.pub.tasks) != 1 {
		t.Errorf("the notification was dropped because the dedup store was unreachable (%d enqueued)",
			len(f.pub.tasks))
	}
}

type brokenDedup struct{}

func (brokenDedup) Seen(context.Context, string, time.Duration) (bool, error) {
	return false, fmt.Errorf("dedup store unavailable")
}

// With no dedup store configured the endpoint still works — it simply cannot honour the header.
func TestIngestWorksWithNoDedupStoreConfigured(t *testing.T) {
	f := newIngest(t)
	f.h.Dedup = nil

	if rec := f.notifyWithKey("chan-public", f.key, "order-9", map[string]any{"title": "x"}); rec.Code != http.StatusAccepted {
		t.Fatalf("notify = %d, want 202", rec.Code)
	}
	if len(f.pub.tasks) != 1 {
		t.Errorf("enqueued %d, want 1", len(f.pub.tasks))
	}
}
