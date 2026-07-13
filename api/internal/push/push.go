// Package push abstracts notification delivery to devices (FCM for mobile/web,
// Web Push for browsers). A logging no-op sender is provided for development.
package push

import (
	"context"
	"log/slog"
	"strings"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Result reports the outcome of a single device delivery attempt.
type Result struct {
	DeviceID string
	Status   domain.DeliveryStatus
	Error    string
}

// ChatNotification is the push for a conversation message.
//
// It deliberately has NO field for message content. Chat messages are end-to-end
// encrypted and the server holds only ciphertext, so a notification can say who
// sent a message but never what it said. Expressing that as a property of the type
// means no future change can leak plaintext into a lock screen by accident — there
// is nowhere to put it. The sender's name comes from conversation membership, not
// from the message.
type ChatNotification struct {
	ConversationID string
	MessageID      string
	SenderName     string
}

// Sender delivers notifications to a set of devices and reports per-device
// results. Channel messages are server-readable and carry their content; chat
// messages are encrypted and carry only who sent them.
type Sender interface {
	Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error)
	SendChat(ctx context.Context, n ChatNotification, devices []domain.Device) ([]Result, error)
}

// notification is the transport-neutral payload every Sender actually delivers.
// Both a channel message and a chat notification are reduced to one of these, so
// each transport has a single delivery path.
type notification struct {
	Title string
	Body  string
	Image string
	Data  map[string]string
}

func messageNotification(publicBaseURL string, msg domain.Message) notification {
	return notification{
		Title: msg.Title,
		Body:  msg.Body,
		Image: imageURL(publicBaseURL, msg),
		Data:  notificationData(msg),
	}
}

// chatBody is the only text a chat push ever carries. It is a constant: the server
// cannot read the message, and must not imply that it can.
const chatBody = "New message"

func chatNotificationPayload(n ChatNotification) notification {
	title := n.SenderName
	if title == "" {
		title = "Pheme"
	}
	return notification{
		Title: title,
		Body:  chatBody,
		// Enough to deep-link on tap; the client decrypts once it opens the chat.
		Data: map[string]string{"conversationId": n.ConversationID, "messageId": n.MessageID},
	}
}

// imageURL returns the absolute URL of a message's first image, or "" when the
// message has no images or no public base URL is configured.
func imageURL(publicBaseURL string, msg domain.Message) string {
	if publicBaseURL == "" || len(msg.Images) == 0 {
		return ""
	}
	return strings.TrimRight(publicBaseURL, "/") + "/v1/images/" + msg.Images[0].ID
}

// notificationData returns the data payload sent with a push notification: the
// message's user-supplied data plus channelId and messageId, so a notification
// tap can deep-link to the specific message.
func notificationData(msg domain.Message) map[string]string {
	data := make(map[string]string, len(msg.Data)+2)
	for k, v := range msg.Data {
		data[k] = v
	}
	data["channelId"] = msg.ChannelID
	data["messageId"] = msg.ID
	return data
}

// LogSender is a development Sender that logs instead of delivering. Every
// device is reported as skipped so the pipeline can be exercised without
// configured FCM/Web Push credentials.
type LogSender struct{}

// NewLogSender returns a no-op logging Sender.
func NewLogSender() *LogSender { return &LogSender{} }

// Send logs the message and marks each device as skipped.
func (s *LogSender) Send(_ context.Context, msg domain.Message, devices []domain.Device) ([]Result, error) {
	results := make([]Result, 0, len(devices))
	for _, d := range devices {
		slog.Info("push (dev no-op)",
			"channel", msg.ChannelID, "title", msg.Title, "device", d.ID, "platform", d.Platform)
		results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
	}
	return results, nil
}

// SendChat logs a chat notification. Only the conversation and sender are logged —
// there is no content to log, by design.
func (s *LogSender) SendChat(_ context.Context, n ChatNotification, devices []domain.Device) ([]Result, error) {
	results := make([]Result, 0, len(devices))
	for _, d := range devices {
		slog.Info("chat push (dev no-op)",
			"conversation", n.ConversationID, "sender", n.SenderName, "device", d.ID, "platform", d.Platform)
		results = append(results, Result{DeviceID: d.ID, Status: domain.DeliverySkipped})
	}
	return results, nil
}

var _ Sender = (*LogSender)(nil)
