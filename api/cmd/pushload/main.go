// Command pushload measures the notification path under realistic push-service latency.
//
// The load test in cmd/loadtest runs with the log push driver, so every number it produced excluded
// the part of a message that actually costs money and time: one outbound HTTPS request per
// recipient device, to a service on the other side of the internet. That omission is the largest
// unknown in the sizing report, because it is the only part of sending a message that is not
// bounded by this server's own CPU.
//
// This drives the real chat push path — the real semaphore, the real per-notification concurrency,
// the real fan-out — against a push service that answers with a latency you choose. What it cannot
// tell you is how FCM or APNs behave: their auth overhead, their rate limits, their error rates.
// What it can tell you is what this server does when a push takes 80ms instead of nothing, which is
// where the drops come from.
//
// The number to watch is dropped notifications. Sending is bounded by a process-wide semaphore, and
// past it a notification is discarded rather than queued — deliberately, because a stale
// notification is worth less than a responsive server. That trade is only sound if the ceiling is
// far from where real traffic sits, and nothing had ever measured where it sits.
//
// Usage:
//
//	pushload -api http://localhost:8090 -admin-email a@b.c -admin-password ... \
//	         -users 200 -devices-per-user 2 -latency 80ms -rate 20 -duration 60s
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

type options struct {
	api            string
	adminEmail     string
	adminPassword  string
	users          int
	devicesPerUser int
	groupSize      int
	latency        time.Duration
	rate           float64
	duration       time.Duration
	drain          time.Duration
	emailDomain    string
	password       string
}

func main() {
	var o options
	flag.StringVar(&o.api, "api", "http://localhost:8090", "base URL of the API under test")
	flag.StringVar(&o.adminEmail, "admin-email", "", "an existing admin, used to provision users")
	flag.StringVar(&o.adminPassword, "admin-password", "", "that admin's password")
	flag.IntVar(&o.users, "users", 100, "number of users to simulate")
	flag.IntVar(&o.devicesPerUser, "devices-per-user", 2, "push devices registered per user")
	flag.IntVar(&o.groupSize, "group-size", 10, "members per conversation")
	flag.DurationVar(&o.latency, "latency", 80*time.Millisecond, "how long the fake push service takes to answer")
	flag.Float64Var(&o.rate, "rate", 10, "messages per second across the run")
	flag.DurationVar(&o.duration, "duration", 60*time.Second, "how long to hold the rate")
	flag.DurationVar(&o.drain, "drain", 15*time.Second, "how long to keep counting after the last send")
	flag.StringVar(&o.emailDomain, "email-domain", "pushload.local", "domain for provisioned accounts")
	flag.StringVar(&o.password, "password", "PushLoad12345", "password for provisioned accounts")
	flag.Parse()

	if o.adminEmail == "" || o.adminPassword == "" {
		fmt.Fprintln(os.Stderr, "-admin-email and -admin-password are required")
		os.Exit(2)
	}

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "\npush load test failed: %v\n", err)
		os.Exit(1)
	}
}

// pushService stands in for FCM or a browser's push endpoint: it answers after a delay and counts
// what it was asked to deliver.
type pushService struct {
	delivered atomic.Int64
	inFlight  atomic.Int64
	peak      atomic.Int64
	latency   time.Duration
	srv       *httptest.Server
}

func newPushService(latency time.Duration) *pushService {
	p := &pushService{latency: latency}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		now := p.inFlight.Add(1)
		for {
			old := p.peak.Load()
			if now <= old || p.peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(p.latency)
		p.inFlight.Add(-1)
		p.delivered.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	return p
}

func (p *pushService) close() { p.srv.Close() }

func run(o options) error {
	ctx := context.Background()
	c := newClient(o.api)

	push := newPushService(o.latency)
	defer push.close()

	fmt.Printf("target        %s\n", o.api)
	fmt.Printf("push service  %s, answering in %s\n", push.srv.URL, o.latency)

	admin, err := c.login(ctx, o.adminEmail, o.adminPassword)
	if err != nil {
		return fmt.Errorf("admin login: %w", err)
	}

	world, err := provision(ctx, c, admin, push.srv.URL, o)
	if err != nil {
		return fmt.Errorf("provision: %w", err)
	}

	// Every message goes to the whole conversation except its sender, on every device.
	perMessage := (o.groupSize - 1) * o.devicesPerUser
	fmt.Printf("\nsending %.0f msg/s for %s — %d notifications per message, %d/s of push\n",
		o.rate, o.duration, perMessage, int(o.rate)*perMessage)

	before := push.delivered.Load()
	sent := sendFor(ctx, c, world, o)

	fmt.Printf("draining for %s...\n", o.drain)
	time.Sleep(o.drain)

	delivered := push.delivered.Load() - before
	expected := int64(sent) * int64(perMessage)

	fmt.Printf("\n  messages sent        %d\n", sent)
	fmt.Printf("  notifications due    %d\n", expected)
	fmt.Printf("  notifications sent   %d\n", delivered)
	fmt.Printf("  peak concurrent push %d\n", push.peak.Load())

	if expected > 0 {
		dropped := expected - delivered
		pct := 100 * float64(dropped) / float64(expected)
		fmt.Printf("  DROPPED              %d (%.1f%%)\n", dropped, pct)
		switch {
		case dropped <= 0:
			fmt.Printf("\n  VERDICT: every notification went out. The push ceiling is comfortably above\n" +
				"  this load.\n")
		case pct < 1:
			fmt.Printf("\n  VERDICT: a fraction of notifications were dropped. This is the edge of the\n" +
				"  ceiling — real traffic should sit well below it, not here.\n")
		default:
			fmt.Printf("\n  VERDICT: %.1f%% of notifications never left the server, and nothing in any\n"+
				"  HTTP response said so. Every send returned 201. At this rate a phone that should\n"+
				"  have buzzed simply does not.\n", pct)
		}
	}
	return nil
}

// ---- the small API client this needs -------------------------------------------------

type client struct {
	base string
	http *http.Client
}

func newClient(base string) *client {
	return &client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{MaxIdleConns: 2048, MaxIdleConnsPerHost: 2048},
		},
	}
}

func (c *client) do(ctx context.Context, method, path, token string, in, out any) error {
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
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *client) login(ctx context.Context, email, password string) (string, error) {
	var t struct {
		AccessToken string `json:"accessToken"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/auth/login", "",
		map[string]string{"email": email, "password": password}, &t)
	return t.AccessToken, err
}
