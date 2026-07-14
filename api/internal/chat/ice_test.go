package chat

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The TURN credential must be exactly what coturn recomputes, or every call that needs a
// relay fails to connect — and it fails silently, because the browser just reports "no
// candidate pair" rather than "your password was wrong".
//
// coturn's `use-auth-secret` mode: the username IS the expiry, and the password is
// base64(HMAC-SHA1(static-auth-secret, username)). This pins both halves against an
// independently computed value.
func TestTURNCredentialMatchesWhatCoturnWouldCompute(t *testing.T) {
	const secret = "a-shared-secret"
	expiry := time.Unix(1_700_000_000, 0)

	username, credential := turnCredential(secret, expiry)

	if username != "1700000000" {
		t.Fatalf("username = %q, want the unix expiry", username)
	}
	// Recomputed the way coturn does, not by calling the same function back.
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if credential != want {
		t.Fatalf("credential = %q, want %q", credential, want)
	}
}

// The username carries the expiry and NOTHING else. Putting the user id in it — which is
// what most TURN tutorials do — writes an identifier for every caller into coturn's logs
// and buys nothing: the credential is already unforgeable and already expires.
func TestTURNUsernameCarriesNoUserIdentity(t *testing.T) {
	username, _ := turnCredential("s", time.Unix(1_700_000_000, 0))
	if _, err := strconv.ParseInt(username, 10, 64); err != nil {
		t.Fatalf("username %q is not a bare expiry — it is leaking something into coturn's logs", username)
	}
}

// The shared secret must never reach a client. A leaked short-lived credential expires by
// itself; a leaked secret mints credentials forever.
func TestICEServersNeverSendTheSecret(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "caller@pheme.test")
	f.setICE(ICEConfig{
		URLs:   "stun:turn.example:3478,turn:turn.example:3478?transport=udp",
		Secret: "super-secret-value",
		TTL:    10 * time.Minute,
	})

	rec := f.do(http.MethodGet, "/v1/calls/ice-servers", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ice servers: got %d (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "super-secret-value") {
		t.Fatal("the static-auth-secret was sent to the client")
	}

	var out struct {
		ICEServers []struct {
			URLs       []string `json:"urls"`
			Username   string   `json:"username"`
			Credential string   `json:"credential"`
		} `json:"iceServers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.ICEServers) != 2 {
		t.Fatalf("expected a STUN entry and a TURN entry, got %d", len(out.ICEServers))
	}
	// STUN needs no credentials; sending them would be noise.
	if out.ICEServers[0].Username != "" || out.ICEServers[0].Credential != "" {
		t.Fatalf("STUN entry carries credentials: %+v", out.ICEServers[0])
	}
	// TURN must have them, or a relayed call cannot authenticate.
	if out.ICEServers[1].Username == "" || out.ICEServers[1].Credential == "" {
		t.Fatalf("TURN entry has no credentials: %+v", out.ICEServers[1])
	}
}

// With no TURN configured, say so. Returning an empty list instead would let the client
// try, and then fail every call behind a symmetric NAT with no explanation.
func TestICEServersRefusesWhenCallingIsNotConfigured(t *testing.T) {
	f := newFixture(t)
	_, token := f.user(t, "caller2@pheme.test")

	rec := f.do(http.MethodGet, "/v1/calls/ice-servers", token, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503 when TURN is unconfigured", rec.Code)
	}
}
