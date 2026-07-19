// Command loadtest drives a deployed Pheme server hard enough to find out what it costs to run.
//
// It answers one question: how much server does N concurrent users need, and what breaks first.
//
// The workload is the real one. Users are provisioned through the admin API, each opens a real SSE
// stream with a real token, and messages are posted as opaque ciphertext — which is exactly what a
// real MLS message is to this server, since it never decrypts anything. What is measured is what a
// user would feel: how long a send takes, how long until the other side's stream shows it, and how
// often it never shows up at all.
//
// That last number is the one to watch. The live bus drops events silently when a subscriber's
// buffer fills: no error, no retry, nothing visible in the HTTP response. A load test that measures
// only POST latency will report a perfect run while users lose messages.
//
// Usage:
//
//	loadtest -api http://localhost:8080 -admin-email a@b.c -admin-password ... \
//	         -users 200 -group-size 10 -rate 50 -duration 60s
//
//	loadtest ... -ramp 10,25,50,100,200   # step the rate until something gives
//
// Provisioned accounts are named loadtest-<run>-<n>@<domain> so a run is identifiable and
// removable afterwards.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type options struct {
	api            string
	adminEmail     string
	adminPassword  string
	users          int
	streamsPerUser int
	groupSize      int
	rate           float64
	ramp           string
	duration       time.Duration
	msgBytes       int
	emailDomain    string
	password       string
	drain          time.Duration
	keepUsers      bool
}

func main() {
	var o options
	flag.StringVar(&o.api, "api", "http://localhost:8080", "base URL of the API to hit")
	flag.StringVar(&o.adminEmail, "admin-email", "", "an existing admin account, used to provision load users")
	flag.StringVar(&o.adminPassword, "admin-password", "", "that admin's password")
	flag.IntVar(&o.users, "users", 100, "number of concurrent users to simulate")
	flag.IntVar(&o.streamsPerUser, "streams-per-user", 1, "SSE connections per user (a user's devices)")
	flag.IntVar(&o.groupSize, "group-size", 10, "members per conversation")
	flag.Float64Var(&o.rate, "rate", 20, "messages per second across the whole run")
	flag.StringVar(&o.ramp, "ramp", "", "comma-separated rates to step through instead of -rate, e.g. 10,50,100")
	flag.DurationVar(&o.duration, "duration", 60*time.Second, "how long to hold each rate")
	flag.IntVar(&o.msgBytes, "msg-bytes", 1024, "ciphertext size per message")
	flag.StringVar(&o.emailDomain, "email-domain", "loadtest.local", "domain for provisioned accounts")
	flag.StringVar(&o.password, "password", "LoadTest12345", "password for provisioned accounts")
	flag.DurationVar(&o.drain, "drain", 10*time.Second, "how long to keep streams open after the last send")
	flag.BoolVar(&o.keepUsers, "keep-users", false, "do not report the provisioned accounts as reusable")
	flag.Parse()

	if o.adminEmail == "" || o.adminPassword == "" {
		fmt.Fprintln(os.Stderr, "-admin-email and -admin-password are required: load users are provisioned through the admin API")
		os.Exit(2)
	}
	if o.groupSize < 2 {
		fmt.Fprintln(os.Stderr, "-group-size must be at least 2")
		os.Exit(2)
	}
	if o.groupSize > o.users {
		fmt.Fprintf(os.Stderr, "-group-size (%d) cannot exceed -users (%d)\n", o.groupSize, o.users)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, o); err != nil {
		fmt.Fprintf(os.Stderr, "\nload test failed: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options) error {
	c := newAPIClient(o.api)

	fmt.Printf("target      %s\n", o.api)
	admin, err := c.login(ctx, o.adminEmail, o.adminPassword)
	if err != nil {
		return fmt.Errorf("admin login: %w", err)
	}

	world, err := provision(ctx, c, admin.AccessToken, o)
	if err != nil {
		return fmt.Errorf("provision: %w", err)
	}
	defer world.close()

	rates, err := parseRates(o)
	if err != nil {
		return err
	}

	for i, rate := range rates {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(rates) > 1 {
			fmt.Printf("\n──────── step %d/%d: %.0f msg/s ────────\n", i+1, len(rates), rate)
		}
		res := measure(ctx, c, world, o, rate)
		res.report(o, rate)
		if res.brokenDown() {
			fmt.Printf("\nStopping the ramp: the server is past its limit at %.0f msg/s.\n", rate)
			break
		}
	}
	return nil
}

func parseRates(o options) ([]float64, error) {
	if strings.TrimSpace(o.ramp) == "" {
		return []float64{o.rate}, nil
	}
	var out []float64
	for _, part := range strings.Split(o.ramp, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("bad -ramp value %q: %w", part, err)
		}
		if v <= 0 {
			return nil, fmt.Errorf("-ramp values must be positive, got %v", v)
		}
		out = append(out, v)
	}
	return out, nil
}

// randomBytes returns n bytes of incompressible filler, standing in for ciphertext.
func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail on any platform this runs on; if it ever did, a load test
		// sending zeroes would still be measuring the right thing, but say so.
		fmt.Fprintf(os.Stderr, "warning: crypto/rand failed (%v); sending zero-filled payloads\n", err)
	}
	return b
}

// waitFor polls until cond is true or the deadline passes. Used instead of a fixed sleep so a fast
// machine is not punished and a slow one is not measured mid-setup.
func waitFor(ctx context.Context, timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return cond()
}

// errJoin collects errors from concurrent workers without losing any of them.
type errJoin struct {
	mu   sync.Mutex
	errs []error
}

func (e *errJoin) add(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	e.errs = append(e.errs, err)
	e.mu.Unlock()
}

func (e *errJoin) err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return errors.Join(e.errs...)
}
