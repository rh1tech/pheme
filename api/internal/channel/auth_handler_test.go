package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/otp"
	"github.com/rh1tech/pheme/api/internal/store"
)

func mustUser(email, hash string) domain.User {
	return domain.User{Email: email, PasswordHash: hash, Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now().UTC()}
}

// captureMailer records the last sent body so tests can read the emailed code.
type captureMailer struct{ body string }

func (c *captureMailer) Send(_ context.Context, _, _, text, _ string) error {
	c.body = text
	return nil
}

var sixDigits = regexp.MustCompile(`\b\d{6}\b`)

func (c *captureMailer) code() string { return sixDigits.FindString(c.body) }

func newTestAuth() (*AuthHandler, *captureMailer, *http.ServeMux) {
	mail := &captureMailer{}
	h := &AuthHandler{
		Store:  store.NewMemory(),
		Tokens: auth.NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour),
		Codes:  otp.NewMemory(),
		Mailer: mail,
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	return h, mail, mux
}

func post(mux *http.ServeMux, path string, body any) *httptest.ResponseRecorder {
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRegisterVerifyHappyPath(t *testing.T) {
	_, mail, mux := newTestAuth()

	rec := post(mux, "/v1/auth/register", map[string]string{"email": "a@b.com", "password": "abcd1234"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("register status = %d, want 202; body=%s", rec.Code, rec.Body)
	}
	code := mail.code()
	if code == "" {
		t.Fatal("no code captured from email")
	}

	rec = post(mux, "/v1/auth/verify", map[string]string{"email": "a@b.com", "code": code})
	if rec.Code != http.StatusCreated {
		t.Fatalf("verify status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var tok tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tok.AccessToken == "" || tok.UserID == "" {
		t.Fatalf("expected tokens, got %+v", tok)
	}
}

func TestRegisterRejectsWeakPassword(t *testing.T) {
	_, _, mux := newTestAuth()
	rec := post(mux, "/v1/auth/register", map[string]string{"email": "a@b.com", "password": "short"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
	}
}

func TestVerifyLockoutAfterThreeWrongCodes(t *testing.T) {
	_, _, mux := newTestAuth()
	if rec := post(mux, "/v1/auth/register", map[string]string{"email": "a@b.com", "password": "abcd1234"}); rec.Code != http.StatusAccepted {
		t.Fatalf("register failed: %d %s", rec.Code, rec.Body)
	}

	// Two wrong attempts → 401.
	for i := 0; i < 2; i++ {
		rec := post(mux, "/v1/auth/verify", map[string]string{"email": "a@b.com", "code": "000000"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i+1, rec.Code)
		}
	}
	// Third wrong attempt → 400 lockout.
	rec := post(mux, "/v1/auth/verify", map[string]string{"email": "a@b.com", "code": "000000"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("third attempt status = %d, want 400", rec.Code)
	}
	// Pending signup is gone now.
	rec = post(mux, "/v1/auth/verify", map[string]string{"email": "a@b.com", "code": "000000"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("post-lockout status = %d, want 400", rec.Code)
	}
}

func TestRegisterCooldownBlocksRapidResend(t *testing.T) {
	_, _, mux := newTestAuth()
	if rec := post(mux, "/v1/auth/register", map[string]string{"email": "a@b.com", "password": "abcd1234"}); rec.Code != http.StatusAccepted {
		t.Fatalf("first register failed: %d", rec.Code)
	}
	rec := post(mux, "/v1/auth/register", map[string]string{"email": "a@b.com", "password": "abcd1234"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rapid resend status = %d, want 429; body=%s", rec.Code, rec.Body)
	}
}

func TestForgotResetPasswordFlow(t *testing.T) {
	h, mail, mux := newTestAuth()
	ctx := context.Background()

	// Create a verified user directly.
	hash, _ := auth.HashPassword("abcd1234")
	u, err := h.Store.CreateUser(ctx, mustUser("a@b.com", hash))
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if rec := post(mux, "/v1/auth/forgot-password", map[string]string{"email": "a@b.com"}); rec.Code != http.StatusOK {
		t.Fatalf("forgot status = %d, want 200", rec.Code)
	}
	code := mail.code()
	if code == "" {
		t.Fatal("no reset code captured")
	}

	rec := post(mux, "/v1/auth/reset-password", map[string]string{"email": "a@b.com", "code": code, "newPassword": "Newpass99"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	// New password must now verify.
	updated, _ := h.Store.UserByEmail(ctx, "a@b.com")
	if updated.ID != u.ID {
		t.Fatalf("user id changed unexpectedly")
	}
	ok, _ := auth.VerifyPassword("Newpass99", updated.PasswordHash)
	if !ok {
		t.Fatal("new password does not verify")
	}
}
