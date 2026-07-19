// Package message wires the dispatch pipeline: persist an incoming task as a
// Message, fan it out to subscribed devices via push, record deliveries, and
// emit a live event.
package message

import (
	"context"
	"log/slog"
	"time"

	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/push"
	"github.com/rh1tech/pheme/api/internal/store"
)

// Dispatcher processes NotifyTasks pulled from the broker.
type Dispatcher struct {
	Store  store.Store
	Push   push.Sender
	Live   live.Bus
	Logger *slog.Logger
}

// NewDispatcher constructs a Dispatcher.
func NewDispatcher(s store.Store, p push.Sender, b live.Bus, l *slog.Logger) *Dispatcher {
	if l == nil {
		l = slog.Default()
	}
	return &Dispatcher{Store: s, Push: p, Live: b, Logger: l}
}

// Handle implements broker.Handler. The message is persisted before push is
// attempted, so history is durable even when delivery fails. Returning an error
// asks the broker to retry (and ultimately dead-letter) the task.
func (d *Dispatcher) Handle(ctx context.Context, task domain.NotifyTask) error {
	msg, err := d.Store.CreateMessage(ctx, domain.Message{
		ChannelID:       task.ChannelID,
		Title:           task.Title,
		Body:            task.Body,
		Images:          task.Images,
		Data:            task.Data,
		CommentsAllowed: task.CommentsAllowed,
		CreatedAt:       time.Now().UTC(),
	})
	if err != nil {
		return err // persistence failed: retry, do not ack
	}

	devices, err := d.Store.ActiveDevicesForChannel(ctx, task.ChannelID)
	if err != nil {
		d.Logger.Error("load devices", "channel", task.ChannelID, "error", err)
		devices = nil
	}

	if len(devices) > 0 {
		results, err := d.Push.Send(ctx, msg, devices)
		if err != nil {
			d.Logger.Error("push send", "message", msg.ID, "error", err)
		}
		for _, r := range results {
			if _, derr := d.Store.CreateDelivery(ctx, domain.Delivery{
				MessageID: msg.ID,
				DeviceID:  r.DeviceID,
				Status:    r.Status,
				Error:     r.Error,
				SentAt:    time.Now().UTC(),
			}); derr != nil {
				d.Logger.Error("record delivery", "message", msg.ID, "device", r.DeviceID, "error", derr)
			}
		}
		d.pruneDeadAddresses(ctx, results)
	}

	if d.Live != nil {
		d.Live.Publish(live.Event{ChannelID: msg.ChannelID, Message: msg})
	}
	return nil
}

// pruneDeadAddresses removes push addresses the provider has declared permanently dead.
//
// The Gone flag was recorded into the delivery row and otherwise ignored on this path, so every
// broadcast to a channel retried devices that could never receive anything again — the app
// uninstalled, the token rotated, the browser subscription dropped. On a channel with thousands of
// subscribers that is a growing tax on every single post, paid one outbound HTTPS request at a
// time. The chat path already did this; this one did not.
//
// Only on a definitive GONE, never on a plain failure: a device must not lose its registration
// because the network had a bad minute.
func (d *Dispatcher) pruneDeadAddresses(ctx context.Context, results []push.Result) {
	for _, id := range push.GoneDeviceIDs(results) {
		if err := d.Store.DeleteDevice(ctx, id); err != nil {
			d.Logger.Error("prune dead device", "device", id, "error", err)
			continue
		}
		d.Logger.Info("pruned a dead push address", "device", id)
	}
}

// Verify Handle satisfies the broker.Handler signature.
var _ broker.Handler = (*Dispatcher)(nil).Handle
