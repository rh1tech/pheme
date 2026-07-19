package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// The "start a chat with…" picker.
//
// This endpoint hands one signed-in stranger information about another, so what it does NOT return
// matters more than what it does. Email is not public and must never appear in a result: it is the
// one field that turns a directory into a mailing list, and it is what the account is identified by
// everywhere else. The minimum query length exists for the same reason — a one-character search
// against a large user base is an export, not a search.
//
// It had no tests at all.

func searchAs(t *testing.T, f *fixture, token, q string) []domain.PublicUser {
	t.Helper()
	rec := f.do(http.MethodGet, "/v1/users/search?q="+url.QueryEscape(q), token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search %q = %d: %s", q, rec.Code, rec.Body)
	}
	var out struct {
		Users []domain.PublicUser `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	return out.Users
}

// namedUser creates a user with a username and display name to search against.
func namedUser(t *testing.T, f *fixture, email, username, display string) string {
	t.Helper()
	u, err := f.store.CreateUser(context.Background(), domain.User{
		Email: email, Username: username, DisplayName: display,
		Role: domain.RoleUser, Status: domain.UserActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create %s: %v", email, err)
	}
	return u.ID
}

func TestUserSearchFindsPeopleByUsernameAndDisplayName(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "searcher@pheme.test")
	namedUser(t, f, "target@pheme.test", "borisq", "Boris Quill")

	if got := searchAs(t, f, token, "boris"); len(got) != 1 || got[0].Username != "borisq" {
		t.Errorf("searching by username returned %+v", got)
	}
	if got := searchAs(t, f, token, "Quill"); len(got) != 1 || got[0].Username != "borisq" {
		t.Errorf("searching by display name returned %+v", got)
	}
}

// THE ONE THAT MATTERS. An email address must never come back, whatever is searched for. It is not
// a public field, and this endpoint is reachable by any signed-in account.
func TestUserSearchNeverReturnsAnEmailAddress(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "searcher2@pheme.test")
	namedUser(t, f, "private.address@pheme.test", "publicname", "Public Name")

	rec := f.do(http.MethodGet, "/v1/users/search?q=publicname", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d", rec.Code)
	}
	// Asserted against the RAW body, not the decoded struct: decoding into a type without an email
	// field would hide an email the server actually sent.
	if body := rec.Body.String(); strings.Contains(strings.ToLower(body), "private.address") ||
		strings.Contains(strings.ToLower(body), "@pheme.test") {
		t.Errorf("a search result carried an email address: %s", body)
	}
}

// Searching by email must not find anybody either — otherwise the endpoint confirms whether a given
// address has an account here, one guess at a time.
func TestUserSearchDoesNotMatchOnEmail(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "searcher3@pheme.test")
	namedUser(t, f, "findme@pheme.test", "unrelated", "Unrelated Person")

	for _, q := range []string{"findme@pheme.test", "findme", "findme@"} {
		if got := searchAs(t, f, token, q); len(got) != 0 {
			t.Errorf("searching %q found %+v; the picker confirms which addresses have accounts", q, got)
		}
	}
}

// A query shorter than the minimum returns nothing, without touching the store. One character
// against a large user base is an export rather than a search.
func TestUserSearchRefusesAQueryTooShortToBeOne(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "searcher4@pheme.test")
	namedUser(t, f, "a@pheme.test", "aaa", "Aaa Person")

	for _, q := range []string{"", "a"} {
		if got := searchAs(t, f, token, q); len(got) != 0 {
			t.Errorf("a %d-character query returned %d users; that is an export, not a search",
				len([]rune(q)), len(got))
		}
	}
	// And the boundary still works, so the limit is not quietly stricter than it claims.
	if got := searchAs(t, f, token, "aa"); len(got) != 1 {
		t.Errorf("a %d-character query returned %d users, want the one that matches",
			minUserSearchLen, len(got))
	}
}

// The caller never appears in their own results — there is no point starting a chat with yourself,
// and a picker that offers it looks broken.
func TestUserSearchExcludesTheCaller(t *testing.T) {
	f := newFixture(t)
	me := namedUser(t, f, "self@pheme.test", "selfsearch", "Self Search")
	token, _, _, err := f.tokens.Issue(me, string(domain.RoleUser))
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	namedUser(t, f, "other@pheme.test", "selfsearcher2", "Self Searcher Two")

	got := searchAs(t, f, token, "selfsearch")
	for _, u := range got {
		if u.ID == me {
			t.Errorf("the caller appeared in their own search results: %+v", got)
		}
	}
	if len(got) != 1 {
		t.Errorf("got %d results, want only the other person", len(got))
	}
}

// Unauthenticated callers get nothing. A public user directory would be a scrape target.
func TestUserSearchRequiresSigningIn(t *testing.T) {
	f := newFixture(t)
	namedUser(t, f, "hidden@pheme.test", "hiddenname", "Hidden Name")

	rec := f.do(http.MethodGet, "/v1/users/search?q=hiddenname", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("an unauthenticated search = %d, want 401", rec.Code)
	}
}
