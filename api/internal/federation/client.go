package federation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client calls peer hosts, signing every request with this host's key.
//
// It does not itself decide who is a peer or where they are — the caller passes
// a base URL discovered via the peer's `.well-known` directory. What it
// guarantees is that every request it sends is authenticated as coming from this
// host, so a peer can verify it against the nodelist.
type Client struct {
	origin string
	keyID  string
	key    ed25519.PrivateKey
	http   *http.Client
	now    func() time.Time
	// PeerURL maps a peer domain to its base URL. Defaults to https://<domain>,
	// which is the production convention; overridable so a test can point at a
	// loopback server, or an operator at a peer reachable at a non-default URL.
	PeerURL func(domain string) string
}

// NewClient builds a signing client for this host's identity.
func NewClient(origin, keyID string, key ed25519.PrivateKey) *Client {
	return &Client{
		origin: origin,
		keyID:  keyID,
		key:    key,
		// A bounded timeout on every S2S call: a peer that hangs must not be
		// able to tie up a request here indefinitely.
		http:    &http.Client{Timeout: 10 * time.Second},
		now:     time.Now,
		PeerURL: PeerBaseURL,
	}
}

// Do sends a signed request to peer and returns the response body, or an error
// for any non-2xx. reqBody may be nil.
//
// peer is the domain as the NODELIST spells it, and path is the endpoint. Both
// are signed, so neither the URL this resolves to (which may be a loopback
// address in a test harness, or a proxy in production) nor a rewritten path
// changes what the receiver verifies against.
func (c *Client) Do(ctx context.Context, peer, method, path string, reqBody []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.PeerURL(peer)+path, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := Sign(req, c.origin, peer, c.keyID, c.key, reqBody, c.now()); err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Bounded read: a peer must not be able to make this host allocate without
	// limit by streaming a huge response.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("federation: %s %s%s: peer returned %d", method, peer, path, resp.StatusCode)
	}
	return body, nil
}

// GetJSON sends a signed GET to peer and decodes a JSON response into out.
func (c *Client) GetJSON(ctx context.Context, peer, path string, out any) error {
	body, err := c.Do(ctx, peer, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// PostJSON sends a signed POST to peer with a JSON body and decodes a JSON response.
func (c *Client) PostJSON(ctx context.Context, peer, path string, in, out any) error {
	reqBody, err := json.Marshal(in)
	if err != nil {
		return err
	}
	respBody, err := c.Do(ctx, peer, http.MethodPost, path, reqBody)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(respBody, out)
}
