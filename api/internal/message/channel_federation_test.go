package message

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/push"
	"github.com/rh1tech/pheme/api/internal/store"
)

func newService(t *testing.T) (*ChannelFederation, store.Store) {
	t.Helper()
	db := store.NewMemory(nil)
	d := NewDispatcher(db, push.NewLogSender(), nil, slog.New(slog.NewTextHandler(io_discard{}, nil)))
	return &ChannelFederation{Store: db, Dispatcher: d}, db
}

// io_discard avoids pulling io just for a discard writer.
type io_discard struct{}

func (io_discard) Write(p []byte) (int, error) { return len(p), nil }

func openChannel(t *testing.T, db store.Store, publicID, name string) domain.Channel {
	t.Helper()
	ch, err := db.CreateChannel(context.Background(), domain.Channel{
		PublicID:         publicID,
		Name:             name,
		OwnerID:          "owner",
		SubscriptionMode: domain.ModeOpen,
		Status:           domain.ChannelActive,
		CreatedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func TestRecordRemoteSubscriberTracksThePeerHost(t *testing.T) {
	svc, db := newService(t)
	openChannel(t, db, "ch_news", "The News")

	name, err := svc.RecordRemoteSubscriber(context.Background(), "ch_news", "b.example")
	if err != nil {
		t.Fatal(err)
	}
	if name != "The News" {
		t.Errorf("name = %q, want the channel's display name for the mirror", name)
	}
	hosts, _ := db.RemoteSubscriberHosts(context.Background(), channelID(t, db, "ch_news"))
	if len(hosts) != 1 || hosts[0] != "b.example" {
		t.Errorf("hosts = %v, want [b.example]", hosts)
	}
}

// A second subscriber from the same host does not duplicate the host: the origin
// tracks hosts, not their users.
func TestRecordRemoteSubscriberIsIdempotentPerHost(t *testing.T) {
	svc, db := newService(t)
	openChannel(t, db, "ch_news", "The News")
	ctx := context.Background()

	_, _ = svc.RecordRemoteSubscriber(ctx, "ch_news", "b.example")
	_, _ = svc.RecordRemoteSubscriber(ctx, "ch_news", "b.example")

	hosts, _ := db.RemoteSubscriberHosts(ctx, channelID(t, db, "ch_news"))
	if len(hosts) != 1 {
		t.Errorf("host recorded %d times, want once", len(hosts))
	}
}

// Only open channels federate; an approval-mode channel is refused, since the
// approval queue does not yet model a remote subscriber.
func TestApprovalChannelRefusesRemoteSubscribers(t *testing.T) {
	svc, db := newService(t)
	_, err := db.CreateChannel(context.Background(), domain.Channel{
		PublicID:         "ch_private",
		Name:             "Private",
		OwnerID:          "owner",
		SubscriptionMode: domain.ModeApproval,
		Status:           domain.ChannelActive,
		CreatedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordRemoteSubscriber(context.Background(), "ch_private", "b.example"); err != ErrNotFederatable {
		t.Errorf("err = %v, want ErrNotFederatable", err)
	}
}

func TestUnknownChannelIsRefused(t *testing.T) {
	svc, _ := newService(t)
	if _, err := svc.RecordRemoteSubscriber(context.Background(), "ch_nope", "b.example"); err == nil {
		t.Error("a subscribe to a nonexistent channel succeeded")
	}
}

// Delivery routes to the local MIRROR by (origin, publicId) and persists a
// message on it. A delivery for a channel this host does not mirror is refused —
// so a peer cannot inject messages into a channel it does not own.
func TestDeliverRemoteMessageRoutesToTheMirror(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	// B mirrors a.example's ch_news.
	mirror, err := db.CreateChannel(ctx, domain.Channel{
		PublicID:         "ch_localmirror",
		Name:             "The News",
		OwnerID:          "local-subscriber",
		OriginDomain:     "a.example",
		OriginPublicID:   "ch_news",
		SubscriptionMode: domain.ModeOpen,
		Status:           domain.ChannelActive,
		CreatedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = svc.DeliverRemoteMessage(ctx, "a.example", federation.RemoteMessage{
		ChannelPublicID: "ch_news",
		Title:           "Breaking",
		Body:            "Something happened",
		CreatedAt:       time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("delivery failed: %v", err)
	}

	msgs, err := db.MessagesByChannel(ctx, mirror.ID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Title != "Breaking" {
		t.Errorf("mirror channel messages = %+v, want the delivered one", msgs)
	}
}

// A delivery for a channel/origin pair this host does not mirror is refused — a
// peer must not be able to deliver into a channel it does not own, nor into one
// mirrored from a different origin.
func TestDeliveryToAWrongOriginIsRefused(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	_, _ = db.CreateChannel(ctx, domain.Channel{
		PublicID:       "ch_localmirror2",
		Name:           "The News",
		OriginDomain:   "a.example",
		OriginPublicID: "ch_news",
		Status:         domain.ChannelActive,
		CreatedAt:      time.Now().UTC(),
	})
	// c.example, not the origin, tries to deliver into a.example's mirror.
	err := svc.DeliverRemoteMessage(ctx, "c.example", federation.RemoteMessage{
		ChannelPublicID: "ch_news",
		Title:           "Spoofed",
	})
	if err != ErrNoMirror {
		t.Errorf("err = %v, want ErrNoMirror — a non-origin delivered into a mirror", err)
	}
}

func channelID(t *testing.T, db store.Store, publicID string) string {
	t.Helper()
	ch, err := db.ChannelByPublicID(context.Background(), publicID)
	if err != nil {
		t.Fatal(err)
	}
	return ch.ID
}
