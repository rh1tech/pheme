// Command fcmprobe measures what talking to the real FCM costs.
//
// Every push number this project has is against a fake service that answered instantly and always
// succeeded. That leaves the parts only the real thing has: OAuth token acquisition, Google's
// round-trip latency, per-project rate limits, and — the one that matters most — what comes back
// when a token is no longer good, because that is the path that decides whether a dead device is
// pruned or retried forever.
//
// It sends to SYNTHETIC tokens by default. They are well-formed and belong to nobody, so FCM
// authenticates the request, parses it, and rejects the token: every part of the path is exercised
// and not one notification reaches a device. That is deliberate. Load-testing by pushing thousands
// of real notifications at somebody's phone is not a measurement, it is a prank.
//
// Usage:
//
//	fcmprobe -credentials /path/to/service-account.json -batch 50 -rounds 10 -concurrency 4
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/push"
)

func main() {
	var (
		creds       = flag.String("credentials", "", "path to the Firebase service-account JSON")
		batch       = flag.Int("batch", 50, "devices per notification (the fan-out of one message)")
		rounds      = flag.Int("rounds", 10, "notifications to send per worker")
		concurrency = flag.Int("concurrency", 4, "notifications in flight at once")
		realToken   = flag.String("real-token", "", "optionally ONE real device token, to confirm a "+
			"notification actually arrives; sent once, not in the load")
	)
	flag.Parse()

	if *creds == "" {
		fmt.Fprintln(os.Stderr, "-credentials is required")
		os.Exit(2)
	}
	if err := run(*creds, *batch, *rounds, *concurrency, *realToken); err != nil {
		fmt.Fprintf(os.Stderr, "\nfcmprobe failed: %v\n", err)
		os.Exit(1)
	}
}

func run(creds string, batch, rounds, concurrency int, realToken string) error {
	ctx := context.Background()

	// Building the sender is where the service account is read and the OAuth exchange is set up.
	// Timed separately: it happens once per process, and counting it in the per-send numbers would
	// make the first notification of a deploy look like the typical one.
	start := time.Now()
	sender, err := push.NewFCMSender(ctx, creds, "")
	if err != nil {
		return fmt.Errorf("build sender: %w", err)
	}
	fmt.Printf("sender built in %s\n", time.Since(start).Round(time.Millisecond))

	// One send before the measurement, to pay for the OAuth token and the first TLS handshake. That
	// cost is real but it is a startup cost, and averaging it into the steady state would overstate
	// every later send.
	warm := time.Now()
	if _, err := sender.SendChat(ctx, chatNote(), devices(1)); err != nil {
		return fmt.Errorf("warm-up send: %w", err)
	}
	fmt.Printf("first send (OAuth + handshake) %s\n\n", time.Since(warm).Round(time.Millisecond))

	fmt.Printf("sending %d notifications of %d devices, %d at a time — %d tokens total\n",
		rounds*concurrency, batch, concurrency, rounds*concurrency*batch)

	var (
		mu       sync.Mutex
		latency  []time.Duration
		statuses = map[string]int{}
		gone     int
		sent     int
	)

	began := time.Now()
	var wg sync.WaitGroup
	for c := 0; c < concurrency; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				devs := devices(batch)
				t0 := time.Now()
				results, err := sender.SendChat(ctx, chatNote(), devs)
				took := time.Since(t0)

				mu.Lock()
				latency = append(latency, took)
				if err != nil {
					statuses["call failed: "+err.Error()]++
				}
				for _, r := range results {
					if r.Status == domain.DeliverySent {
						sent++
						continue
					}
					if r.Gone {
						gone++
					}
					statuses[classify(r)]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(began)

	total := rounds * concurrency * batch
	fmt.Printf("\n  elapsed              %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  throughput           %.0f tokens/s\n", float64(total)/elapsed.Seconds())
	report(latency, batch)

	fmt.Printf("\n  reported as sent     %d\n", sent)
	fmt.Printf("  reported as GONE     %d\n", gone)
	fmt.Printf("\n  what FCM said:\n")
	keys := make([]string, 0, len(statuses))
	for k := range statuses {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return statuses[keys[i]] > statuses[keys[j]] })
	for _, k := range keys {
		fmt.Printf("    %6d  %s\n", statuses[k], k)
	}

	// Whether a rejected token is classified as Gone decides whether the server deletes the device
	// row. Getting it wrong in the safe direction means pushing to a dead address forever; getting
	// it wrong in the other means deleting a device that was fine.
	//
	// It was worth checking whether a synthetic token even reaches the same code path as a real
	// dead one. It does: FCM answers NotRegistered for both, so what this measures is exactly what
	// production hits when a phone uninstalls the app.
	fmt.Printf("\n  %d of %d were classified as permanently gone (FCM: NotRegistered), which is the\n", gone, total)
	fmt.Printf("  same answer it gives for a token that was once real and is now dead — so this\n")
	fmt.Printf("  exercises the real pruning path, not a near-miss of it.\n")

	if realToken != "" {
		fmt.Printf("\nsending ONE notification to the supplied real token...\n")
		results, err := sender.SendChat(ctx, chatNote(), []domain.Device{{
			ID: "real-device", Platform: domain.PlatformAndroid, FCMToken: realToken,
		}})
		if err != nil {
			return fmt.Errorf("real send: %w", err)
		}
		for _, r := range results {
			fmt.Printf("  status=%s gone=%v err=%s\n", r.Status, r.Gone, r.Error)
		}
	}
	return nil
}

func report(latency []time.Duration, batch int) {
	if len(latency) == 0 {
		return
	}
	sort.Slice(latency, func(i, j int) bool { return latency[i] < latency[j] })
	at := func(p float64) time.Duration {
		i := int(float64(len(latency)-1) * p)
		return latency[i].Round(time.Millisecond)
	}
	fmt.Printf("  per-notification latency (a fan-out of %d devices)\n", batch)
	fmt.Printf("    p50 %-8s p95 %-8s p99 %-8s max %s\n", at(0.50), at(0.95), at(0.99), at(1.0))
}

// classify collapses FCM's error to its kind, so the summary is a handful of lines rather than one
// per token.
//
// It reads Gone rather than matching the error text for that case. The text FCM actually returns is
// "NotRegistered", which an earlier version of this did not match — so the very finding that
// matters most, a token the server will delete the device for, was printed as an unrecognised
// string. The server's own classification is the authority here; the probe should report it, not
// re-derive it.
func classify(r push.Result) string {
	if r.Gone {
		return "NotRegistered — token is dead; the server prunes this device"
	}
	err := r.Error
	switch {
	case err == "":
		return "(no error text)"
	case strings.Contains(err, "not a valid FCM registration token"),
		strings.Contains(err, "invalid-argument"):
		return "malformed token"
	case strings.Contains(err, "quota"), strings.Contains(err, "rate"):
		return "RATE LIMITED"
	case strings.Contains(err, "Unavailable"), strings.Contains(err, "unavailable"):
		return "FCM unavailable (transient)"
	}
	return err
}

// devices builds n synthetic Android devices. The tokens are shaped like FCM's own — an instance id,
// a colon, then a long opaque blob — so the request is well-formed and the rejection comes from the
// token being unknown rather than from the shape of it.
func devices(n int) []domain.Device {
	out := make([]domain.Device, n)
	for i := range out {
		out[i] = domain.Device{
			ID:       fmt.Sprintf("probe-%d", i),
			Platform: domain.PlatformAndroid,
			FCMToken: syntheticToken(),
		}
	}
	return out
}

func syntheticToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	blob := make([]byte, 96)
	_, _ = rand.Read(blob)
	return base64.RawURLEncoding.EncodeToString(b) + ":APA91b" +
		base64.RawURLEncoding.EncodeToString(blob)
}

func chatNote() push.ChatNotification {
	return push.ChatNotification{
		ConversationID: "probe-conversation",
		MessageID:      "probe-message",
		SenderName:     "Probe",
	}
}
