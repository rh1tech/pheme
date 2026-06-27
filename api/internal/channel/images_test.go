package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// makePNG returns a PNG-encoded test image of the given size.
func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 64, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// multipartNotify builds a multipart/form-data body with optional text fields and
// any number of image parts (each PNG bytes). It returns the body and content type.
func multipartNotify(t *testing.T, title, body string, images ...[]byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if title != "" {
		_ = mw.WriteField("title", title)
	}
	if body != "" {
		_ = mw.WriteField("body", body)
	}
	for i, img := range images {
		fw, err := mw.CreateFormFile("images", "photo"+string(rune('0'+i))+".png")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write(img); err != nil {
			t.Fatalf("write image: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}

func (f *appFixture) doRaw(method, path, token, contentType string, body *bytes.Buffer) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// drainTask reads the single task enqueued by a notify call (publisher and
// consumer share the same in-memory broker instance).
func (f *appFixture) drainTask(t *testing.T) domain.NotifyTask {
	t.Helper()
	out := make(chan domain.NotifyTask, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		_ = f.pub.Consume(ctx, func(_ context.Context, task domain.NotifyTask) error {
			out <- task
			return nil
		})
	}()
	select {
	case task := <-out:
		return task
	case <-ctx.Done():
		t.Fatal("no task enqueued")
		return domain.NotifyTask{}
	}
}

func (f *appFixture) newChannel(t *testing.T, token, name string) domain.Channel {
	t.Helper()
	rec := f.do(http.MethodPost, "/v1/channels", token, map[string]any{"name": name})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", rec.Code, rec.Body)
	}
	var ch domain.Channel
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode channel: %v", err)
	}
	return ch
}

func TestNotifyWithImageProcessesStoresAndServes(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	ch := f.newChannel(t, token, "Photos")

	body, ct := multipartNotify(t, "Trip", "By the lake", makePNG(t, 1600, 900))
	rec := f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", token, ct, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("notify status = %d, want 202; body=%s", rec.Code, rec.Body)
	}

	task := f.drainTask(t)
	if len(task.Images) != 1 {
		t.Fatalf("task images = %d, want 1", len(task.Images))
	}
	img := task.Images[0]
	if img.Width != 1000 || img.Height < 560 || img.Height > 564 {
		t.Fatalf("processed dims = %dx%d, want 1000x~562", img.Width, img.Height)
	}

	// The processed JPEG is served publicly (no bearer token).
	rec = f.doRaw(http.MethodGet, "/v1/images/"+img.ID, "", "application/octet-stream", &bytes.Buffer{})
	if rec.Code != http.StatusOK {
		t.Fatalf("serve image status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("content type = %q, want image/jpeg", got)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(rec.Body.Bytes())); err != nil || format != "jpeg" {
		t.Fatalf("served body not a jpeg (format=%q err=%v)", format, err)
	}
}

func TestNotifyImageOnlyAllowed(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	ch := f.newChannel(t, token, "Photos")

	body, ct := multipartNotify(t, "", "", makePNG(t, 200, 200))
	rec := f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", token, ct, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("image-only notify status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
}

func TestNotifyMultipartEmptyRejected(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	ch := f.newChannel(t, token, "Photos")

	body, ct := multipartNotify(t, "", "")
	rec := f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", token, ct, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty multipart status = %d, want 400", rec.Code)
	}
}

func TestNotifyTooManyImagesRejected(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	ch := f.newChannel(t, token, "Photos")

	imgs := make([][]byte, maxImages+1)
	for i := range imgs {
		imgs[i] = makePNG(t, 64, 64)
	}
	body, ct := multipartNotify(t, "many", "", imgs...)
	rec := f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", token, ct, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("too-many-images status = %d, want 400", rec.Code)
	}
}

func TestNotifyRejectsCorruptImage(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	ch := f.newChannel(t, token, "Photos")

	body, ct := multipartNotify(t, "", "", []byte("this is not an image"))
	rec := f.doRaw(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", token, ct, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("corrupt image status = %d, want 400", rec.Code)
	}
}

func TestNotifyJSONTextStillWorks(t *testing.T) {
	f := newAppFixture(t)
	token, _ := f.tokenFor(t, "owner@b.com")
	ch := f.newChannel(t, token, "Text")

	rec := f.do(http.MethodPost, "/v1/channels/"+ch.ID+"/notify", token,
		map[string]any{"title": "Hello", "body": "World"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("json notify status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	task := f.drainTask(t)
	if len(task.Images) != 0 || task.Title != "Hello" {
		t.Fatalf("unexpected task: %+v", task)
	}
}

func TestServeMissingImageIs404(t *testing.T) {
	f := newAppFixture(t)
	rec := f.doRaw(http.MethodGet, "/v1/images/deadbeef", "", "application/octet-stream", &bytes.Buffer{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing image status = %d, want 404", rec.Code)
	}
}
