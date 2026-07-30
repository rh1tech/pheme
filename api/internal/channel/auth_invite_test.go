package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/invite"
	"github.com/rh1tech/pheme/api/internal/store"
)

// newInviteOnlyAuth is newTestAuth with the door shut.
func newInviteOnlyAuth(t *testing.T) (*AuthHandler, *captureMailer, *http.ServeMux) {
	t.Helper()
	h, mail, mux := newTestAuth()
	h.InviteOnly = true
	return h, mail, mux
}

// mintInvite writes an invite straight to the store and returns its raw code, the way an
// admin's POST /v1/admin/invites would have.
func mintInvite(t *testing.T, db store.Store, mutate func(*domain.Invite)) (domain.Invite, string) {
	t.Helper()
	code, err := invite.GenerateCode()
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	inv := domain.Invite{
		CodeHash:  invite.HashCode(code),
		Prefix:    invite.Prefix(code),
		CreatedBy: "admin",
		CreatedAt: time.Now().UTC(),
	}
	if mutate != nil {
		mutate(&inv)
	}
	created, err := db.CreateInvite(context.Background(), inv)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	return created, code
}

func TestRegisterRefusedWithoutInviteWhenInviteOnly(t *testing.T) {
	_, _, mux := newInviteOnlyAuth(t)

	rec := post(mux, "/v1/auth/register", map[string]string{
		"email":    "nobody@example.com",
		"password": "correct horse battery",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRegisterRefusedWithUnknownInvite(t *testing.T) {
	_, _, mux := newInviteOnlyAuth(t)

	rec := post(mux, "/v1/auth/register", map[string]string{
		"email":    "nobody@example.com",
		"password": "correct horse battery",
		"invite":   "not-a-real-code",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// The whole point of the feature: an invitation admits exactly one account.
func TestInviteAdmitsOneAccountThenIsSpent(t *testing.T) {
	h, mail, mux := newInviteOnlyAuth(t)
	_, code := mintInvite(t, h.Store, nil)

	if rec := post(mux, "/v1/auth/register", map[string]string{
		"email":    "first@example.com",
		"password": "correct horse battery",
		"invite":   code,
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("register status = %d, want 202", rec.Code)
	}
	if rec := post(mux, "/v1/auth/verify", map[string]string{
		"email": "first@example.com",
		"code":  mail.code(),
	}); rec.Code != http.StatusCreated {
		t.Fatalf("verify status = %d, want 201", rec.Code)
	}

	// Same link, second person.
	rec := post(mux, "/v1/auth/register", map[string]string{
		"email":    "second@example.com",
		"password": "correct horse battery",
		"invite":   code,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("second register status = %d, want 403", rec.Code)
	}
}

// The invitation must survive an abandoned signup: registering and never confirming the code
// is the commonest way a form is left half-finished, and it must not cost the invitee their
// one chance.
func TestInviteSurvivesUnverifiedRegistration(t *testing.T) {
	h, _, mux := newInviteOnlyAuth(t)
	inv, code := mintInvite(t, h.Store, nil)

	if rec := post(mux, "/v1/auth/register", map[string]string{
		"email":    "ghost@example.com",
		"password": "correct horse battery",
		"invite":   code,
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("register status = %d, want 202", rec.Code)
	}

	after, err := h.Store.InviteByID(context.Background(), inv.ID)
	if err != nil {
		t.Fatalf("invite by id: %v", err)
	}
	if !after.Redeemable(time.Now().UTC()) {
		t.Fatal("invite was spent by a registration that was never verified")
	}
}

func TestRegisterRefusedWithExpiredInvite(t *testing.T) {
	h, _, mux := newInviteOnlyAuth(t)
	past := time.Now().UTC().Add(-time.Hour)
	_, code := mintInvite(t, h.Store, func(i *domain.Invite) { i.ExpiresAt = &past })

	rec := post(mux, "/v1/auth/register", map[string]string{
		"email":    "late@example.com",
		"password": "correct horse battery",
		"invite":   code,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestRegisterRefusedWithRevokedInvite(t *testing.T) {
	h, _, mux := newInviteOnlyAuth(t)
	_, code := mintInvite(t, h.Store, nil)
	inv, err := h.Store.InviteByCodeHash(context.Background(), invite.HashCode(code))
	if err != nil {
		t.Fatalf("invite by hash: %v", err)
	}
	if err := h.Store.RevokeInvite(context.Background(), inv.ID, time.Now().UTC()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	rec := post(mux, "/v1/auth/register", map[string]string{
		"email":    "withdrawn@example.com",
		"password": "correct horse battery",
		"invite":   code,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

// An open server must keep behaving exactly as it did — the flag is what closes the door, and
// nothing else about registration changes.
func TestRegisterIgnoresInviteWhenServerIsOpen(t *testing.T) {
	_, mail, mux := newTestAuth()

	if rec := post(mux, "/v1/auth/register", map[string]string{
		"email":    "open@example.com",
		"password": "correct horse battery",
	}); rec.Code != http.StatusAccepted {
		t.Fatalf("register status = %d, want 202", rec.Code)
	}
	if rec := post(mux, "/v1/auth/verify", map[string]string{
		"email": "open@example.com",
		"code":  mail.code(),
	}); rec.Code != http.StatusCreated {
		t.Fatalf("verify status = %d, want 201", rec.Code)
	}
}

func TestRegistrationEndpointReportsMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		inviteOnly bool
	}{{"open", false}, {"closed", true}} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, mux := newTestAuth()
			h.InviteOnly = tc.inviteOnly

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/registration", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var got struct {
				InviteOnly bool `json:"inviteOnly"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.InviteOnly != tc.inviteOnly {
				t.Fatalf("inviteOnly = %v, want %v", got.InviteOnly, tc.inviteOnly)
			}
		})
	}
}

func TestCheckInviteNamesWhyACodeIsRefused(t *testing.T) {
	h, _, mux := newInviteOnlyAuth(t)
	_, good := mintInvite(t, h.Store, nil)
	past := time.Now().UTC().Add(-time.Hour)
	_, expired := mintInvite(t, h.Store, func(i *domain.Invite) { i.ExpiresAt = &past })

	for _, tc := range []struct {
		name       string
		code       string
		wantValid  bool
		wantReason string
	}{
		{"good", good, true, ""},
		{"expired", expired, false, "expired"},
		{"unknown", "no-such-code", false, "unknown"},
		{"empty", "", false, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/v1/auth/invite?code="+tc.code, nil)
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			var got inviteStatus
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Valid != tc.wantValid || got.Reason != tc.wantReason {
				t.Fatalf("got %+v, want valid=%v reason=%q", got, tc.wantValid, tc.wantReason)
			}
		})
	}
}

// Two people verifying against the same invitation at the same instant. Exactly one account
// may exist afterwards — if both won, the invite would not be single-use at all.
func TestConcurrentVerificationsSpendOneInviteOnce(t *testing.T) {
	h, _, mux := newInviteOnlyAuth(t)
	_, code := mintInvite(t, h.Store, nil)

	// Two pending signups, both admitted by the same invite: register only checks, it does
	// not spend, so both get this far by design.
	mailers := map[string]string{}
	for _, email := range []string{"a@example.com", "b@example.com"} {
		mail := &captureMailer{}
		h.Mailer = mail
		if rec := post(mux, "/v1/auth/register", map[string]string{
			"email":    email,
			"password": "correct horse battery",
			"invite":   code,
		}); rec.Code != http.StatusAccepted {
			t.Fatalf("register %s status = %d, want 202", email, rec.Code)
		}
		mailers[email] = mail.code()
	}

	var wg sync.WaitGroup
	results := make([]int, 0, 2)
	var mu sync.Mutex
	for email, verifyCode := range mailers {
		wg.Add(1)
		go func(email, verifyCode string) {
			defer wg.Done()
			rec := post(mux, "/v1/auth/verify", map[string]string{"email": email, "code": verifyCode})
			mu.Lock()
			results = append(results, rec.Code)
			mu.Unlock()
		}(email, verifyCode)
	}
	wg.Wait()

	created := 0
	for _, code := range results {
		if code == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d accounts created from one invite, want exactly 1 (statuses %v)", created, results)
	}
}
