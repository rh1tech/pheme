package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/auth"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/store"
)

// Managing a channel's API keys.
//
// Revocation is the only thing that can be done about a key that has leaked — pasted into a ticket,
// committed to a repository, left in a screenshot. The ingest side is tested (a revoked key stops
// working immediately); this is the endpoint an operator actually reaches for, and it had no tests.
//
// Listing keys has its own requirement: a key is a bearer secret, and the server keeps only its
// hash. Neither the plaintext nor the hash may come back out of this endpoint. Returning the hash
// would hand an attacker with read access to the admin API the material to verify guesses offline.

func seedChannelWithKey(t *testing.T, db store.Store, ownerID, name string) (domain.Channel, domain.APIKey, string) {
	t.Helper()
	ch, err := db.CreateChannel(context.Background(), domain.Channel{
		PublicID: "pub-" + name, OwnerID: ownerID, Name: name,
		Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	plaintext, hash, prefix, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	key, err := db.CreateAPIKey(context.Background(), domain.APIKey{
		ChannelID: ch.ID, HashedKey: hash, Prefix: prefix, Label: "primary",
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return ch, key, plaintext
}

func TestAdminListsAChannelsKeys(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "keys-admin@pheme.test", domain.RoleAdmin)
	ch, key, _ := seedChannelWithKey(t, db, admin.ID, "news")

	rec := adminReq(mux, http.MethodGet, "/v1/admin/channels/"+ch.ID+"/keys", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list keys = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Keys []domain.APIKey `json:"keys"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Keys) != 1 || out.Keys[0].ID != key.ID {
		t.Fatalf("got %+v, want the one key", out.Keys)
	}
	// The prefix is what identifies a key to a human — "which of these is the one in the cron job" —
	// so it must survive.
	if out.Keys[0].Prefix == "" {
		t.Error("the key came back with no prefix; an operator cannot tell their keys apart")
	}
}

// THE ONE THAT MATTERS FOR SECRECY. The stored hash must not be served.
func TestAdminKeyListingNeverExposesTheSecret(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "keys-admin2@pheme.test", domain.RoleAdmin)
	ch, key, plaintext := seedChannelWithKey(t, db, admin.ID, "secrets")

	rec := adminReq(mux, http.MethodGet, "/v1/admin/channels/"+ch.ID+"/keys", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list keys = %d", rec.Code)
	}
	// Checked against the RAW body: decoding into a struct that has no field for the hash would
	// hide a hash the server actually sent.
	body := rec.Body.String()
	if strings.Contains(body, plaintext) {
		t.Error("the key listing returned the plaintext key")
	}
	if strings.Contains(body, key.HashedKey) {
		t.Errorf("the key listing returned the stored hash, which is the material for verifying "+
			"guesses offline: %s", body)
	}
}

// Revocation must take effect in the store, not merely answer 200. This is the entire remedy for a
// leaked key.
func TestAdminRevokesAKey(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "keys-admin3@pheme.test", domain.RoleAdmin)
	ch, key, _ := seedChannelWithKey(t, db, admin.ID, "revokeme")

	rec := adminReq(mux, http.MethodDelete,
		"/v1/admin/channels/"+ch.ID+"/keys/"+key.ID, admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d: %s", rec.Code, rec.Body)
	}

	keys, err := db.APIKeysByChannel(context.Background(), ch.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys", len(keys))
	}
	if keys[0].RevokedAt == nil {
		t.Error("the endpoint answered 200 but the key is not revoked; a leaked key would keep " +
			"working and the operator would believe they had turned it off")
	}
}

// A key that does not exist is a 404, not a cheerful 200. An operator revoking a key from a stale
// list must be told it did not happen.
func TestAdminRevokingAKeyThatIsNotThereIs404(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "keys-admin4@pheme.test", domain.RoleAdmin)
	ch, _, _ := seedChannelWithKey(t, db, admin.ID, "ghostkey")

	rec := adminReq(mux, http.MethodDelete,
		"/v1/admin/channels/"+ch.ID+"/keys/000000000000000000000000", admin.ID, "admin", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("revoking a missing key = %d, want 404 — a 200 tells an operator they have "+
			"disabled a key they have not", rec.Code)
	}
}

// Both endpoints are admin-only. A channel's keys are the credentials for posting to it.
func TestAdminKeyEndpointsRefuseNonAdmins(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "keys-admin5@pheme.test", domain.RoleAdmin)
	ordinary := seedUser(t, db, "ordinary@pheme.test", domain.RoleUser)
	ch, key, _ := seedChannelWithKey(t, db, admin.ID, "guarded")

	for _, tc := range []struct {
		name, method, path string
	}{
		{"list", http.MethodGet, "/v1/admin/channels/" + ch.ID + "/keys"},
		{"revoke", http.MethodDelete, "/v1/admin/channels/" + ch.ID + "/keys/" + key.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := adminReq(mux, tc.method, tc.path, ordinary.ID, "user", nil)
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
				t.Errorf("%s as an ordinary user = %d, want 403/401", tc.name, rec.Code)
			}
		})
	}

	// And the key is still live afterwards.
	keys, err := db.APIKeysByChannel(context.Background(), ch.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if keys[0].RevokedAt != nil {
		t.Error("a non-admin revoked a key")
	}
}

// Admin message browsing, which is how an operator sees what a channel has been sending.
func TestAdminBrowsesAChannelsMessages(t *testing.T) {
	db := store.NewMemory(nil)
	mux := adminMux(db)
	admin := seedUser(t, db, "msg-admin@pheme.test", domain.RoleAdmin)
	ch, _, _ := seedChannelWithKey(t, db, admin.ID, "chatty")

	for i := 0; i < 3; i++ {
		if _, err := db.CreateMessage(context.Background(), domain.Message{
			ChannelID: ch.ID, Title: "post", Body: "body", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	rec := adminReq(mux, http.MethodGet, "/v1/admin/channels/"+ch.ID+"/messages", admin.ID, "admin", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list messages = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Messages   []domain.Message `json:"messages"`
		NextCursor string           `json:"nextCursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Messages) != 3 {
		t.Errorf("got %d messages, want 3", len(out.Messages))
	}
	// Fewer messages than the page size means there is no next page. A cursor here would make a
	// client loop over the same last page forever.
	if out.NextCursor != "" {
		t.Errorf("a short page returned a cursor (%q); a client would page in circles", out.NextCursor)
	}

	if rec := adminReq(mux, http.MethodGet, "/v1/admin/channels/"+ch.ID+"/messages",
		seedUser(t, db, "nosy@pheme.test", domain.RoleUser).ID, "user", nil); rec.Code == http.StatusOK {
		t.Error("an ordinary user browsed a channel's messages through the admin API")
	}
}
