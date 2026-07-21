package message_test

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/federation"
	"github.com/rh1tech/pheme/api/internal/message"
	"github.com/rh1tech/pheme/api/internal/push"
	"github.com/rh1tech/pheme/api/internal/store"
)

// A whole federated channel, end to end, over real HTTP: a channel on host A, a
// user on host B subscribes across the network, A publishes, and the message
// lands on B's mirror. Nothing mocked but the two nodelists (each a fixed
// domain->key map) and the push sender.
//
// This is the integration the unit tests approach from both sides: A's
// RecordRemoteSubscriber and DeliverToPeer, B's DeliverRemoteMessage, and the
// dispatcher's fanOutToPeers, wired through the same signed transport a real
// deployment uses.
func TestFederatedChannelEndToEnd(t *testing.T) {
	aKey := hostKey(1)
	bKey := hostKey(2)
	// Each host trusts both, as they would from a shared nodelist.
	roster := fakeNodelist{
		"a.local": aKey.Public().(ed25519.PublicKey),
		"b.local": bKey.Public().(ed25519.PublicKey),
	}

	// --- Host A: has the channel ---
	aStore := store.NewMemory(nil)
	aDisp := message.NewDispatcher(aStore, push.NewLogSender(), nil, quietLogger())
	aChannels := &message.ChannelFederation{Store: aStore, Dispatcher: aDisp}
	aServer := httptest.NewServer(federationMux("a.local", roster, aChannels))
	defer aServer.Close()

	// --- Host B: the subscriber's host ---
	bStore := store.NewMemory(nil)
	bDisp := message.NewDispatcher(bStore, push.NewLogSender(), nil, quietLogger())
	bChannels := &message.ChannelFederation{Store: bStore, Dispatcher: bDisp}
	bServer := httptest.NewServer(federationMux("b.local", roster, bChannels))
	defer bServer.Close()

	// Clients resolve a peer domain to the httptest URL instead of https://.
	urls := map[string]string{"a.local": aServer.URL, "b.local": bServer.URL}
	aClient := clientTo("a.local", aKey, urls)
	bClient := clientTo("b.local", bKey, urls)

	// A's dispatcher fans a new message out to peers via A's client.
	aDisp.Peers = aClient

	ctx := context.Background()

	// A creates an open channel.
	ch, err := aStore.CreateChannel(ctx, domain.Channel{
		PublicID:         "ch_news",
		Name:             "A's Newsroom",
		OwnerID:          "a-owner",
		SubscriptionMode: domain.ModeOpen,
		Status:           domain.ChannelActive,
		CreatedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// B subscribes across the network: B tells A it has a subscriber, and mirrors
	// the channel locally with a subscribed device.
	name, err := bClient.SubscribeToRemoteChannel(ctx, "a.local", "ch_news")
	if err != nil {
		t.Fatalf("cross-host subscribe failed: %v", err)
	}
	if name != "A's Newsroom" {
		t.Errorf("channel name = %q, want the origin's", name)
	}
	mirror, err := bStore.CreateChannel(ctx, domain.Channel{
		PublicID:         "ch_localmirror",
		Name:             name,
		OwnerID:          "b-user",
		OriginDomain:     "a.local",
		OriginPublicID:   "ch_news",
		SubscriptionMode: domain.ModeOpen,
		Status:           domain.ChannelActive,
		CreatedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A subscribed device on B, so B's local fan-out has somewhere to deliver.
	dev, err := bStore.CreateDevice(ctx, domain.Device{UserID: "b-user", Platform: domain.PlatformWeb, WebPushSub: `{"endpoint":"https://push.example/x"}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bStore.Subscribe(ctx, domain.Subscription{ChannelID: mirror.ID, DeviceID: dev.ID, Status: domain.SubActive}); err != nil {
		t.Fatal(err)
	}

	// A publishes. The dispatcher persists, fans out locally (no local subs on A),
	// then federates to B.
	if err := aDisp.Handle(ctx, domain.NotifyTask{
		ChannelID: ch.ID,
		Title:     "Breaking",
		Body:      "It works across hosts",
	}); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	// The message must now exist on B's mirror.
	msgs, err := bStore.MessagesByChannel(ctx, mirror.ID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("B's mirror has %d messages, want 1 — the federated delivery did not land", len(msgs))
	}
	if msgs[0].Title != "Breaking" || msgs[0].Body != "It works across hosts" {
		t.Errorf("delivered message = %+v, want the published one", msgs[0])
	}
}

// A channel post's image crosses hosts: the origin carries the processed bytes
// inline (the subscriber cannot fetch them — they sit behind the origin's unlisted
// path prefix), and the subscriber re-hosts them in its own blob store so its
// members load them locally.
func TestFederatedChannelDeliversImages(t *testing.T) {
	aKey := hostKey(1)
	bKey := hostKey(2)
	roster := fakeNodelist{
		"a.local": aKey.Public().(ed25519.PublicKey),
		"b.local": bKey.Public().(ed25519.PublicKey),
	}

	aStore := store.NewMemory(nil)
	aBlob := blob.NewMemory()
	aDisp := message.NewDispatcher(aStore, push.NewLogSender(), nil, quietLogger())
	aDisp.Blobs = aBlob
	aChannels := &message.ChannelFederation{Store: aStore, Dispatcher: aDisp}
	aServer := httptest.NewServer(federationMux("a.local", roster, aChannels))
	defer aServer.Close()

	bStore := store.NewMemory(nil)
	bBlob := blob.NewMemory()
	bDisp := message.NewDispatcher(bStore, push.NewLogSender(), nil, quietLogger())
	bChannels := &message.ChannelFederation{Store: bStore, Dispatcher: bDisp, Blobs: bBlob}
	bServer := httptest.NewServer(federationMux("b.local", roster, bChannels))
	defer bServer.Close()

	urls := map[string]string{"a.local": aServer.URL, "b.local": bServer.URL}
	aDisp.Peers = clientTo("a.local", aKey, urls)
	bClient := clientTo("b.local", bKey, urls)

	ctx := context.Background()
	ch, err := aStore.CreateChannel(ctx, domain.Channel{
		PublicID: "ch_pics", Name: "Pics", OwnerID: "a-owner",
		SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bClient.SubscribeToRemoteChannel(ctx, "a.local", "ch_pics"); err != nil {
		t.Fatal(err)
	}
	mirror, err := bStore.CreateChannel(ctx, domain.Channel{
		PublicID: "ch_picsmirror", Name: "Pics", OwnerID: "b-user",
		OriginDomain: "a.local", OriginPublicID: "ch_pics",
		SubscriptionMode: domain.ModeOpen, Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// A stores a processed image, then publishes a post that references it.
	imgBytes := []byte("\xff\xd8processed-jpeg-bytes")
	imgID, err := aBlob.Put(ctx, imgBytes, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if err := aDisp.Handle(ctx, domain.NotifyTask{
		ChannelID: ch.ID, Title: "Look", Body: "a picture",
		Images: []domain.MessageImage{{ID: imgID, Width: 640, Height: 480}},
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// B's mirror holds the post with an image whose bytes B now serves itself.
	msgs, err := bStore.MessagesByChannel(ctx, mirror.ID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || len(msgs[0].Images) != 1 {
		t.Fatalf("B's mirror = %d messages / images %v, want 1 message with 1 image", len(msgs), msgs)
	}
	got := msgs[0].Images[0]
	if got.Width != 640 || got.Height != 480 {
		t.Errorf("image dims = %dx%d, want 640x480", got.Width, got.Height)
	}
	// The bytes are re-hosted on B under a fresh id, byte-for-byte the original.
	data, contentType, err := bBlob.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("B does not serve the image it was sent: %v", err)
	}
	if string(data) != string(imgBytes) || contentType != "image/jpeg" {
		t.Errorf("re-hosted image = %q (%s), want the origin's bytes", data, contentType)
	}
}

// --- helpers ---

func hostKey(seed byte) ed25519.PrivateKey {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = seed
	}
	return ed25519.NewKeyFromSeed(s)
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// fakeNodelist is a domain->key map satisfying the federation handler's lookup
// and the channel handler's IsPeer.
type fakeNodelist map[string]ed25519.PublicKey

func (f fakeNodelist) KeyFor(domain string) (ed25519.PublicKey, error) { return f[domain], nil }

func federationMux(origin string, roster fakeNodelist, ch *message.ChannelFederation) *http.ServeMux {
	mux := http.NewServeMux()
	h := federation.NewHandler(origin, roster, nil).WithChannels(ch)
	h.Register(mux)
	return mux
}

func clientTo(origin string, key ed25519.PrivateKey, urls map[string]string) *federation.Client {
	c := federation.NewClient(origin, "k1", key)
	c.PeerURL = func(d string) string { return urls[d] }
	return c
}
