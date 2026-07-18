package push

import (
	"context"
	"fmt"
	"strconv"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// FCMSender delivers notifications to devices that have an FCM registration
// token (Android, iOS via APNs, and Chrome/Firefox web when using FCM).
type FCMSender struct {
	client        *messaging.Client
	publicBaseURL string
}

// NewFCMSender initialises the Firebase Admin messaging client from a
// service-account credentials file. publicBaseURL (may be empty) is the base used
// to build absolute image URLs for notifications.
func NewFCMSender(ctx context.Context, credentialsFile, publicBaseURL string) (*FCMSender, error) {
	app, err := firebase.NewApp(ctx, nil, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("firebase init: %w", err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("firebase messaging: %w", err)
	}
	return &FCMSender{client: client, publicBaseURL: publicBaseURL}, nil
}

// Send delivers a channel message to each device with an FCM token.
func (s *FCMSender) Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error) {
	return s.send(ctx, messageNotification(s.publicBaseURL, msg), devices)
}

// SendChat delivers a conversation notification — who sent it, never what it said.
func (s *FCMSender) SendChat(ctx context.Context, n ChatNotification, devices []domain.Device) ([]Result, error) {
	return s.send(ctx, chatNotificationPayload(s.publicBaseURL, n), devices)
}

// send delivers one notification to every device with an FCM token, using a single
// batch request. Devices without a token are reported as skipped.
func (s *FCMSender) send(ctx context.Context, n notification, devices []domain.Device) ([]Result, error) {
	results := make([]Result, 0, len(devices))
	var batch []*messaging.Message
	var batched []string // device IDs aligned with batch order

	for _, d := range devices {
		if d.FCMToken == "" {
			results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
			continue
		}
		batch = append(batch, buildMessage(d.FCMToken, n))
		batched = append(batched, d.ID)
	}

	if len(batch) == 0 {
		return results, nil
	}

	resp, err := s.client.SendEach(ctx, batch)
	if err != nil {
		// Whole batch failed; mark every batched device as failed.
		for _, id := range batched {
			results = append(results, Result{DeviceID: id, Status: domain.DeliveryFailed, Error: err.Error()})
		}
		return results, err
	}

	for i, r := range resp.Responses {
		res := Result{DeviceID: batched[i], Status: domain.DeliverySent}
		if !r.Success {
			res.Status = domain.DeliveryFailed
			if r.Error != nil {
				res.Error = r.Error.Error()
			}
		}
		results = append(results, res)
	}
	return results, nil
}

// buildMessage turns a notification into an FCM message with the per-platform delivery options that
// decide whether it arrives in time to be worth anything.
//
// The message carried none of these. Every push went out as a plain `notification` message with no
// AndroidConfig and no APNSConfig, which means default priority and no expiry — and the consequence
// was not that calls rang late, it was that a call could not ring a sleeping phone at all. Two
// separate reasons, and both had to be fixed:
//
//   - ANDROID. A normal-priority message is held by Doze and App Standby until the device next comes
//     out of idle, which may be an hour. Only a HIGH-priority message punches through. And it must be
//     DATA-ONLY: a message with a `notification` payload is rendered by the system tray while the app
//     is backgrounded and does NOT reliably start the app's background handler — but starting that
//     handler is the entire job here, because it is what raises the ringer.
//
//   - iOS. An alert push arrives at whatever priority APNs feels like and cannot show a call screen.
//     A real ringing call needs PushKit, which FCM cannot reach (see domain.Device.VoIPToken and
//     push_apns_voip.go). What is set here is the FALLBACK for an iPhone with no PushKit token: a
//     time-sensitive, high-priority alert — a banner rather than a call screen, but at least a prompt
//     one.
//
// A TTL matters for the same reason a call matters: it is worthless the moment it stops ringing.
// Delivering it two minutes later shows somebody an incoming call that no longer exists.
func buildMessage(token string, n notification) *messaging.Message {
	ttl := time.Duration(n.TTL) * time.Second
	expiry := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)

	msg := &messaging.Message{Token: token, Data: n.Data}

	// A notification payload is what makes Android's system tray draw the push itself, and that is
	// exactly what has to NOT happen when the device is the only thing that can read the message.
	// Two different reasons to withhold it, with the same mechanics:
	//
	//   - a CALL must start the background handler that raises the ringer;
	//   - a PREVIEW must start the handler that decrypts the body.
	//
	// Both then need high priority too, or Doze holds the data message until the phone next wakes
	// and the notification arrives an hour late.
	//
	// iOS is unaffected either way: its alert lives in the APNs payload below, not here, so an
	// iPhone still gets a banner it can show without any of this.
	trayRendered := !n.Urgent && !n.ClientRendered

	priority := "normal"
	if n.Urgent || n.ClientRendered {
		priority = "high"
	}
	if trayRendered {
		msg.Notification = &messaging.Notification{
			Title: n.Title,
			Body:  n.Body,
			// Image ONLY — never Icon. FCM's image is the full-width hero picture, and an avatar
			// put there fills the notification with an enlarged face. FCM has no field for a small
			// round icon from a URL at all, so a chat's avatar is carried in the data instead and
			// drawn by the client as a largeIcon (see push_service.dart).
			ImageURL: n.Image,
		}
	}

	msg.Android = &messaging.AndroidConfig{
		Priority:    priority,
		TTL:         &ttl,
		CollapseKey: n.CollapseKey,
	}
	if trayRendered && n.Image != "" {
		msg.Android.Notification = &messaging.AndroidNotification{ImageURL: n.Image}
	}

	apsAlert := &messaging.ApsAlert{Title: n.Title, Body: n.Body}
	headers := map[string]string{
		"apns-priority":   "10",
		"apns-push-type":  "alert",
		"apns-expiration": expiry,
	}
	if n.CollapseKey != "" {
		// So a cancelled call REPLACES its own ring on the lock screen rather than stacking a second
		// notification underneath it.
		headers["apns-collapse-id"] = n.CollapseKey
	}

	// ThreadID is what makes iOS file ten messages from one chat as a single expandable
	// stack instead of ten banners that bury everything else on the lock screen. Empty for
	// a call, which must stand alone so it can be replaced and dismissed on its own.
	aps := &messaging.Aps{Alert: apsAlert, Sound: "default", ThreadID: n.ThreadID}
	// Lets the NotificationServiceExtension rewrite this alert before it is shown — which is how
	// an iPhone displays a message only it can decrypt. The alert above stays as written, so a
	// device with no extension, or one whose extension fails or runs out of time, still shows the
	// server's generic text rather than nothing.
	if n.ClientRendered {
		aps.MutableContent = true
	}
	if n.Urgent {
		// iOS 15+. Lets a call cut through a Focus mode, which is the whole point of a call.
		aps.CustomData = map[string]any{"interruption-level": "time-sensitive"}
	}

	msg.APNS = &messaging.APNSConfig{
		Headers: headers,
		Payload: &messaging.APNSPayload{Aps: aps},
	}
	return msg
}

var _ Sender = (*FCMSender)(nil)
