// Package message wires the dispatch pipeline: persist an incoming task as a
// Message, fan it out to subscribed devices via push, record deliveries, and
// emit a live event.
package message

import (
	"context"
	"log/slog"
	"time"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/push"
	"github.com/rh1tech/pheme/api/internal/store"
)

// peerDeliverer delivers a channel message to a peer host. *federation.Client
// satisfies it; the interface keeps the dispatcher testable without a network.
type peerDeliverer interface {
	DeliverToPeer(ctx context.Context, peerDomain string, msg federation.RemoteMessage) error
}

// Dispatcher processes NotifyTasks pulled from the broker.
type Dispatcher struct {
	Store  store.Store
	Push   push.Sender
	Live   live.Bus
	Logger *slog.Logger
	// Peers is optional: when nil, the dispatcher does no cross-host fan-out and
	// behaves exactly as a non-federated deployment.
	Peers peerDeliverer
	// Blobs lets the fan-out carry a channel message's images to peer hosts by
	// value. Nil (or a load failure) simply delivers the text — a federated post
	// still arrives, without its pictures — rather than failing the delivery.
	Blobs blob.Store
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

	d.DeliverLocally(ctx, msg)
	d.fanOutToPeers(ctx, msg)
	return nil
}

// DeliverLocally pushes a persisted message to this host's own subscribed
// devices and emits the live event. It is the half of delivery that is the same
// whether a message originated here or arrived from a peer — the subscriber's
// host runs exactly this on a federated delivery, so a mirrored channel's
// subscribers are served by identical code to a native one's.
//
// It deliberately does NOT fan out to peers: a message arriving from a peer must
// not be re-federated, or a channel mirrored on two hosts would loop.
func (d *Dispatcher) DeliverLocally(ctx context.Context, msg domain.Message) {
	devices, err := d.Store.ActiveDevicesForChannel(ctx, msg.ChannelID)
	if err != nil {
		d.Logger.Error("load devices", "channel", msg.ChannelID, "error", err)
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
}

// fanOutToPeers delivers a message to every peer host with a subscriber to this
// channel. Fire-and-forget: a peer that is down or slow must not hold up a
// message that has already reached local subscribers, so failures are logged and
// not retried. A message that matters to a peer whose delivery failed is
// recoverable — the peer can pull history — where blocking on it is not.
//
// A no-op unless federation is wired in and the channel is native to this host
// (a mirror has no remote subscribers of its own, and must never re-federate).
func (d *Dispatcher) fanOutToPeers(ctx context.Context, msg domain.Message) {
	if d.Peers == nil {
		return
	}
	hosts, err := d.Store.RemoteSubscriberHosts(ctx, msg.ChannelID)
	if err != nil {
		d.Logger.Error("load remote subscribers", "channel", msg.ChannelID, "error", err)
		return
	}
	if len(hosts) == 0 {
		return
	}
	ch, err := d.Store.ChannelByID(ctx, msg.ChannelID)
	if err != nil || ch.IsMirror() {
		return // a mirror never re-federates
	}
	out := federation.RemoteMessage{
		ChannelPublicID: ch.PublicID,
		Title:           msg.Title,
		Body:            msg.Body,
		Images:          d.remoteImages(ctx, msg),
		CreatedAt:       msg.CreatedAt,
	}
	for _, host := range hosts {
		if err := d.Peers.DeliverToPeer(ctx, host, out); err != nil {
			// Logged, not retried: local delivery already happened, and the
			// peer can backfill from history.
			d.Logger.Warn("federated delivery failed", "channel", ch.PublicID, "peer", host, "error", err)
		}
	}
}

// remoteImages loads a message's processed image bytes for inline delivery to
// peers. Best-effort: without a blob store, or if an image cannot be read, that
// image is simply left out — a post arrives without a picture rather than not at
// all.
func (d *Dispatcher) remoteImages(ctx context.Context, msg domain.Message) []federation.RemoteImage {
	if d.Blobs == nil || len(msg.Images) == 0 {
		return nil
	}
	out := make([]federation.RemoteImage, 0, len(msg.Images))
	for _, img := range msg.Images {
		data, contentType, err := d.Blobs.Get(ctx, img.ID)
		if err != nil {
			d.Logger.Warn("federated image skipped", "channel", msg.ChannelID, "image", img.ID, "error", err)
			continue
		}
		out = append(out, federation.RemoteImage{
			Width: img.Width, Height: img.Height, ContentType: contentType, Data: data,
		})
	}
	return out
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
