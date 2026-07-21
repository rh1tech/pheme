package federation

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeUsers map[string]bool

func (f fakeUsers) UserExists(_ context.Context, id string) bool { return f[id] }

// ResolveUsername treats each present key as both a username and its id, which is
// all the resolve-user test needs.
func (f fakeUsers) ResolveUsername(_ context.Context, username string) (string, string, bool) {
	if f[username] {
		return username, username, true
	}
	return "", "", false
}

// serverFor builds a running federation server for host `origin`, trusting the
// given domain->key nodelist, and returns its base URL.
func serverFor(t *testing.T, origin string, lookup fakeLookup, users fakeUsers) string {
	t.Helper()
	mux := http.NewServeMux()
	NewHandler(origin, lookup, users).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// The whole F2 loop: host A signs a request, host B verifies it against the
// nodelist, and B's handler runs knowing who A is. Nothing faked but the
// nodelist and the user table.
func TestLivenessEndToEnd(t *testing.T) {
	aKey := hostKey(t, 1)
	aPub := aKey.Public().(ed25519.PublicKey)

	// B trusts A.
	base := serverFor(t, "b.example", fakeLookup{"a.example": aPub}, nil)

	// A calls B.
	client := NewClient("a.example", "a-key-1", aKey)
	var out struct {
		Origin string `json:"origin"`
		Peer   string `json:"peer"`
	}
	if err := client.GetJSON(context.Background(), base+"/federation/v1/liveness", &out); err != nil {
		t.Fatalf("liveness call failed: %v", err)
	}
	if out.Origin != "b.example" {
		t.Errorf("origin = %q, want b.example", out.Origin)
	}
	// B echoes back who it authenticated the caller as — proof the signature
	// was checked, not merely accepted.
	if out.Peer != "a.example" {
		t.Errorf("peer = %q, want a.example — B did not prove the caller", out.Peer)
	}
}

func TestUserExistsEndToEnd(t *testing.T) {
	aKey := hostKey(t, 1)
	aPub := aKey.Public().(ed25519.PublicKey)
	base := serverFor(t, "b.example", fakeLookup{"a.example": aPub}, fakeUsers{"real-user": true})
	client := NewClient("a.example", "a-key-1", aKey)

	var out struct {
		Exists bool `json:"exists"`
	}
	if err := client.PostJSON(context.Background(),
		base+"/federation/v1/user-exists", map[string]string{"userId": "real-user"}, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Exists {
		t.Error("a real user was reported absent")
	}

	if err := client.PostJSON(context.Background(),
		base+"/federation/v1/user-exists", map[string]string{"userId": "ghost"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Exists {
		t.Error("a nonexistent user was reported present")
	}
}

func TestResolveUserEndToEnd(t *testing.T) {
	aKey := hostKey(t, 1)
	aPub := aKey.Public().(ed25519.PublicKey)
	base := serverFor(t, "b.example", fakeLookup{"a.example": aPub}, fakeUsers{"alice": true})
	client := NewClient("a.example", "a-key-1", aKey)

	var out struct {
		UserID      string `json:"userId"`
		DisplayName string `json:"displayName"`
	}
	if err := client.PostJSON(context.Background(),
		base+"/federation/v1/resolve-user", map[string]string{"username": "alice"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.UserID != "alice" {
		t.Errorf("userId = %q, want the resolved id", out.UserID)
	}

	// An unknown username is a 404, which PostJSON surfaces as an error — never a
	// silent empty id the caller might add as a member.
	err := client.PostJSON(context.Background(),
		base+"/federation/v1/resolve-user", map[string]string{"username": "nobody"}, &out)
	if err == nil {
		t.Error("resolving a nonexistent username succeeded")
	}
}

// A host B does not trust cannot call B, however well-formed its request.
func TestAnUntrustedHostIsRejectedEndToEnd(t *testing.T) {
	strangerKey := hostKey(t, 9)
	// B's nodelist does not contain c.example.
	base := serverFor(t, "b.example", fakeLookup{}, nil)

	client := NewClient("c.example", "c-key-1", strangerKey)
	err := client.GetJSON(context.Background(), base+"/federation/v1/liveness", &struct{}{})
	if err == nil {
		t.Fatal("an untrusted host's call succeeded")
	}
}

// An entirely unsigned request — someone poking the endpoint with curl — is 401.
func TestUnsignedRequestIsRejected(t *testing.T) {
	base := serverFor(t, "b.example", fakeLookup{}, nil)
	resp, err := http.Get(base + "/federation/v1/liveness")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for an unsigned request", resp.StatusCode)
	}
}

// The directory is public and unsigned, because a caller that does not yet know
// our endpoints cannot have signed a request to one.
func TestDirectoryIsPublic(t *testing.T) {
	base := serverFor(t, "b.example", fakeLookup{}, nil)
	resp, err := http.Get(base + "/.well-known/pheme-federation")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("directory status = %d, want 200", resp.StatusCode)
	}
	var dir struct {
		Origin    string            `json:"origin"`
		Endpoints map[string]string `json:"endpoints"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&dir); err != nil {
		t.Fatal(err)
	}
	if dir.Origin != "b.example" {
		t.Errorf("origin = %q", dir.Origin)
	}
	if dir.Endpoints["liveness"] == "" {
		t.Error("directory did not advertise the liveness endpoint")
	}
}
