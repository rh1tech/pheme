package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// One person's public profile, for the view behind an avatar in a chat.
//
// The interesting assertions here are the negative ones. This endpoint hands one signed-in stranger
// a fuller picture of another than search does, so the fields it must NOT carry are the point:
// email, which turns a directory into a mailing list, and phone, which is a way to reach somebody
// off this service entirely. Both live on the same struct as the bio, one field apart, and both
// would have been published by any projection written carelessly.

func profileAs(t *testing.T, f *fixture, token, id string) (*http.Response, map[string]any) {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/users/"+id, token, nil)
	var body map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v (%s)", err, rec.Body)
		}
	}
	return rec.Result(), body
}

func profiledUser(t *testing.T, f *fixture) string {
	t.Helper()
	u, err := f.store.CreateUser(context.Background(), domain.User{
		Email:       "subject@pheme.test",
		Username:    "borisq",
		DisplayName: "Boris Quill",
		Bio:         "Writes things down.",
		Phone:       "+44 7700 900000",
		Website:     "https://example.test",
		Role:        domain.RoleUser,
		Status:      domain.UserActive,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create subject: %v", err)
	}
	return u.ID
}

func TestUserProfileReturnsWhatThePersonPublished(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "viewer@pheme.test")
	id := profiledUser(t, f)

	res, body := profileAs(t, f, token, id)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("profile = %d", res.StatusCode)
	}
	for field, want := range map[string]string{
		"username":    "borisq",
		"displayName": "Boris Quill",
		"bio":         "Writes things down.",
		"website":     "https://example.test",
	} {
		if got, _ := body[field].(string); got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
}

func TestUserProfileNeverCarriesEmailOrPhone(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "viewer@pheme.test")
	id := profiledUser(t, f)

	_, body := profileAs(t, f, token, id)
	for _, field := range []string{"email", "phone", "passwordHash", "role", "status"} {
		if _, present := body[field]; present {
			t.Errorf("profile leaked %q: %v", field, body[field])
		}
	}
}

func TestUserProfileRequiresAuthentication(t *testing.T) {
	f := newFixture(t)
	id := profiledUser(t, f)

	res, _ := profileAs(t, f, "", id)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated profile = %d, want 401", res.StatusCode)
	}
}

func TestUserProfileIsNotFoundForAnUnknownID(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "viewer@pheme.test")

	res, _ := profileAs(t, f, token, "nobody")
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown user = %d, want 404", res.StatusCode)
	}
}

// The wildcard route must not swallow the literal one registered beside it.
func TestUserSearchStillRoutesAheadOfTheProfileWildcard(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "viewer@pheme.test")
	profiledUser(t, f)

	if got := searchAs(t, f, token, "boris"); len(got) != 1 {
		t.Fatalf("search returned %d results, want 1 — /v1/users/search may be resolving as a profile id", len(got))
	}
}
