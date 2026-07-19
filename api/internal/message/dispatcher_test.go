package message

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/blob"
	"github.com/rh1tech/pheme/api/internal/domain"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/push"
	"github.com/rh1tech/pheme/api/internal/store"
)

// The channel broadcast pipeline: take a task off the queue, persist it, push it to every
// subscribed device, record what happened, and put it on the live stream. It had no tests.
//
// The ordering is the load-bearing part and it is not arbitrary. The message is persisted BEFORE
// anything is pushed, so a push service having a bad afternoon costs notifications and never
// history — the message is still in the channel when someone opens the app. And only a failure to
// persist returns an error, because returning one asks the broker to redeliver: a task retried
// after a successful write would create the message a second time, which is a duplicate post in
// everybody's feed rather than a duplicate notification.

// recordingStore wraps a real store and captures what the dispatcher asked of it.
type recordingStore struct {
	store.Store

	mu               sync.Mutex
	deliveries       []domain.Delivery
	deletedDevices   []string
	failCreateMsg    error
	failDevices      error
	failDelivery     error
	devicesForChanel []domain.Device
}

func (r *recordingStore) CreateMessage(ctx context.Context, m domain.Message) (domain.Message, error) {
	if r.failCreateMsg != nil {
		return domain.Message{}, r.failCreateMsg
	}
	return r.Store.CreateMessage(ctx, m)
}

func (r *recordingStore) ActiveDevicesForChannel(ctx context.Context, channelID string) ([]domain.Device, error) {
	if r.failDevices != nil {
		return nil, r.failDevices
	}
	return r.devicesForChanel, nil
}

func (r *recordingStore) CreateDelivery(ctx context.Context, d domain.Delivery) (domain.Delivery, error) {
	r.mu.Lock()
	r.deliveries = append(r.deliveries, d)
	r.mu.Unlock()
	if r.failDelivery != nil {
		return domain.Delivery{}, r.failDelivery
	}
	return r.Store.CreateDelivery(ctx, d)
}

func (r *recordingStore) DeleteDevice(ctx context.Context, deviceID string) error {
	r.mu.Lock()
	r.deletedDevices = append(r.deletedDevices, deviceID)
	r.mu.Unlock()
	return r.Store.DeleteDevice(ctx, deviceID)
}

func (r *recordingStore) recorded() ([]domain.Delivery, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]domain.Delivery(nil), r.deliveries...), append([]string(nil), r.deletedDevices...)
}

// scriptedPush returns whatever it is told to, and remembers what it was asked to send.
type scriptedPush struct {
	mu      sync.Mutex
	results []push.Result
	err     error
	calls   int
	sentTo  []domain.Device
	sentMsg domain.Message
}

func (s *scriptedPush) Send(_ context.Context, msg domain.Message, devices []domain.Device) ([]push.Result, error) {
	s.mu.Lock()
	s.calls++
	s.sentTo = devices
	s.sentMsg = msg
	s.mu.Unlock()
	return s.results, s.err
}

func (s *scriptedPush) SendChat(context.Context, push.ChatNotification, []domain.Device) ([]push.Result, error) {
	return nil, nil
}

func (s *scriptedPush) seen() (int, []domain.Device, domain.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.sentTo, s.sentMsg
}

type capturingBus struct {
	mu     sync.Mutex
	events []live.Event
}

func (c *capturingBus) Publish(e live.Event) {
	c.mu.Lock()
	c.events = append(c.events, e)
	c.mu.Unlock()
}

func (c *capturingBus) Subscribe(string) (<-chan live.Event, func()) {
	ch := make(chan live.Event)
	return ch, func() { close(ch) }
}

func (c *capturingBus) published() []live.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]live.Event(nil), c.events...)
}

type fixture struct {
	d     *Dispatcher
	store *recordingStore
	push  *scriptedPush
	bus   *capturingBus
	chID  string
}

func newFixture(t *testing.T, devices []domain.Device, results []push.Result) *fixture {
	t.Helper()
	db := store.NewMemory(blob.NewMemory())
	ch, err := db.CreateChannel(context.Background(), domain.Channel{
		PublicID: "pub", OwnerID: "owner", Name: "news",
		Status: domain.ChannelActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	rec := &recordingStore{Store: db, devicesForChanel: devices}
	pusher := &scriptedPush{results: results}
	bus := &capturingBus{}
	d := NewDispatcher(rec, pusher, bus, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &fixture{d: d, store: rec, push: pusher, bus: bus, chID: ch.ID}
}

func task(channelID string) domain.NotifyTask {
	return domain.NotifyTask{
		ChannelID: channelID, Title: "Deploy finished", Body: "v1.2.3 is live",
		CommentsAllowed: true, EnqueuedAt: time.Now().UTC(),
	}
}

func TestDispatchPersistsPushesRecordsAndPublishes(t *testing.T) {
	devices := []domain.Device{
		{ID: "d1", UserID: "u1", Platform: domain.PlatformWeb},
		{ID: "d2", UserID: "u2", Platform: domain.PlatformAndroid},
	}
	f := newFixture(t, devices, []push.Result{
		{DeviceID: "d1", Status: domain.DeliverySent},
		{DeviceID: "d2", Status: domain.DeliveryFailed, Error: "unavailable"},
	})

	if err := f.d.Handle(context.Background(), task(f.chID)); err != nil {
		t.Fatalf("handle: %v", err)
	}

	// Persisted, with the task's content intact.
	msgs, err := f.store.MessagesByChannel(context.Background(), f.chID, "", "", 10)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("stored %d messages, want 1", len(msgs))
	}
	if msgs[0].Title != "Deploy finished" || msgs[0].Body != "v1.2.3 is live" {
		t.Errorf("stored message = %+v", msgs[0])
	}
	if !msgs[0].CommentsAllowed {
		t.Error("commentsAllowed was lost between the task and the message")
	}

	// Pushed once, to every subscribed device, carrying the STORED message — the one with an id,
	// which is what a notification tap needs to open the right post.
	calls, sentTo, sentMsg := f.push.seen()
	if calls != 1 {
		t.Errorf("push called %d times, want 1", calls)
	}
	if len(sentTo) != 2 {
		t.Errorf("pushed to %d devices, want 2", len(sentTo))
	}
	if sentMsg.ID == "" || sentMsg.ID != msgs[0].ID {
		t.Errorf("pushed message id %q, want the stored one %q", sentMsg.ID, msgs[0].ID)
	}

	// One delivery row per device, carrying what actually happened. This is the only record of a
	// failed push there will ever be.
	deliveries, _ := f.store.recorded()
	if len(deliveries) != 2 {
		t.Fatalf("recorded %d deliveries, want one per device", len(deliveries))
	}
	byDevice := map[string]domain.Delivery{}
	for _, d := range deliveries {
		byDevice[d.DeviceID] = d
	}
	if got := byDevice["d1"]; got.Status != domain.DeliverySent || got.MessageID != msgs[0].ID {
		t.Errorf("d1 delivery = %+v", got)
	}
	if got := byDevice["d2"]; got.Status != domain.DeliveryFailed || got.Error != "unavailable" {
		t.Errorf("d2 delivery = %+v; the reason a push failed must survive", got)
	}

	// And on the live stream, so an open tab shows it without refetching.
	events := f.bus.published()
	if len(events) != 1 || events[0].ChannelID != f.chID || events[0].Message.ID != msgs[0].ID {
		t.Errorf("published %+v, want one event carrying the stored message", events)
	}
}

// THE ORDERING PROPERTY. A push that fails must not cost the message.
func TestAFailedPushStillLeavesTheMessageInTheChannel(t *testing.T) {
	f := newFixture(t, []domain.Device{{ID: "d1", UserID: "u1"}}, nil)
	f.push.err = errors.New("push provider is down")

	// And it must not ask the broker to retry: the message is already stored, so a redelivery
	// would post it to the channel a second time.
	if err := f.d.Handle(context.Background(), task(f.chID)); err != nil {
		t.Fatalf("a failed push returned %v; the task is retried and the message posted twice", err)
	}

	msgs, err := f.store.MessagesByChannel(context.Background(), f.chID, "", "", 10)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("stored %d messages after a failed push, want the message kept", len(msgs))
	}
	if events := f.bus.published(); len(events) != 1 {
		t.Errorf("published %d live events after a failed push; an open tab would never see the "+
			"message even though it was stored", len(events))
	}
}

// A failure to PERSIST is the one thing that must be retried: nothing was written, so redelivery
// costs nothing and dropping the task loses the notification entirely.
func TestAFailureToPersistIsRetried(t *testing.T) {
	f := newFixture(t, nil, nil)
	f.store.failCreateMsg = errors.New("mongo is unreachable")

	err := f.d.Handle(context.Background(), task(f.chID))
	if err == nil {
		t.Fatal("a task whose message could not be stored was acknowledged; the notification is " +
			"gone and nothing will retry it")
	}
	if calls, _, _ := f.push.seen(); calls != 0 {
		t.Errorf("push was attempted %d times for a message that was never stored", calls)
	}
	if events := f.bus.published(); len(events) != 0 {
		t.Errorf("a message that was never stored was published to the live stream: %+v", events)
	}
}

// Losing the device list is degraded, not fatal. The message is stored and goes out live; only the
// push is missed, and a retry would re-post the message.
func TestAFailureToLoadDevicesStillStoresAndPublishes(t *testing.T) {
	f := newFixture(t, nil, nil)
	f.store.failDevices = errors.New("device query failed")

	if err := f.d.Handle(context.Background(), task(f.chID)); err != nil {
		t.Fatalf("handle = %v, want the task acknowledged", err)
	}
	msgs, _ := f.store.MessagesByChannel(context.Background(), f.chID, "", "", 10)
	if len(msgs) != 1 {
		t.Errorf("stored %d messages, want the message kept", len(msgs))
	}
	if events := f.bus.published(); len(events) != 1 {
		t.Errorf("published %d live events, want the message on the stream", len(events))
	}
	if calls, _, _ := f.push.seen(); calls != 0 {
		t.Errorf("push was called %d times with no device list", calls)
	}
}

// A channel nobody subscribes to is not an error, and must not call push with an empty list.
func TestAChannelWithNoDevicesStillPublishes(t *testing.T) {
	f := newFixture(t, nil, nil)

	if err := f.d.Handle(context.Background(), task(f.chID)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if calls, _, _ := f.push.seen(); calls != 0 {
		t.Errorf("push called %d times for a channel with no subscribers", calls)
	}
	if events := f.bus.published(); len(events) != 1 {
		t.Errorf("published %d live events, want 1", len(events))
	}
}

// A delivery row that cannot be written must not abort the rest. The remaining devices' rows still
// matter, and the message has already gone out.
func TestAFailedDeliveryRecordDoesNotAbortTheRest(t *testing.T) {
	devices := []domain.Device{{ID: "d1"}, {ID: "d2"}, {ID: "d3"}}
	f := newFixture(t, devices, []push.Result{
		{DeviceID: "d1", Status: domain.DeliverySent},
		{DeviceID: "d2", Status: domain.DeliverySent},
		{DeviceID: "d3", Status: domain.DeliverySent},
	})
	f.store.failDelivery = errors.New("delivery write failed")

	if err := f.d.Handle(context.Background(), task(f.chID)); err != nil {
		t.Fatalf("handle = %v, want the task acknowledged", err)
	}
	deliveries, _ := f.store.recorded()
	if len(deliveries) != 3 {
		t.Errorf("attempted %d delivery records, want all 3 tried despite the failures", len(deliveries))
	}
}

// A nil bus is a supported configuration and must not panic.
func TestANilLiveBusIsFine(t *testing.T) {
	f := newFixture(t, nil, nil)
	f.d.Live = nil

	if err := f.d.Handle(context.Background(), task(f.chID)); err != nil {
		t.Fatalf("handle with no live bus: %v", err)
	}
}

// THE ONE THAT WAS MISSING. A push address the provider says is permanently dead must be removed.
//
// The chat path does this, and says why in a comment: a dead address stays in the collection and is
// pushed to on every message forever, which is how one account accumulated four Android rows for
// one phone, only the newest of which could receive anything. The channel broadcast path — the one
// with thousands of subscribers rather than a handful of conversation members — recorded the Gone
// flag into a delivery row and did nothing about it.
func TestPermanentlyDeadAddressesArePruned(t *testing.T) {
	devices := []domain.Device{{ID: "alive"}, {ID: "uninstalled"}, {ID: "flaky"}}
	f := newFixture(t, devices, []push.Result{
		{DeviceID: "alive", Status: domain.DeliverySent},
		{DeviceID: "uninstalled", Status: domain.DeliveryFailed, Error: "UNREGISTERED", Gone: true},
		// A plain failure: the network had a bad moment. This device must KEEP its registration.
		{DeviceID: "flaky", Status: domain.DeliveryFailed, Error: "timeout"},
	})

	if err := f.d.Handle(context.Background(), task(f.chID)); err != nil {
		t.Fatalf("handle: %v", err)
	}

	_, deleted := f.store.recorded()
	if len(deleted) != 1 || deleted[0] != "uninstalled" {
		t.Errorf("pruned %v, want only the permanently dead address; every future broadcast to this "+
			"channel retries a device that can never receive anything", deleted)
	}
}

// And a transient failure must never cost a device its registration — a bad minute on the network
// would otherwise silently unsubscribe people.
func TestATransientFailureKeepsTheDevice(t *testing.T) {
	f := newFixture(t, []domain.Device{{ID: "d1"}}, []push.Result{
		{DeviceID: "d1", Status: domain.DeliveryFailed, Error: "503 from the push service"},
	})

	if err := f.d.Handle(context.Background(), task(f.chID)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if _, deleted := f.store.recorded(); len(deleted) != 0 {
		t.Errorf("a transient push failure deleted %v; the person silently stops receiving the "+
			"channel", deleted)
	}
}
