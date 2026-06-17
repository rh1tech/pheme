package broker

import (
	"context"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Memory is an in-process Publisher+Consumer backed by a buffered channel. It is
// intended for local development where running RabbitMQ is unnecessary. Tasks do
// not survive a restart and there is no dead-lettering.
type Memory struct {
	ch chan domain.NotifyTask
}

// NewMemory returns an in-memory broker with the given buffer capacity.
func NewMemory(buffer int) *Memory {
	if buffer <= 0 {
		buffer = 256
	}
	return &Memory{ch: make(chan domain.NotifyTask, buffer)}
}

// Publish enqueues a task, blocking if the buffer is full.
func (m *Memory) Publish(ctx context.Context, task domain.NotifyTask) error {
	select {
	case m.ch <- task:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Consume processes tasks until the context is cancelled. Handler errors are
// ignored here (no retry/DLQ) — the production RabbitMQ consumer handles those.
func (m *Memory) Consume(ctx context.Context, h Handler) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case task := <-m.ch:
			_ = h(ctx, task)
		}
	}
}

func (m *Memory) Close(context.Context) error { return nil }
