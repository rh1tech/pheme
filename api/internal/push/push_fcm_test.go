package push

import (
	"testing"
	"time"
)

// A call has to wake a sleeping phone. On Android that means two things at once, and getting either
// one wrong means the phone does not ring — with nothing in any log to say why.
func TestBuildMessage_CallIsHighPriorityAndDataOnly(t *testing.T) {
	n := chatNotificationPayload(ChatNotification{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		SenderName:     "Ada",
		Kind:           KindCall,
		CallID:         "call-1",
	})

	msg := buildMessage("token", n)

	if msg.Android == nil {
		t.Fatal("no AndroidConfig: a default-priority push is held by Doze until the device wakes, which for a call is the same as never")
	}
	if msg.Android.Priority != "high" {
		t.Errorf("Android priority = %q, want high", msg.Android.Priority)
	}

	// The one that looks harmless and is not. A message carrying a notification payload is rendered by
	// the system tray while the app is backgrounded, and does NOT reliably start the Dart background
	// handler — which is the only thing that can raise the ringer. A call must be data-only.
	if msg.Notification != nil {
		t.Error("a call must not carry a notification payload: the system tray would render it and the background handler that raises the ringer would never start")
	}

	// The caller's name therefore has to travel in the data, because there is no title to read it from.
	if msg.Data["callerName"] != "Ada" {
		t.Errorf("Data[callerName] = %q, want Ada — a data-only push has no title, so a name left there reaches nobody", msg.Data["callerName"])
	}
	if msg.Data["callId"] != "call-1" {
		t.Errorf("Data[callId] = %q, want call-1", msg.Data["callId"])
	}

	// A call is worthless once it has stopped ringing.
	if msg.Android.TTL == nil || *msg.Android.TTL != 30*time.Second {
		t.Errorf("Android TTL = %v, want 30s", msg.Android.TTL)
	}
}

// An ordinary message is the opposite case in every respect, and must not be dragged along with the
// call settings: waking a dozing phone for a chat message is exactly the behaviour Doze exists to stop.
func TestBuildMessage_MessageIsNormalPriorityWithNotification(t *testing.T) {
	n := chatNotificationPayload(ChatNotification{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		SenderName:     "Ada",
		Kind:           KindMessage,
	})

	msg := buildMessage("token", n)

	if msg.Android.Priority != "normal" {
		t.Errorf("Android priority = %q, want normal", msg.Android.Priority)
	}
	if msg.Notification == nil {
		t.Fatal("a message should carry a notification payload so the tray renders it without waking the app")
	}
	if msg.Notification.Title != "Ada" {
		t.Errorf("title = %q, want Ada", msg.Notification.Title)
	}
	if msg.Android.TTL == nil || *msg.Android.TTL != defaultTTL*time.Second {
		t.Errorf("Android TTL = %v, want %ds", msg.Android.TTL, defaultTTL)
	}
}

// The ring and its cancellation must share a collapse key, or hanging up before an answer leaves a
// dead call sitting on the other person's lock screen looking live — and tapping it deep-links into a
// call nobody is on.
func TestBuildMessage_CallAndCancelShareACollapseKey(t *testing.T) {
	ring := chatNotificationPayload(ChatNotification{Kind: KindCall, CallID: "call-1"})
	cancel := chatNotificationPayload(ChatNotification{Kind: KindCallCancel, CallID: "call-1"})

	if ring.CollapseKey == "" {
		t.Fatal("a ring has no collapse key, so its cancellation cannot replace it")
	}
	if ring.CollapseKey != cancel.CollapseKey {
		t.Errorf("collapse keys differ: ring %q, cancel %q — the cancel would stack instead of replacing", ring.CollapseKey, cancel.CollapseKey)
	}

	msg := buildMessage("token", ring)
	if msg.APNS.Headers["apns-collapse-id"] != ring.CollapseKey {
		t.Errorf("apns-collapse-id = %q, want %q", msg.APNS.Headers["apns-collapse-id"], ring.CollapseKey)
	}
}

// The iOS fallback for a phone with no PushKit token: not a call screen, but at least a prompt banner
// that can cut through a Focus mode.
func TestBuildMessage_CallSetsTimeSensitiveAPNs(t *testing.T) {
	n := chatNotificationPayload(ChatNotification{Kind: KindCall, CallID: "call-1"})
	msg := buildMessage("token", n)

	if msg.APNS.Headers["apns-priority"] != "10" {
		t.Errorf("apns-priority = %q, want 10", msg.APNS.Headers["apns-priority"])
	}
	if got := msg.APNS.Payload.Aps.CustomData["interruption-level"]; got != "time-sensitive" {
		t.Errorf("interruption-level = %v, want time-sensitive", got)
	}
}

// A message must never carry a push the server cannot justify. The body is a constant, and the type
// makes it impossible to put content there — this pins that it stays that way.
func TestChatNotification_CarriesNoMessageContent(t *testing.T) {
	n := chatNotificationPayload(ChatNotification{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		SenderName:     "Ada",
	})

	if n.Body != chatBody {
		t.Errorf("body = %q, want the constant %q: the server holds only ciphertext and must not imply otherwise", n.Body, chatBody)
	}
	for k, v := range n.Data {
		if v == "secret" {
			t.Errorf("Data[%q] leaked content", k)
		}
	}
}
