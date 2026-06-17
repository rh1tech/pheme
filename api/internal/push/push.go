// Package push abstracts notification delivery to devices (FCM for mobile/web,
// Web Push for browsers). A logging no-op sender is provided for development.
package push

import (
	"context"
	"log/slog"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Result reports the outcome of a single device delivery attempt.
type Result struct {
	DeviceID string
	Status   domain.DeliveryStatus
	Error    string
}

// Sender delivers a message to a set of devices and reports per-device results.
type Sender interface {
	Send(ctx context.Context, msg domain.Message, devices []domain.Device) ([]Result, error)
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
