package push

import (
	"context"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// MultiSender routes each device to the appropriate underlying sender: devices
// with an FCM token go to the FCM sender; devices with a Web Push subscription
// go to the Web Push sender. A device matching neither is reported as skipped.
//
// Either sender may be nil, in which case its class of devices is skipped. This
// lets a deployment enable only the channels it has configured credentials for.
type MultiSender struct {
	FCM     Sender
	WebPush Sender
}

// NewMultiSender composes optional FCM and Web Push senders.
func NewMultiSender(fcm, webpush Sender) *MultiSender {
	return &MultiSender{FCM: fcm, WebPush: webpush}
}

// Send partitions devices by transport, dispatches a channel message to each
// sender, and merges the per-device results.
func (m *MultiSender) Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error) {
	return m.route(ctx, devices, func(s Sender, d []domain.Device) ([]Result, error) {
		return s.Send(ctx, msg, d)
	})
}

// SendChat does the same for a conversation notification.
func (m *MultiSender) SendChat(ctx context.Context, n ChatNotification, devices []domain.Device) ([]Result, error) {
	return m.route(ctx, devices, func(s Sender, d []domain.Device) ([]Result, error) {
		return s.SendChat(ctx, n, d)
	})
}

// route partitions devices by transport and hands each group to `deliver` on the
// matching sender, so both notification kinds share one routing path.
func (m *MultiSender) route(
	_ context.Context,
	devices []domain.Device,
	deliver func(Sender, []domain.Device) ([]Result, error),
) ([]Result, error) {
	var fcmDevices, webDevices, skipped []domain.Device
	for _, d := range devices {
		switch {
		case d.FCMToken != "":
			fcmDevices = append(fcmDevices, d)
		case d.WebPushSub != "":
			webDevices = append(webDevices, d)
		default:
			skipped = append(skipped, d)
		}
	}

	results := make([]Result, 0, len(devices))
	var firstErr error

	results = appendSkipped(results, skipped)
	results, firstErr = dispatch(m.FCM, fcmDevices, deliver, results, firstErr)
	results, firstErr = dispatch(m.WebPush, webDevices, deliver, results, firstErr)

	return results, firstErr
}

func dispatch(
	s Sender,
	devices []domain.Device,
	deliver func(Sender, []domain.Device) ([]Result, error),
	acc []Result,
	firstErr error,
) ([]Result, error) {
	if len(devices) == 0 {
		return acc, firstErr
	}
	if s == nil {
		return appendSkipped(acc, devices), firstErr
	}
	res, err := deliver(s, devices)
	acc = append(acc, res...)
	if err != nil && firstErr == nil {
		firstErr = err
	}
	return acc, firstErr
}

func appendSkipped(acc []Result, devices []domain.Device) []Result {
	for _, d := range devices {
		acc = append(acc, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
	}
	return acc
}

var _ Sender = (*MultiSender)(nil)
