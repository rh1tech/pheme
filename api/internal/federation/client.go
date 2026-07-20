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

// Do sends a signed request to a peer and returns the response body, or an error
// for any non-2xx. reqBody may be nil.
func (c *Client) Do(ctx context.Context, method, url string, reqBody []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	Sign(req, c.origin, c.keyID, c.key, reqBody, c.now())

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
		return nil, fmt.Errorf("federation: %s %s: peer returned %d", method, url, resp.StatusCode)
	}
	return body, nil
}

// GetJSON sends a signed GET and decodes a JSON response into out.
func (c *Client) GetJSON(ctx context.Context, url string, out any) error {
	body, err := c.Do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// PostJSON sends a signed POST with a JSON body and decodes a JSON response.
func (c *Client) PostJSON(ctx context.Context, url string, in, out any) error {
	reqBody, err := json.Marshal(in)
	if err != nil {
		return err
	}
	respBody, err := c.Do(ctx, http.MethodPost, url, reqBody)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(respBody, out)
}
