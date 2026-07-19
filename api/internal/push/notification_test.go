package push

import (
	"context"
	"strings"
	"testing"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Building the payload that reaches a phone.
//
// The interesting part is the deep link. A notification a person taps has to open the thing it was
// about, and the only way it can is the data payload — so a missing channelId or messageId is a
// notification that opens the app on whatever screen it happened to be on, which reads as the
// notification being for nothing.

func TestAMessageNotificationCarriesEnoughToDeepLink(t *testing.T) {
	msg := domain.Message{
		ID: "msg-1", ChannelID: "chan-1", Title: "Deploy finished", Body: "v1.2.3 is live",
	}
	n := messageNotification("https://pheme.example", msg)

	if n.Title != "Deploy finished" || n.Body != "v1.2.3 is live" {
		t.Errorf("title/body = %q/%q", n.Title, n.Body)
	}
	if n.Data["channelId"] != "chan-1" || n.Data["messageId"] != "msg-1" {
		t.Errorf("data = %v; a tap cannot open the message it was about", n.Data)
	}
	if n.TTL <= 0 {
		t.Errorf("TTL = %d; a push with no lifetime may be discarded or kept forever depending on "+
			"the service", n.TTL)
	}
}

// The caller's own data survives alongside the routing keys — that is the point of letting a caller
// send data at all.
func TestAMessagesOwnDataIsPreserved(t *testing.T) {
	msg := domain.Message{
		ID: "msg-1", ChannelID: "chan-1",
		Data: map[string]string{"url": "https://example.test/build/42", "status": "green"},
	}
	n := messageNotification("", msg)

	if n.Data["url"] != "https://example.test/build/42" || n.Data["status"] != "green" {
		t.Errorf("the caller's data was lost: %v", n.Data)
	}
	if n.Data["channelId"] != "chan-1" || n.Data["messageId"] != "msg-1" {
		t.Errorf("the routing keys are missing: %v", n.Data)
	}
}

// A caller cannot overwrite the routing keys with their own data. If they could, a notification
// could be made to deep-link into somebody else's channel.
func TestACallerCannotOverwriteTheRoutingKeys(t *testing.T) {
	msg := domain.Message{
		ID: "real-message", ChannelID: "real-channel",
		Data: map[string]string{"channelId": "somebody-elses-channel", "messageId": "somebody-elses-message"},
	}
	n := messageNotification("", msg)

	if n.Data["channelId"] != "real-channel" {
		t.Errorf("channelId = %q; a caller redirected the deep link to another channel",
			n.Data["channelId"])
	}
	if n.Data["messageId"] != "real-message" {
		t.Errorf("messageId = %q; a caller redirected the deep link to another message",
			n.Data["messageId"])
	}
}

// Mutating the returned data must not reach back into the message it came from.
func TestNotificationDataIsACopy(t *testing.T) {
	original := map[string]string{"k": "v"}
	msg := domain.Message{ID: "m", ChannelID: "c", Data: original}

	n := messageNotification("", msg)
	n.Data["k"] = "changed"
	n.Data["added"] = "new"

	if original["k"] != "v" {
		t.Error("editing the notification data changed the message it was built from")
	}
	if _, ok := original["added"]; ok {
		t.Error("adding to the notification data added to the message")
	}
}

func TestTheImageURLIsAbsoluteOrAbsent(t *testing.T) {
	withImage := domain.Message{ID: "m", ChannelID: "c", Images: []domain.MessageImage{{ID: "img-1"}}}

	// No public base configured: no URL, rather than a relative one a phone cannot fetch.
	if got := imageURL("", withImage); got != "" {
		t.Errorf("imageURL with no base = %q, want empty — a phone cannot resolve a relative URL", got)
	}
	// No image: no URL.
	if got := imageURL("https://pheme.example", domain.Message{ID: "m"}); got != "" {
		t.Errorf("imageURL with no images = %q, want empty", got)
	}

	got := imageURL("https://pheme.example", withImage)
	if !strings.HasPrefix(got, "https://pheme.example/") || !strings.HasSuffix(got, "img-1") {
		t.Errorf("imageURL = %q, want an absolute URL ending in the image id", got)
	}
	// A trailing slash on the configured base must not produce a doubled one, which some services
	// reject and others fetch as a different path.
	if doubled := imageURL("https://pheme.example/", withImage); strings.Contains(doubled, "//v1") {
		t.Errorf("a trailing slash on the base produced %q", doubled)
	}
	if doubled := imageURL("https://pheme.example/", withImage); doubled != got {
		t.Errorf("base with and without a trailing slash disagree: %q vs %q", doubled, got)
	}
}

// The log sender is what an unconfigured deployment runs. It must report every device as SKIPPED —
// not sent — or the delivery records would claim notifications went out that never did.
func TestTheLogSenderReportsSkippedRatherThanSent(t *testing.T) {
	s := NewLogSender()
	devices := []domain.Device{{ID: "d1"}, {ID: "d2"}}

	results, err := s.Send(context.Background(), domain.Message{ID: "m", ChannelID: "c"}, devices)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(results) != len(devices) {
		t.Fatalf("got %d results for %d devices", len(results), len(devices))
	}
	for i, r := range results {
		if r.Status != domain.DeliverySkipped {
			t.Errorf("device %d reported %q; the delivery record would claim a notification was "+
				"sent that never left the process", i, r.Status)
		}
		if r.Gone {
			t.Errorf("device %d was reported as permanently dead by the no-op sender; it would be "+
				"deleted", i)
		}
	}

	chatResults, err := s.SendChat(context.Background(),
		ChatNotification{ConversationID: "conv", SenderName: "Alice"}, devices)
	if err != nil {
		t.Fatalf("SendChat: %v", err)
	}
	for i, r := range chatResults {
		if r.Status != domain.DeliverySkipped {
			t.Errorf("chat device %d reported %q, want skipped", i, r.Status)
		}
	}
}

// GoneDeviceIDs is the single definition of "this address is permanently dead", shared by the chat
// and channel paths. Both directions are expensive: pruning on a plain failure silently
// unsubscribes people over a bad minute; not pruning leaves dead addresses pushed to forever.
func TestGoneDeviceIDsSelectsOnlyPermanentFailures(t *testing.T) {
	got := GoneDeviceIDs([]Result{
		{DeviceID: "sent", Status: domain.DeliverySent},
		{DeviceID: "uninstalled", Status: domain.DeliveryFailed, Error: "UNREGISTERED", Gone: true},
		{DeviceID: "flaky", Status: domain.DeliveryFailed, Error: "timeout"},
		{DeviceID: "skipped", Status: domain.DeliverySkipped},
		{DeviceID: "dropped-subscription", Status: domain.DeliveryFailed, Error: "410", Gone: true},
		// Gone with no device id is unusable and must not produce an empty id to delete by.
		{DeviceID: "", Gone: true},
	})

	want := map[string]bool{"uninstalled": true, "dropped-subscription": true}
	if len(got) != len(want) {
		t.Fatalf("GoneDeviceIDs = %v, want exactly %v", got, want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("%q was selected for deletion; a transient failure must never cost a device "+
				"its registration", id)
		}
		if id == "" {
			t.Error("an empty device id was selected for deletion")
		}
	}

	if n := len(GoneDeviceIDs(nil)); n != 0 {
		t.Errorf("GoneDeviceIDs(nil) returned %d ids", n)
	}
}
