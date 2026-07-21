package message

import (
	"context"
	"errors"
	"time"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/store"
)

// ChannelFederation implements federation.ChannelService: the origin side that
// records a peer's interest, and the subscriber side that delivers an arriving
// message. It sits between the federation handler and the store, and reuses the
// dispatcher's local fan-out so a mirrored channel's subscribers are served by
// the same code as a native one's.
type ChannelFederation struct {
	Store      store.Store
	Dispatcher *Dispatcher
	// Blobs re-hosts a delivered message's images on this host, so its local
	// subscribers load them from here — the origin serves them under a path prefix
	// this host does not know. Nil drops images (the text still arrives).
	Blobs blob.Store
}

var (
	// ErrNotFederatable is returned for a channel that exists but does not accept
	// remote subscribers — for now, anything not in open subscription mode.
	ErrNotFederatable = errors.New("channel does not accept remote subscribers")
	// ErrNoMirror is returned when a delivery arrives for a channel this host does
	// not mirror from that origin.
	ErrNoMirror = errors.New("no mirror channel for this origin")
)

// RecordRemoteSubscriber runs on the CHANNEL'S host. It validates that the
// channel exists and is federatable, records the peer, and returns the channel's
// display name for the peer's mirror.
func (c *ChannelFederation) RecordRemoteSubscriber(ctx context.Context, channelPublicID, peerDomain string) (string, error) {
	ch, err := c.Store.ChannelByPublicID(ctx, channelPublicID)
	if err != nil {
		return "", err
	}
	// Only open channels federate for now. An approval-mode channel would need
	// the approval queue to represent a remote host or user, which title-and-
	// body broadcast does not yet model — see docs/federation.md. A mirror is
	// never itself an origin, so it cannot be subscribed to across hosts either.
	if ch.SubscriptionMode != domain.ModeOpen || ch.IsMirror() {
		return "", ErrNotFederatable
	}
	if err := c.Store.AddRemoteSubscription(ctx, ch.ID, peerDomain); err != nil {
		return "", err
	}
	return ch.Name, nil
}

// DeliverRemoteMessage runs on the SUBSCRIBER'S host. It routes an arriving
// message to the local mirror channel — identified by (originDomain, publicID),
// so a peer cannot deliver into a channel it does not own — persists it, and
// fans it out to local subscribers via the dispatcher's local path.
func (c *ChannelFederation) DeliverRemoteMessage(ctx context.Context, originDomain string, m federation.RemoteMessage) error {
	ch, err := c.Store.ChannelByOriginPublicID(ctx, originDomain, m.ChannelPublicID)
	if err != nil {
		return ErrNoMirror
	}
	// Server-side time, not the origin's: a peer must not be able to backdate or
	// future-date a message on our timeline. The origin's send time is not
	// load-bearing for a broadcast, and trusting it would let a peer reorder our
	// channel view.
	created := time.Now().UTC()
	if !m.CreatedAt.IsZero() && m.CreatedAt.Before(created) {
		created = m.CreatedAt
	}
	msg, err := c.Store.CreateMessage(ctx, domain.Message{
		ChannelID: ch.ID,
		Title:     m.Title,
		Body:      m.Body,
		Images:    c.rehostImages(ctx, m.Images),
		CreatedAt: created,
	})
	if err != nil {
		return err
	}
	// Local fan-out only. A mirror has no remote subscribers of its own, so this
	// cannot loop.
	c.Dispatcher.DeliverLocally(ctx, msg)
	return nil
}

// rehostImages stores a delivered message's inline images in this host's own blob
// store and returns the local image references. Best-effort: without a blob store,
// or if one cannot be stored, that image is dropped and the message keeps the
// rest — the same tolerance the origin applies when it cannot read one.
func (c *ChannelFederation) rehostImages(ctx context.Context, images []federation.RemoteImage) []domain.MessageImage {
	if c.Blobs == nil || len(images) == 0 {
		return nil
	}
	out := make([]domain.MessageImage, 0, len(images))
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		id, err := c.Blobs.Put(ctx, img.Data, img.ContentType)
		if err != nil {
			continue
		}
		out = append(out, domain.MessageImage{ID: id, Width: img.Width, Height: img.Height})
	}
	return out
}
