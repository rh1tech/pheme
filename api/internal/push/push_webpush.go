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
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Image string            `json:"image,omitempty"`
	Data  map[string]string `json:"data,omitempty"`
}

// Send delivers msg to each device with a Web Push subscription. Devices without
// one are reported as skipped.
func (s *WebPushSender) Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error) {
	payload, err := json.Marshal(webPushPayload{Title: msg.Title, Body: msg.Body, Image: imageURL(s.publicBaseURL, msg), Data: notificationData(msg)})
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
			TTL:             60,
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
			})
			continue
		}
		results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySent})
	}
	return results, nil
}

var _ Sender = (*WebPushSender)(nil)
