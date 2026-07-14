package chat

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rh1tech/pheme/api/internal/httpx"
)

// ICE servers for 1:1 voice calls.
//
// Calls are peer to peer: the media never touches this server. What a browser needs from
// us before it can find its peer is a STUN server (to learn its own public address) and,
// for the pairs that cannot reach each other directly at all — symmetric NAT, which is
// most mobile carriers — a TURN relay to fall back on. TURN is the one and only thing
// that ever puts call media on our machine, and it is used for a minority of calls.
//
// The interesting part is the credential. coturn's `use-auth-secret` mode does not hold a
// user list: it accepts any username of the form "<unix-expiry>" whose password is
// HMAC-SHA1(secret, username). So we can mint a short-lived credential here with no state
// and no round trip to coturn — and the shared secret never leaves the server. A
// credential that leaks expires on its own and cannot be reversed into the secret.
//
// Deliberately, the username carries ONLY the expiry. Putting the user id in it (the shape
// most tutorials use) writes an identifier for every caller into coturn's logs and buys
// nothing: the credential is already unforgeable and already expires.

// ICEConfig is what the handler needs to mint credentials. Empty URLs disable calling.
type ICEConfig struct {
	// URLs is the comma-separated ICE URL list, e.g.
	// "stun:turn.example:3478,turn:turn.example:3478?transport=udp".
	URLs string
	// Secret is coturn's static-auth-secret. Never sent to a client.
	Secret string
	// TTL is how long an issued credential is valid.
	TTL time.Duration
}

type iceServer struct {
	URLs []string `json:"urls"`
	// Username and Credential are set only on turn:/turns: entries. A STUN server needs
	// no authentication, and sending it credentials would be noise.
	Username   string `json:"username,omitempty"`
	Credential string `json:"credential,omitempty"`
}

// iceServers hands the caller a set of ICE servers with a short-lived TURN credential.
//
// Rate limited, because it mints credentials: without a limit it is a free TURN-credential
// vending machine for anyone with a login.
func (h *Handler) iceServers(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if h.ICE.URLs == "" {
		// Say so plainly rather than returning an empty list that the client would read as
		// "no TURN available" and then fail every call behind a symmetric NAT with no
		// explanation.
		httpx.Error(w, http.StatusServiceUnavailable, "calling is not configured on this server")
		return
	}
	if h.Limiter != nil && !h.Limiter.Allow("ice:"+uid) {
		httpx.Error(w, http.StatusTooManyRequests, "slow down")
		return
	}

	// "direct" means calling is on but there is nothing to hand out: both ends are expected to
	// reach each other on their own addresses. That is true on a LAN, and it is true of the
	// two browsers in the end-to-end suite — where naming a STUN server that is not actually
	// there costs ten seconds per call while the ICE agent waits for it to time out.
	//
	// It is NOT true of the internet, and this is not a default. A deployment that wants calls
	// to work for people behind NAT needs real STUN and TURN URLs.
	if strings.TrimSpace(h.ICE.URLs) == "direct" {
		httpx.JSON(w, http.StatusOK, map[string]any{"iceServers": []iceServer{}})
		return
	}

	username, credential := turnCredential(h.ICE.Secret, time.Now().Add(h.ICE.TTL))

	servers := make([]iceServer, 0, 2)
	var stun, turn []string
	for _, raw := range strings.Split(h.ICE.URLs, ",") {
		url := strings.TrimSpace(raw)
		if url == "" {
			continue
		}
		if strings.HasPrefix(url, "stun:") {
			stun = append(stun, url)
		} else {
			turn = append(turn, url)
		}
	}
	if len(stun) > 0 {
		servers = append(servers, iceServer{URLs: stun})
	}
	if len(turn) > 0 {
		servers = append(servers, iceServer{URLs: turn, Username: username, Credential: credential})
	}

	httpx.JSON(w, http.StatusOK, map[string]any{"iceServers": servers})
}

// turnCredential mints a coturn `use-auth-secret` credential valid until `expiry`.
//
// The scheme is coturn's REST API convention (and the same one Twilio, Cloudflare and
// every other TURN provider implements): the username IS the expiry, and the password is
// its HMAC under the shared secret. coturn recomputes the HMAC to check the password and
// reads the username to check the clock — so it stores nothing, and neither do we.
func turnCredential(secret string, expiry time.Time) (username, credential string) {
	username = strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username))
	return username, base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
