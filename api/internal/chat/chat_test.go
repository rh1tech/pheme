package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/store"
)

// fixture wires the chat Handler behind the real JWT middleware, so tests
// exercise the full auth + routing path like the channel suite does.
type fixture struct {
	mux    *http.ServeMux
	tokens *auth.TokenManager
	store  store.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := store.NewMemory(blob.NewMemory())
	tokens := auth.NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)
	h := &Handler{Store: db, Live: live.NewMemoryBus()}
	protected := http.NewServeMux()
	h.Register(protected)
	mux := http.NewServeMux()
	mux.Handle("/v1/", tokens.Middleware(protected))
	return &fixture{mux: mux, tokens: tokens, store: db}
}

func (f *fixture) user(t *testing.T, email string) (string, string) {
	t.Helper()
	u, err := f.store.CreateUser(context.Background(), domain.User{
		Email: email, Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	access, _, err := f.tokens.Issue(u.ID, string(domain.RoleUser))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return u.ID, access
}

func (f *fixture) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var r io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		r = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}
