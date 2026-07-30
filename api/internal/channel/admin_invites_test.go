package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/invite"
	"github.com/rh1tech/pheme/api/internal/store"
)

func TestAdminCreateInviteReturnsCodeOnlyOnce(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)

	rec := adminReq(mux, http.MethodPost, "/v1/admin/invites", admin.ID, "admin",
		map[string]any{"note": "Anna", "expiresInDays": 7})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created inviteSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Code == "" {
		t.Fatal("creation response carried no code; the link would be unusable")
	}
	if created.Status != domain.InvitePending || created.ExpiresAt == nil {
		t.Fatalf("got %+v, want a pending invite with an expiry", created)
	}

	// The store keeps a hash, never the code — that is what makes a leaked dump useless.
	stored, err := db.InviteByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("invite by id: %v", err)
	}
	if stored.CodeHash != invite.HashCode(created.Code) {
		t.Fatal("stored hash does not match the issued code")
	}

	// And listing must never hand it back.
	rec = adminReq(mux, http.MethodGet, "/v1/admin/invites", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	var listed struct {
		Invites []inviteSummary `json:"invites"`
		Total   int64           `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listed.Total != 1 || len(listed.Invites) != 1 {
		t.Fatalf("listed %d invites, want 1", len(listed.Invites))
	}
	if listed.Invites[0].Code != "" {
		t.Fatal("the invite list re-displayed the code")
	}
}

func TestAdminInvitesRequireAdmin(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	user := seedUser(t, db, "user@b.com", domain.RoleUser)

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"list", http.MethodGet, "/v1/admin/invites", nil},
		{"create", http.MethodPost, "/v1/admin/invites", map[string]any{}},
		{"revoke", http.MethodPost, "/v1/admin/invites/whatever/revoke", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := adminReq(mux, tc.method, tc.path, user.ID, "user", tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestAdminRevokeInvite(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)
	inv, _ := mintInvite(t, db, nil)

	rec := adminReq(mux, http.MethodPost, "/v1/admin/invites/"+inv.ID+"/revoke", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got inviteSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != domain.InviteRevoked {
		t.Fatalf("status = %q, want revoked", got.Status)
	}

	if rec := adminReq(mux, http.MethodPost, "/v1/admin/invites/nope/revoke", admin.ID, "admin", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown invite status = %d, want 404", rec.Code)
	}
}

func TestAdminCreateInviteRejectsAbsurdExpiry(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "admin@b.com", domain.RoleAdmin)

	for _, days := range []int{-1, maxInviteTTLDays + 1} {
		rec := adminReq(mux, http.MethodPost, "/v1/admin/invites", admin.ID, "admin",
			map[string]any{"expiresInDays": days})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expiresInDays=%d status = %d, want 400", days, rec.Code)
		}
	}
}
