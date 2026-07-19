package push

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// webPushConcurrency is how many push services this will talk to at once for a single
// notification. Enough that a large group does not serialise into a long queue of round-trips;
// small enough not to look like an attack to any one push service.
const webPushConcurrency = 16

// pushHTTPClient is the client every Web Push request goes through, and it exists because the
// default is badly wrong for this workload.
//
// webpush-go builds `&http.Client{}` when no client is supplied. That has a nil Transport, so it
// uses http.DefaultTransport — whose MaxIdleConnsPerHost is 2. Notifications go to a handful of
// push hosts and often to just one, so past two concurrent sends to the same host every further
// notification opened a fresh TCP connection, completed a full TLS handshake, and then closed it
// because there was no room to keep it idle.
//
// The cost lands in three places at once: a handshake's worth of latency on almost every
// notification, the CPU to perform it, and a socket left in TIME_WAIT afterwards. Under load the
// third one is what breaks first — a push run against a local service exhausted the machine's
// ephemeral ports and started failing with "can't assign requested address", which is the same
// wall a server would hit talking to FCM, only sooner because the round trip is shorter.
//
// The pool is sized above the real concurrency ceiling (pushWorkers x webPushConcurrency) so that
// connections are reused rather than rebuilt. Idle connections are cheap; handshakes are not.
var pushHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		MaxIdleConns:        1024,
		MaxIdleConnsPerHost: 256,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ForceAttemptHTTP2:   true,
	},
	// No Timeout here on purpose: every send already carries a context with the caller's deadline,
	// and a second, blunter bound would only cut some of those short.
}

// WebPushSender delivers notifications to browsers via the Web Push protocol
// using VAPID authentication. A device's WebPushSub field holds the JSON
// PushSubscription produced by the browser.
type WebPushSender struct {
	vapidPublic   string
	vapidPrivate  string
	subscriber    string // mailto: or URL contact, per VAPID spec
	publicBaseURL string
}

// NewWebPushSender configures a sender with the server's VAPID key pair and
// subscriber contact. publicBaseURL (may be empty) is the base used to build
// absolute image URLs for notifications.
func NewWebPushSender(vapidPublic, vapidPrivate, subscriber, publicBaseURL string) *WebPushSender {
	return &WebPushSender{vapidPublic: vapidPublic, vapidPrivate: vapidPrivate, subscriber: subscriber, publicBaseURL: publicBaseURL}
}

type webPushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// A channel post's photograph, for the notification's hero slot.
	Image string `json:"image,omitempty"`
	// A sender's avatar, for the small round slot. See notification.Icon.
	Icon string            `json:"icon,omitempty"`
	Data map[string]string `json:"data,omitempty"`
}

// Send delivers a channel message to each device with a Web Push subscription.
func (s *WebPushSender) Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error) {
	return s.send(ctx, messageNotification(s.publicBaseURL, msg), devices)
}

// SendChat delivers a conversation notification — who sent it, never what it said.
func (s *WebPushSender) SendChat(ctx context.Context, n ChatNotification, devices []domain.Device) ([]Result, error) {
	return s.send(ctx, chatNotificationPayload(s.publicBaseURL, n), devices)
}

// send delivers one notification to each device with a Web Push subscription.
// Devices without one are reported as skipped.
func (s *WebPushSender) send(ctx context.Context, n notification, devices []domain.Device) ([]Result, error) {
	payload, err := json.Marshal(webPushPayload{
		Title: n.Title, Body: n.Body, Image: n.Image, Icon: n.Icon, Data: n.Data,
	})
	if err != nil {
		return nil, err
	}

	// Sent CONCURRENTLY, a bounded number at a time.
	//
	// This was a plain sequential loop: one HTTPS round-trip to a push service per device, each
	// waiting for the last. A notification for a conversation of ten people with two devices each
	// meant twenty round-trips end to end, inside a fifteen-second budget, while holding one of the
	// process's sixty-four push slots the whole time. The slots are what the server drops
	// notifications from when they run out, so the sequential loop was not just slow for one
	// notification — it was the reason others got dropped.
	//
	// The bound matters as much as the concurrency: unbounded, a large group would open a
	// connection per device to whichever push services its members use, which is how a sender
	// arranges to be rate-limited by them.
	results := make([]Result, len(devices))
	sem := make(chan struct{}, webPushConcurrency)
	var wg sync.WaitGroup

	for i, d := range devices {
		if d.WebPushSub == "" {
			results[i] = Result{DeviceID: d.ID, Status: domain.DeliverySkipped}
			continue
		}
		wg.Add(1)
		go func(i int, d domain.Device) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i] = Result{DeviceID: d.ID, Status: domain.DeliveryFailed, Error: ctx.Err().Error()}
				return
			}
			// Written by exactly one goroutine per index, read only after wg.Wait, so no lock is
			// needed and none is taken on the hot path.
			results[i] = s.sendOne(ctx, payload, n, d)
		}(i, d)
	}
	wg.Wait()

	return results, nil
}

// sendOne delivers to a single device and reports what happened to it.
func (s *WebPushSender) sendOne(ctx context.Context, payload []byte, n notification, d domain.Device) Result {
	// Each send gets its OWN copy of the payload, because the library takes ownership of the slice
	// it is handed: internally it does bytes.NewBuffer(message) and then appends the encryption
	// padding, which writes into the slice's spare capacity. Sharing one payload across concurrent
	// sends is a data race on the caller's own buffer — the race detector catches it immediately —
	// and worse, one send can be padding the bytes another is encrypting. Sequentially this was
	// invisible, which is exactly why it survived into a concurrent version.
	//
	// A notification is a few hundred bytes, so the copy is not worth avoiding.
	mine := make([]byte, len(payload))
	copy(mine, payload)

	var sub webpush.Subscription
	if err := json.Unmarshal([]byte(d.WebPushSub), &sub); err != nil {
		return Result{DeviceID: d.ID, Status: domain.DeliveryFailed, Error: "invalid subscription"}
	}
	resp, err := webpush.SendNotificationWithContext(ctx, mine, &sub, &webpush.Options{
		Subscriber:      s.subscriber,
		VAPIDPublicKey:  s.vapidPublic,
		VAPIDPrivateKey: s.vapidPrivate,
		TTL:             n.TTL,
		Urgency:         webpush.UrgencyHigh,
		HTTPClient:      pushHTTPClient,
	})
	if err != nil {
		return Result{DeviceID: d.ID, Status: domain.DeliveryFailed, Error: err.Error()}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	_ = resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return Result{
			DeviceID: d.ID,
			Status:   domain.DeliveryFailed,
			Error:    fmt.Sprintf("push service returned %d: %s", resp.StatusCode, string(body)),
			// 404 and 410 are the Web Push spec's way of saying the subscription is gone for
			// good — the user cleared site data, or the browser dropped it. Every other status
			// may be transient and must not cost the device its registration.
			Gone: resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone,
		}
	}
	return Result{DeviceID: d.ID, Status: domain.DeliverySent}
}

var _ Sender = (*WebPushSender)(nil)
