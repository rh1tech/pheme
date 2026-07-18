package push

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/rh1tech/pheme/api/internal/domain"
)

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

	results := make([]Result, 0, len(devices))
	for _, d := range devices {
		if d.WebPushSub == "" {
			results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
			continue
		}
		var sub webpush.Subscription
		if err := json.Unmarshal([]byte(d.WebPushSub), &sub); err != nil {
			results = append(results, Result{DeviceID: d.ID, Status: domain.DeliveryFailed, Error: "invalid subscription"})
			continue
		}
		resp, err := webpush.SendNotificationWithContext(ctx, payload, &sub, &webpush.Options{
			Subscriber:      s.subscriber,
			VAPIDPublicKey:  s.vapidPublic,
			VAPIDPrivateKey: s.vapidPrivate,
			TTL:             n.TTL,
			Urgency:         webpush.UrgencyHigh,
		})
		if err != nil {
			results = append(results, Result{DeviceID: d.ID, Status: domain.DeliveryFailed, Error: err.Error()})
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusBadRequest {
			results = append(results, Result{
				DeviceID: d.ID,
				Status:   domain.DeliveryFailed,
				Error:    fmt.Sprintf("push service returned %d: %s", resp.StatusCode, string(body)),
				// 404 and 410 are the Web Push spec's way of saying the subscription is gone for
				// good — the user cleared site data, or the browser dropped it. Every other status
				// may be transient and must not cost the device its registration.
				Gone: resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone,
			})
			continue
		}
		results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySent})
	}
	return results, nil
}

var _ Sender = (*WebPushSender)(nil)
