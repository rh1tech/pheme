package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A minimal client for the endpoints the load generator drives. It deliberately speaks raw HTTP
// rather than importing the handlers: the point is to measure the deployed server, including its
// reverse proxy, TLS and connection limits, not a handler in this process.

type apiClient struct {
	base string
	http *http.Client
}

func newAPIClient(base string) *apiClient {
	return &apiClient{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// The default of 2 idle connections per host would serialise thousands of senders
				// behind a handful of sockets and measure the client, not the server.
				MaxIdleConns:        4096,
				MaxIdleConnsPerHost: 4096,
				MaxConnsPerHost:     0,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("http %d: %s", e.Status, e.Body) }

// do sends a JSON request and decodes a JSON response. A nil out discards the body.
func (c *apiClient) do(ctx context.Context, method, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &apiError{Status: resp.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

type tokens struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

func (c *apiClient) login(ctx context.Context, email, password string) (tokens, error) {
	var t tokens
	err := c.do(ctx, http.MethodPost, "/v1/auth/login", "",
		map[string]string{"email": email, "password": password}, &t)
	return t, err
}

type userRef struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// createUser provisions an account through the admin API, which is how the E2E suite does it too.
// Registration would need an emailed one-time code, and a load run needs thousands of accounts.
// The response carries the new id, so provisioning costs one request per user rather than two.
func (c *apiClient) createUser(ctx context.Context, adminToken, email, password string) (userRef, error) {
	var u userRef
	err := c.do(ctx, http.MethodPost, "/v1/admin/users", adminToken,
		map[string]string{"email": email, "password": password, "role": "user"}, &u)
	return u, err
}

// findUser resolves an account that already existed from an earlier run. The admin list is used
// rather than user search, which is scoped to people you can start a chat with.
func (c *apiClient) findUser(ctx context.Context, adminToken, email string) (userRef, error) {
	var out struct {
		Users []userRef `json:"users"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/admin/users?q="+url.QueryEscape(email), adminToken, nil, &out)
	if err != nil {
		return userRef{}, err
	}
	for _, u := range out.Users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return userRef{}, fmt.Errorf("user %s exists but could not be found in the admin list", email)
}

type conversation struct {
	ID string `json:"id"`
}

func (c *apiClient) createGroup(ctx context.Context, token, name string, memberIDs []string) (conversation, error) {
	var conv conversation
	err := c.do(ctx, http.MethodPost, "/v1/conversations", token, map[string]any{
		"kind": "group", "name": name, "memberIds": memberIDs,
	}, &conv)
	return conv, err
}

type chatMessage struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	SenderID       string    `json:"senderId"`
	Ciphertext     []byte    `json:"ciphertext"`
	ContentType    string    `json:"contentType"`
	CreatedAt      time.Time `json:"createdAt"`
}

// postMessage sends one opaque ciphertext. The server never decrypts, so random bytes exercise
// exactly the same path as a real MLS message of the same size.
func (c *apiClient) postMessage(ctx context.Context, token, convID string, ciphertext []byte) (chatMessage, error) {
	var msg chatMessage
	err := c.do(ctx, http.MethodPost, "/v1/conversations/"+convID+"/messages", token, map[string]any{
		"ciphertext": ciphertext, "contentType": "application/octet-stream",
	}, &msg)
	return msg, err
}

// streamEvent is the shape the SSE endpoint emits, narrowed to what the generator measures.
type streamEvent struct {
	ConversationID string       `json:"conversationId,omitempty"`
	ChatMessage    *chatMessage `json:"chatMessage,omitempty"`
}

// stream opens an SSE connection and calls onEvent for every message frame until the context is
// cancelled or the server hangs up. It returns the reason it stopped.
//
// The token goes in the query string because EventSource cannot set headers — the same way the real
// clients connect, so this exercises the real code path.
func (c *apiClient) stream(ctx context.Context, token string, onOpen func(), onEvent func(streamEvent)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/stream?token="+token, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	// No client timeout on a stream: the shared client's 30s ceiling would sever every connection
	// mid-run and turn a healthy server into a fake failure.
	streamer := &http.Client{Transport: c.http.Transport}
	resp, err := streamer.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return &apiError{Status: resp.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}
	if onOpen != nil {
		onOpen()
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue // comments (": ping") and event: lines
		}
		var e streamEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e); err != nil {
			continue
		}
		onEvent(e)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}
