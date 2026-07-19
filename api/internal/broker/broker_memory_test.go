package broker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// The queue that carries notification work off the request path. Two properties matter: a task
// published is a task delivered, and a consumer that is told to stop actually stops — a Consume
// that ignores cancellation holds a goroutine (and the process) open at shutdown.

func TestMemoryPublishAndConsumeDeliversEveryTask(t *testing.T) {
	b := NewMemory(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var got []string
	done := make(chan struct{})

	go func() {
		_ = b.Consume(ctx, func(_ context.Context, task domain.NotifyTask) error {
			mu.Lock()
			got = append(got, task.Title)
			if len(got) == 3 {
				close(done)
			}
			mu.Unlock()
			return nil
		})
	}()

	for _, id := range []string{"m1", "m2", "m3"} {
		if err := b.Publish(ctx, domain.NotifyTask{Title: id}); err != nil {
			t.Fatalf("publish %s: %v", id, err)
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("only %d of 3 tasks were delivered: %v", len(got), got)
	}

	mu.Lock()
	defer mu.Unlock()
	// Order matters: notifications for one conversation arriving out of order read as the
	// conversation itself being out of order.
	for i, want := range []string{"m1", "m2", "m3"} {
		if got[i] != want {
			t.Errorf("task %d = %q, want %q — the queue reordered", i, got[i], want)
		}
	}
}

// A handler that fails must not stop the queue. One bad notification cannot be allowed to end
// delivery for everyone else.
func TestMemoryKeepsGoingAfterAHandlerError(t *testing.T) {
	b := NewMemory(4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seen := make(chan string, 4)
	go func() {
		_ = b.Consume(ctx, func(_ context.Context, task domain.NotifyTask) error {
			seen <- task.Title
			if task.Title == "bad" {
				return errors.New("handler failed")
			}
			return nil
		})
	}()

	for _, id := range []string{"bad", "good"} {
		if err := b.Publish(ctx, domain.NotifyTask{Title: id}); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	for _, want := range []string{"bad", "good"} {
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("the queue stopped after a handler error; %q was never delivered", want)
		}
	}
}

// Cancelling the context must end Consume. Without this, shutdown hangs on a goroutine that is
// still waiting for work that will never come.
func TestMemoryConsumeStopsWhenCancelled(t *testing.T) {
	b := NewMemory(1)
	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan error, 1)
	go func() { stopped <- b.Consume(ctx, func(context.Context, domain.NotifyTask) error { return nil }) }()

	cancel()
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Consume returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Consume ignored cancellation; shutdown would hang")
	}
}

// Publishing into a full buffer must respect cancellation rather than blocking for ever. This is
// the back-pressure path: if it cannot be interrupted, a saturated queue wedges the request that
// is trying to publish.
func TestMemoryPublishRespectsCancellationWhenFull(t *testing.T) {
	b := NewMemory(1)
	ctx, cancel := context.WithCancel(context.Background())

	// Fill the single slot; nothing is consuming.
	if err := b.Publish(ctx, domain.NotifyTask{Title: "first"}); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	blocked := make(chan error, 1)
	go func() { blocked <- b.Publish(ctx, domain.NotifyTask{Title: "second"}) }()

	// It should still be waiting.
	select {
	case err := <-blocked:
		t.Fatalf("a publish into a full buffer returned early: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-blocked:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("blocked publish returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a blocked publish ignored cancellation; the caller would wait for ever")
	}
}

// A nonsensical buffer size must not produce an unbuffered queue, which would make every publish
// block until a consumer happened to be ready.
func TestMemoryBufferSizeFallsBackForNonsense(t *testing.T) {
	for _, size := range []int{0, -1, -1000} {
		b := NewMemory(size)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		// With no consumer running, this can only succeed if the buffer is real.
		err := b.Publish(ctx, domain.NotifyTask{Title: "x"})
		cancel()
		if err != nil {
			t.Errorf("NewMemory(%d) produced a queue that blocks with no consumer: %v", size, err)
		}
	}
}

func TestMemoryCloseIsSafeAndRepeatable(t *testing.T) {
	b := NewMemory(1)
	if err := b.Close(context.Background()); err != nil {
		t.Errorf("Close: %v", err)
	}
	// Shutdown paths call Close on every driver, sometimes more than once; failing there would
	// mask the real reason a process was stopping.
	if err := b.Close(context.Background()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
