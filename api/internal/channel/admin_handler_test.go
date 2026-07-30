package channel

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
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/store"
)

// adminMux registers the admin routes on a bare mux (no JWT middleware) so tests
// drive the handlers directly while still exercising routing and path values.
func adminMux(db store.Store) *http.ServeMux {
	mux, _ := adminMuxWithRevoker(db)
	return mux
}

func adminMuxWithRevoker(db store.Store) (*http.ServeMux, *auth.SessionRevoker) {
	revoker := auth.NewSessionRevoker(db)
	mux := http.NewServeMux()
	(&AdminHandler{Store: db, Revoker: revoker, SessionTTL: 24 * time.Hour}).Register(mux)
	return mux, revoker
}

// adminReq sends a request through the admin mux with the caller's id and role
// injected into the context (mirroring what the JWT middleware would do).
func adminReq(mux *http.ServeMux, method, path, callerID, role string, body any) *httptest.ResponseRecorder {
	var r io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		r = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, r)
	ctx := auth.WithRole(auth.WithUserID(req.Context(), callerID), role)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// seedUser inserts a user directly via the store for test setup.
func seedUser(t *testing.T, db store.Store, email string, role domain.Role) domain.User {
	t.Helper()
	hash, _ := auth.HashPassword("seedpass1")
	u, err := db.CreateUser(context.Background(), domain.User{
		Email:        email,
		PasswordHash: hash,
		Role:         role,
		Status:       domain.UserActive,
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return u
}

func TestAdminCreateUserHappyPath(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)

	rec := adminReq(mux, http.MethodPost, "/v1/admin/users", admin.ID, "admin",
		map[string]string{"email": "New@B.com", "password": "abcd1234"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}

	var out userSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Email != "new@b.com" {
		t.Fatalf("email = %q, want lowercased new@b.com", out.Email)
	}
	if out.Role != domain.RoleUser || out.Status != domain.UserActive {
		t.Fatalf("role/status = %q/%q, want user/active", out.Role, out.Status)
	}

	// The created user can authenticate with the supplied password.
	stored, err := db.UserByEmail(context.Background(), "new@b.com")
	if err != nil {
		t.Fatalf("user not persisted: %v", err)
	}
	if ok, _ := auth.VerifyPassword("abcd1234", stored.PasswordHash); !ok {
		t.Fatal("password does not verify")
	}
}

func TestAdminCreateUserAsAdminRole(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)

	rec := adminReq(mux, http.MethodPost, "/v1/admin/users", admin.ID, "admin",
		map[string]string{"email": "boss@b.com", "password": "abcd1234", "role": "admin"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var out userSummary
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Role != domain.RoleAdmin {
		t.Fatalf("role = %q, want admin", out.Role)
	}
}

func TestAdminCreateUserValidation(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
	seedUser(t, db, "taken@b.com", domain.RoleUser)

	cases := []struct {
		name string
		body map[string]string
		want int
	}{
		{"invalid email", map[string]string{"email": "nope", "password": "abcd1234"}, http.StatusBadRequest},
		{"weak password", map[string]string{"email": "ok@b.com", "password": "short"}, http.StatusBadRequest},
		{"invalid role", map[string]string{"email": "ok@b.com", "password": "abcd1234", "role": "superuser"}, http.StatusBadRequest},
		{"duplicate email", map[string]string{"email": "taken@b.com", "password": "abcd1234"}, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := adminReq(mux, http.MethodPost, "/v1/admin/users", admin.ID, "admin", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestAdminCreateUserForbiddenForNonAdmin(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	user := seedUser(t, db, "user@b.com", domain.RoleUser)

	rec := adminReq(mux, http.MethodPost, "/v1/admin/users", user.ID, "user",
		map[string]string{"email": "x@b.com", "password": "abcd1234"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
}

func TestAdminListUsersCountsChannels(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
	owner := seedUser(t, db, "owner@b.com", domain.RoleUser)
	for i := 0; i < 2; i++ {
		if _, err := db.CreateChannel(context.Background(), domain.Channel{
			OwnerID: owner.ID, Name: "c", Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("seed channel: %v", err)
		}
	}

	rec := adminReq(mux, http.MethodGet, "/v1/admin/users?page=1&limit=20", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out struct {
		Users []userSummary `json:"users"`
		Total int           `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Total != 2 {
		t.Fatalf("total = %d, want 2", out.Total)
	}
	for _, u := range out.Users {
		if u.Email == "owner@b.com" && u.ChannelCount != 2 {
			t.Fatalf("owner channelCount = %d, want 2", u.ChannelCount)
		}
	}
}

func TestAdminUpdateUserRoleAndStatus(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
	target := seedUser(t, db, "target@b.com", domain.RoleUser)

	role := domain.RoleAdmin
	rec := adminReq(mux, http.MethodPatch, "/v1/admin/users/"+target.ID, admin.ID, "admin",
		map[string]any{"role": role})
	if rec.Code != http.StatusOK {
		t.Fatalf("promote status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	updated, _ := db.UserByEmail(context.Background(), "target@b.com")
	if updated.Role != domain.RoleAdmin {
		t.Fatalf("role = %q, want admin", updated.Role)
	}

	status := domain.UserBlocked
	rec = adminReq(mux, http.MethodPatch, "/v1/admin/users/"+target.ID, admin.ID, "admin",
		map[string]any{"status": status})
	if rec.Code != http.StatusOK {
		t.Fatalf("block status = %d, want 200", rec.Code)
	}
	updated, _ = db.UserByEmail(context.Background(), "target@b.com")
	if updated.Status != domain.UserBlocked {
		t.Fatalf("status = %q, want blocked", updated.Status)
	}
}

func TestAdminCannotChangeOwnRole(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)

	role := domain.RoleUser
	rec := adminReq(mux, http.MethodPatch, "/v1/admin/users/"+admin.ID, admin.ID, "admin",
		map[string]any{"role": role})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (self-change blocked)", rec.Code)
	}
}

func TestAdminResetUserPassword(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
	target := seedUser(t, db, "target@b.com", domain.RoleUser)

	// Weak password rejected.
	rec := adminReq(mux, http.MethodPost, "/v1/admin/users/"+target.ID+"/reset-password", admin.ID, "admin",
		map[string]string{"newPassword": "short"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("weak password status = %d, want 400", rec.Code)
	}

	// Valid password accepted and applied.
	rec = adminReq(mux, http.MethodPost, "/v1/admin/users/"+target.ID+"/reset-password", admin.ID, "admin",
		map[string]string{"newPassword": "Newpass99"})
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	updated, _ := db.UserByEmail(context.Background(), "target@b.com")
	if ok, _ := auth.VerifyPassword("Newpass99", updated.PasswordHash); !ok {
		t.Fatal("new password does not verify")
	}
}

func TestAdminDeleteUser(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
	target := seedUser(t, db, "target@b.com", domain.RoleUser)

	// Cannot delete self.
	rec := adminReq(mux, http.MethodDelete, "/v1/admin/users/"+admin.ID, admin.ID, "admin", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-delete status = %d, want 400", rec.Code)
	}

	// Delete another user.
	rec = adminReq(mux, http.MethodDelete, "/v1/admin/users/"+target.ID, admin.ID, "admin", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if _, err := db.UserByEmail(context.Background(), "target@b.com"); err != store.ErrNotFound {
		t.Fatalf("user still present, err=%v", err)
	}
}

func TestAdminSecurityChangesRevokeExistingSessions(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   func(string) string
		body   any
	}{
		{
			name: "role change", method: http.MethodPatch,
			path: func(id string) string { return "/v1/admin/users/" + id },
			body: map[string]any{"role": domain.RoleAdmin},
		},
		{
			name: "status change", method: http.MethodPatch,
			path: func(id string) string { return "/v1/admin/users/" + id },
			body: map[string]any{"status": domain.UserBlocked},
		},
		{
			name: "password reset", method: http.MethodPost,
			path: func(id string) string { return "/v1/admin/users/" + id + "/reset-password" },
			body: map[string]any{"newPassword": "Newpass99"},
		},
		{
			name: "deletion", method: http.MethodDelete,
			path: func(id string) string { return "/v1/admin/users/" + id },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := store.NewMemory(nil)
			mux, revoker := adminMuxWithRevoker(db)
			admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
			target := seedUser(t, db, "target@b.com", domain.RoleUser)
			tokens := auth.NewTokenManager("test-secret", 15*time.Minute, 24*time.Hour)
			access, _, _, err := tokens.Issue(target.ID, string(target.Role))
			if err != nil {
				t.Fatalf("issue target session: %v", err)
			}
			claims, err := tokens.ParseClaims(access, auth.AccessToken)
			if err != nil {
				t.Fatalf("parse target session: %v", err)
			}

			rec := adminReq(mux, tc.method, tc.path(target.ID), admin.ID, "admin", tc.body)
			if rec.Code < 200 || rec.Code >= 300 {
				t.Fatalf("mutation status = %d; body=%s", rec.Code, rec.Body)
			}
			if !revoker.IsUserRevoked(target.ID, claims.IssuedAt.Time) {
				t.Fatal("the account mutation left the target's existing session active")
			}
		})
	}
}

func TestAdminChannelLifecycle(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
	owner := seedUser(t, db, "owner@b.com", domain.RoleUser)
	ch, _ := db.CreateChannel(context.Background(), domain.Channel{
		OwnerID: owner.ID, Name: "News", PublicID: "ch_news",
		SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})

	// List enriches with owner email.
	rec := adminReq(mux, http.MethodGet, "/v1/admin/channels", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	var list struct {
		Channels []channelSummary `json:"channels"`
		Total    int              `json:"total"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Total != 1 || len(list.Channels) != 1 || list.Channels[0].OwnerEmail != "owner@b.com" {
		t.Fatalf("unexpected list: %+v", list)
	}

	// Disable the channel.
	status := domain.ChannelDisabled
	rec = adminReq(mux, http.MethodPatch, "/v1/admin/channels/"+ch.ID, admin.ID, "admin",
		map[string]any{"status": status})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	// Delete it.
	rec = adminReq(mux, http.MethodDelete, "/v1/admin/channels/"+ch.ID, admin.ID, "admin", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rec.Code)
	}
	if _, err := db.ChannelByID(context.Background(), ch.ID); err != store.ErrNotFound {
		t.Fatalf("channel still present, err=%v", err)
	}
}

func TestAdminEndpointsRejectNonAdmin(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	user := seedUser(t, db, "user@b.com", domain.RoleUser)

	paths := []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/admin/stats"},
		{http.MethodGet, "/v1/admin/users"},
		{http.MethodGet, "/v1/admin/channels"},
	}
	for _, p := range paths {
		rec := adminReq(mux, p.method, p.path, user.ID, "user", nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", p.method, p.path, rec.Code)
		}
	}
}
