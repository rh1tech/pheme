package push

import (
	"context"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// MultiSender routes each device to the appropriate underlying sender: devices
// with an FCM token go to the FCM sender; devices with a Web Push subscription
// go to the Web Push sender. A device matching neither is reported as skipped.
//
// Any sender may be nil, in which case its class of devices is skipped. This
// lets a deployment enable only the channels it has configured credentials for.
type MultiSender struct {
	FCM     Sender
	WebPush Sender
	// VoIP rings an iPhone that is asleep. It is not a Sender: it only ever carries calls, and it
	// cannot carry anything else — a VoIP push that does not result in a call being reported to
	// CallKit gets the app killed. So it has its own narrow interface, and the routing below is what
	// guarantees nothing but a call ever reaches it.
	VoIP VoIPSender
}

// VoIPSender delivers a ringing call to an iOS device via PushKit.
type VoIPSender interface {
	SendCall(ctx context.Context, n ChatNotification, devices []domain.Device) ([]Result, error)
}

// NewMultiSender composes optional FCM, Web Push and APNs-VoIP senders.
func NewMultiSender(fcm, webpush Sender, voip VoIPSender) *MultiSender {
	return &MultiSender{FCM: fcm, WebPush: webpush, VoIP: voip}
}

// Send partitions devices by transport, dispatches a channel message to each
// sender, and merges the per-device results.
func (m *MultiSender) Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error) {
	return m.route(ctx, devices, func(s Sender, d []domain.Device) ([]Result, error) {
		return s.Send(ctx, msg, d)
	})
}

// SendChat does the same for a conversation notification — except that a CALL to an iPhone that has a
// PushKit token is peeled off first and sent straight to Apple.
//
// It has to be peeled off, because FCM physically cannot deliver it: wrong token, wrong topic, wrong
// push type (see APNsVoIPSender). Everything else — Android, web, and iPhones with no PushKit token —
// carries on through the ordinary path.
func (m *MultiSender) SendChat(ctx context.Context, n ChatNotification, devices []domain.Device) ([]Result, error) {
	if n.Kind == KindMessage || m.VoIP == nil {
		return m.route(ctx, devices, func(s Sender, d []domain.Device) ([]Result, error) {
			return s.SendChat(ctx, n, d)
		})
	}

	var voipDevices, rest []domain.Device
	for _, d := range devices {
		if d.Platform == domain.PlatformIOS && d.VoIPToken != "" {
			voipDevices = append(voipDevices, d)
			continue
		}
		rest = append(rest, d)
	}

	results := make([]Result, 0, len(devices))
	var firstErr error

	if len(voipDevices) > 0 {
		res, err := m.VoIP.SendCall(ctx, n, voipDevices)
		results = append(results, res...)
		if err != nil {
			firstErr = err
		}
	}

	if len(rest) > 0 {
		res, err := m.route(ctx, rest, func(s Sender, d []domain.Device) ([]Result, error) {
			return s.SendChat(ctx, n, d)
		})
		results = append(results, res...)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return results, firstErr
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
