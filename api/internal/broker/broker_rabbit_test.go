package broker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/rh1tech/pheme/api/internal/domain"
)

// The broker that actually ships, against a real RabbitMQ.
//
// The in-memory driver is a channel and cannot express what matters here: a publish that is
// CONFIRMED by the broker rather than merely written to a socket, and a failed handler that sends
// the message to a dead-letter queue instead of dropping it or spinning on it forever. Neither
// property can be tested against a Go channel, and both are the reason the Rabbit driver exists.
//
// Skipped unless PHEME_TEST_RABBIT_URI is set:
//
//	docker run -d --rm -p 5772:5672 rabbitmq:3-alpine
//	PHEME_TEST_RABBIT_URI=amqp://guest:guest@localhost:5772/ go test ./internal/broker/

func rabbitOrSkip(t *testing.T) *Rabbit {
	t.Helper()
	uri := os.Getenv("PHEME_TEST_RABBIT_URI")
	if uri == "" {
		t.Skip("PHEME_TEST_RABBIT_URI not set — skipping the broker that runs in production")
	}
	// A queue per test: these declare durable queues, and sharing one would have tests consuming
	// each other's work.
	queue := fmt.Sprintf("phemetest.%s", t.Name())
	r, err := NewRabbit(uri, queue)
	if err != nil {
		t.Fatalf("connect to rabbitmq: %v", err)
	}
	// Start empty. Queues here are durable, and a previous run that died before its cleanup — an
	// AMQP channel closes on a failed passive declare, taking the cleanup's deletes with it —
	// leaves messages behind that would be counted as this run's.
	if _, err := r.channel.QueuePurge(queue, false); err != nil {
		t.Fatalf("purge %s: %v", queue, err)
	}
	if _, err := r.channel.QueuePurge(queue+dlqSuffix, false); err != nil {
		t.Fatalf("purge %s: %v", queue+dlqSuffix, err)
	}
	t.Cleanup(func() {
		// Tear the queues down, or a re-run inherits the previous run's messages.
		_, _ = r.channel.QueueDelete(queue, false, false, false)
		_, _ = r.channel.QueueDelete(queue+dlqSuffix, false, false, false)
		_ = r.channel.ExchangeDelete(queue+dlxSuffix, false, false)
		_ = r.Close(context.Background())
	})
	return r
}

func TestRabbitPublishIsConfirmedAndDelivered(t *testing.T) {
	r := rabbitOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Publish returns only once the BROKER has confirmed it. Without that a notification could be
	// lost between the request returning 201 and anything durable happening.
	if err := r.Publish(ctx, domain.NotifyTask{Title: "hello", Body: "world"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	got := make(chan domain.NotifyTask, 1)
	consumeCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		_ = r.Consume(consumeCtx, func(_ context.Context, task domain.NotifyTask) error {
			got <- task
			return nil
		})
	}()

	select {
	case task := <-got:
		if task.Title != "hello" || task.Body != "world" {
			t.Errorf("task came back as %+v", task)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a confirmed publish was never delivered")
	}
}

// A handler that fails must send the message to the DEAD-LETTER queue: not dropped, so it can be
// inspected, and not requeued, so one bad task cannot spin forever and starve the rest.
func TestRabbitFailedHandlerDeadLettersRatherThanLooping(t *testing.T) {
	r := rabbitOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	if err := r.Publish(ctx, domain.NotifyTask{Title: "poison"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	attempts := make(chan struct{}, 8)
	consumeCtx, stop := context.WithCancel(ctx)
	go func() {
		_ = r.Consume(consumeCtx, func(context.Context, domain.NotifyTask) error {
			attempts <- struct{}{}
			return errors.New("handler failed")
		})
	}()

	// It is handled once...
	select {
	case <-attempts:
	case <-time.After(15 * time.Second):
		stop()
		t.Fatal("the task was never handled")
	}
	// ...and NOT handed back. A requeue-on-failure would deliver it again immediately, forever.
	select {
	case <-attempts:
		stop()
		t.Fatal("a failed task was redelivered; one bad message would spin forever and starve the queue")
	case <-time.After(2 * time.Second):
	}
	stop()

	// And it is waiting in the dead-letter queue rather than gone.
	//
	// Inspected by its exact name: a passive declare of a queue that does not exist closes the
	// AMQP channel, so a "try this name, then that one" fallback cannot work — the second attempt
	// runs on a dead channel and reports the wrong reason.
	q, err := r.channel.QueueInspect(fmt.Sprintf("phemetest.%s%s", t.Name(), dlqSuffix))
	if err != nil {
		t.Fatalf("inspect dead-letter queue: %v", err)
	}
	if q.Messages != 1 {
		t.Errorf("the dead-letter queue holds %d messages, want the failed one", q.Messages)
	}
}

// A malformed body goes straight to the dead-letter queue too. It can never succeed, so retrying it
// is the same spin, and dropping it silently loses the evidence.
func TestRabbitMalformedMessageIsDeadLettered(t *testing.T) {
	r := rabbitOrSkip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	queue := fmt.Sprintf("phemetest.%s", t.Name())
	if err := r.channel.PublishWithContext(ctx, "", queue, false, false, amqp.Publishing{
		ContentType: "application/json",
		Body:        []byte("this is not json"),
	}); err != nil {
		t.Fatalf("publish raw: %v", err)
	}

	handled := make(chan struct{}, 4)
	consumeCtx, stop := context.WithCancel(ctx)
	go func() {
		_ = r.Consume(consumeCtx, func(context.Context, domain.NotifyTask) error {
			handled <- struct{}{}
			return nil
		})
	}()

	// The handler must never see it.
	select {
	case <-handled:
		stop()
		t.Fatal("a malformed message reached the handler")
	case <-time.After(3 * time.Second):
	}
	stop()

	q, err := r.channel.QueueInspect(queue + dlqSuffix)
	if err != nil {
		t.Fatalf("inspect dead-letter queue: %v", err)
	}
	if q.Messages != 1 {
		t.Errorf("the dead-letter queue holds %d messages, want the malformed one", q.Messages)
	}
}

// Cancelling must end Consume, or shutdown hangs on a goroutine waiting for work.
func TestRabbitConsumeStopsWhenCancelled(t *testing.T) {
	r := rabbitOrSkip(t)
	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan error, 1)
	go func() { stopped <- r.Consume(ctx, func(context.Context, domain.NotifyTask) error { return nil }) }()

	time.Sleep(500 * time.Millisecond) // let the consumer register
	cancel()

	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Consume returned %v, want context.Canceled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Consume ignored cancellation; shutdown would hang")
	}
}

// Declaring is idempotent: the server restarts, reconnects, and must not fail because its queues
// already exist.
func TestRabbitCanBeOpenedTwiceOnTheSameQueue(t *testing.T) {
	uri := os.Getenv("PHEME_TEST_RABBIT_URI")
	if uri == "" {
		t.Skip("PHEME_TEST_RABBIT_URI not set")
	}
	queue := fmt.Sprintf("phemetest.%s", t.Name())

	first, err := NewRabbit(uri, queue)
	if err != nil {
		t.Fatalf("first connect: %v", err)
	}
	defer func() {
		_, _ = first.channel.QueueDelete(queue, false, false, false)
		_, _ = first.channel.QueueDelete(queue+dlqSuffix, false, false, false)
		_ = first.channel.ExchangeDelete(queue+dlxSuffix, false, false)
		_ = first.Close(context.Background())
	}()

	second, err := NewRabbit(uri, queue)
	if err != nil {
		t.Fatalf("re-declaring an existing queue failed: %v — a restart would not come back up", err)
	}
	_ = second.Close(context.Background())
}

func TestRabbitCloseIsSafe(t *testing.T) {
	uri := os.Getenv("PHEME_TEST_RABBIT_URI")
	if uri == "" {
		t.Skip("PHEME_TEST_RABBIT_URI not set")
	}
	queue := fmt.Sprintf("phemetest.%s", t.Name())
	r, err := NewRabbit(uri, queue)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, _ = r.channel.QueueDelete(queue, false, false, false)
	_, _ = r.channel.QueueDelete(queue+dlqSuffix, false, false, false)
	_ = r.channel.ExchangeDelete(queue+dlxSuffix, false, false)

	if err := r.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}
}
