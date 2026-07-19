package push

import (
	"context"
	"errors"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Which transport a device is reached on, and what happens when one of them is missing or broken.
//
// This is the layer where a notification silently goes nowhere. A device routed to a sender that is
// not configured, or dropped from the partition entirely, produces no error and no delivery — the
// server logs a successful send of nothing. That has already happened here once: a stack configured
// for both transports ran as web-push only, and Android received nothing for a day.

// recordingSender remembers what it was asked to deliver, and can be made to fail.
type recordingSender struct {
	name    string
	devices []domain.Device
	calls   int
	err     error
}

func (s *recordingSender) Send(_ context.Context, _ domain.Message, devices []domain.Device) ([]Result, error) {
	s.calls++
	s.devices = append(s.devices, devices...)
	if s.err != nil {
		return nil, s.err
	}
	return resultsFor(devices, domain.DeliverySent), nil
}

func (s *recordingSender) SendChat(_ context.Context, _ ChatNotification, devices []domain.Device) ([]Result, error) {
	s.calls++
	s.devices = append(s.devices, devices...)
	if s.err != nil {
		return nil, s.err
	}
	return resultsFor(devices, domain.DeliverySent), nil
}

type recordingVoIP struct {
	devices []domain.Device
	calls   int
}

func (s *recordingVoIP) SendCall(_ context.Context, _ ChatNotification, devices []domain.Device) ([]Result, error) {
	s.calls++
	s.devices = append(s.devices, devices...)
	return resultsFor(devices, domain.DeliverySent), nil
}

func resultsFor(devices []domain.Device, status domain.DeliveryStatus) []Result {
	out := make([]Result, 0, len(devices))
	for _, d := range devices {
		out = append(out, Result{DeviceID: d.ID, Status: status})
	}
	return out
}

func tokensSeen(devices []domain.Device) map[string]bool {
	out := map[string]bool{}
	for _, d := range devices {
		out[d.ID] = true
	}
	return out
}

func TestMultiSenderRoutesEachDeviceToItsTransport(t *testing.T) {
	fcm := &recordingSender{name: "fcm"}
	web := &recordingSender{name: "web"}
	m := NewMultiSender(fcm, web, nil)

	devices := []domain.Device{
		{ID: "android", Platform: domain.PlatformAndroid, FCMToken: "t"},
		{ID: "browser", Platform: domain.PlatformWeb, WebPushSub: "{}"},
		{ID: "iphone", Platform: domain.PlatformIOS, FCMToken: "t2"},
	}

	results, err := m.SendChat(context.Background(), ChatNotification{Kind: KindMessage}, devices)
	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}

	if !tokensSeen(fcm.devices)["android"] || !tokensSeen(fcm.devices)["iphone"] {
		t.Errorf("FCM was not given both token-bearing devices: %v", tokensSeen(fcm.devices))
	}
	if !tokensSeen(web.devices)["browser"] {
		t.Error("web push was not given the browser")
	}
	if tokensSeen(fcm.devices)["browser"] {
		t.Error("the browser was sent to FCM, which cannot reach it")
	}
	// Every device is accounted for in the results, or a caller cannot tell what happened.
	if len(results) != len(devices) {
		t.Errorf("got %d results for %d devices", len(results), len(devices))
	}
}

// A device with no address at all is SKIPPED explicitly rather than dropped. A dropped device
// produces no result, and a caller counting results would believe it was delivered to.
func TestMultiSenderReportsAddresslessDevicesAsSkipped(t *testing.T) {
	fcm := &recordingSender{}
	m := NewMultiSender(fcm, nil, nil)

	devices := []domain.Device{
		{ID: "reachable", FCMToken: "t"},
		{ID: "no-address", Platform: domain.PlatformIOS}, // registered for a call lock, no push
	}
	results, err := m.SendChat(context.Background(), ChatNotification{Kind: KindMessage}, devices)
	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}

	var skipped bool
	for _, r := range results {
		if r.DeviceID == "no-address" && r.Status == domain.DeliverySkipped {
			skipped = true
		}
	}
	if !skipped {
		t.Errorf("a device with no push address was not reported as skipped: %+v", results)
	}
}

// A transport that is not configured must not silently swallow its devices. This is the shape of
// the outage that already happened: the stack thought it had FCM and did not.
func TestMultiSenderSkipsDevicesWhoseTransportIsAbsent(t *testing.T) {
	web := &recordingSender{}
	m := NewMultiSender(nil, web, nil) // no FCM configured

	devices := []domain.Device{
		{ID: "android", FCMToken: "t"},
		{ID: "browser", WebPushSub: "{}"},
	}
	results, err := m.SendChat(context.Background(), ChatNotification{Kind: KindMessage}, devices)
	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}

	var androidResult *Result
	for i := range results {
		if results[i].DeviceID == "android" {
			androidResult = &results[i]
		}
	}
	if androidResult == nil {
		t.Fatal("a device whose transport is not configured vanished from the results entirely; " +
			"the server would report a successful send of nothing")
	}
	if androidResult.Status == domain.DeliverySent {
		t.Error("a device was reported as SENT through a transport that does not exist")
	}
	if !tokensSeen(web.devices)["browser"] {
		t.Error("the configured transport stopped working because another was missing")
	}
}

// A CALL to an iPhone with a PushKit token goes to APNs directly: FCM physically cannot deliver it
// — wrong token, wrong topic, wrong push type — so a call routed there rings nobody.
func TestMultiSenderPeelsOffVoIPCallsForIPhones(t *testing.T) {
	fcm := &recordingSender{}
	voip := &recordingVoIP{}
	m := NewMultiSender(fcm, nil, voip)

	devices := []domain.Device{
		{ID: "iphone", Platform: domain.PlatformIOS, FCMToken: "t", VoIPToken: "voip-token"},
		{ID: "android", Platform: domain.PlatformAndroid, FCMToken: "t2"},
	}
	if _, err := m.SendChat(context.Background(), ChatNotification{Kind: KindCall}, devices); err != nil {
		t.Fatalf("SendChat: %v", err)
	}

	if !tokensSeen(voip.devices)["iphone"] {
		t.Error("a call to an iPhone with a PushKit token did not go to APNs; it would ring nobody")
	}
	if tokensSeen(fcm.devices)["iphone"] {
		t.Error("the iPhone was ALSO sent through FCM, which would ring it twice or not at all")
	}
	// Everything else carries on through the ordinary path.
	if !tokensSeen(fcm.devices)["android"] {
		t.Error("peeling off the iPhone stopped Android being rung")
	}
}

// An iPhone with no PushKit token is an ordinary device: it has not registered for calls, and
// pretending otherwise sends its call into a void.
func TestMultiSenderLeavesIPhonesWithoutVoIPTokensOnTheOrdinaryPath(t *testing.T) {
	fcm := &recordingSender{}
	voip := &recordingVoIP{}
	m := NewMultiSender(fcm, nil, voip)

	devices := []domain.Device{{ID: "iphone", Platform: domain.PlatformIOS, FCMToken: "t"}}
	if _, err := m.SendChat(context.Background(), ChatNotification{Kind: KindCall}, devices); err != nil {
		t.Fatalf("SendChat: %v", err)
	}

	if voip.calls != 0 {
		t.Error("an iPhone with no PushKit token was sent to APNs VoIP")
	}
	if !tokensSeen(fcm.devices)["iphone"] {
		t.Error("an iPhone with no PushKit token was not reached at all")
	}
}

// A MESSAGE never goes down the VoIP path, whatever tokens the device has. Ringing a phone for a
// text message is not a notification, it is an alarm.
func TestMultiSenderNeverSendsAMessageOverVoIP(t *testing.T) {
	fcm := &recordingSender{}
	voip := &recordingVoIP{}
	m := NewMultiSender(fcm, nil, voip)

	devices := []domain.Device{
		{ID: "iphone", Platform: domain.PlatformIOS, FCMToken: "t", VoIPToken: "voip-token"},
	}
	if _, err := m.SendChat(context.Background(), ChatNotification{Kind: KindMessage}, devices); err != nil {
		t.Fatalf("SendChat: %v", err)
	}

	if voip.calls != 0 {
		t.Error("a message was delivered as a ringing call")
	}
}

// One transport failing must not stop the others. A web-push outage that also silenced Android
// would turn a partial failure into a total one.
func TestMultiSenderKeepsGoingWhenOneTransportFails(t *testing.T) {
	fcm := &recordingSender{err: errors.New("fcm is down")}
	web := &recordingSender{}
	m := NewMultiSender(fcm, web, nil)

	devices := []domain.Device{
		{ID: "android", FCMToken: "t"},
		{ID: "browser", WebPushSub: "{}"},
	}
	_, err := m.SendChat(context.Background(), ChatNotification{Kind: KindMessage}, devices)

	// The error is reported — a caller must be able to tell something went wrong...
	if err == nil {
		t.Error("a failing transport was reported as success")
	}
	// ...but the healthy transport still delivered.
	if !tokensSeen(web.devices)["browser"] {
		t.Error("one transport failing stopped another from delivering")
	}
}

// No devices is not an error, and must not call anything. Sending an empty batch to a push provider
// wastes a round trip on every message in a conversation nobody has a device for.
func TestMultiSenderDoesNothingForNoDevices(t *testing.T) {
	fcm := &recordingSender{}
	web := &recordingSender{}
	m := NewMultiSender(fcm, web, nil)

	results, err := m.SendChat(context.Background(), ChatNotification{Kind: KindMessage}, nil)
	if err != nil {
		t.Fatalf("SendChat with no devices: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results for no devices", len(results))
	}
	if fcm.calls != 0 || web.calls != 0 {
		t.Errorf("a sender was called with nothing to send (fcm=%d web=%d)", fcm.calls, web.calls)
	}
}

// The channel-message path partitions the same way. It is a separate method and could drift.
func TestMultiSenderRoutesChannelMessagesToo(t *testing.T) {
	fcm := &recordingSender{}
	web := &recordingSender{}
	m := NewMultiSender(fcm, web, nil)

	devices := []domain.Device{
		{ID: "android", FCMToken: "t"},
		{ID: "browser", WebPushSub: "{}"},
	}
	if _, err := m.Send(context.Background(), domain.Message{Title: "hi"}, devices); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !tokensSeen(fcm.devices)["android"] || !tokensSeen(web.devices)["browser"] {
		t.Errorf("channel messages are not partitioned like chat ones: fcm=%v web=%v",
			tokensSeen(fcm.devices), tokensSeen(web.devices))
	}
}
