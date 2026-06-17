// Package broker defines the message-broker contract used to decouple ingest
// from delivery, plus an in-memory implementation for local development.
//
// A RabbitMQ-backed implementation (durable queue, publisher confirms, DLQ)
// should satisfy the same interfaces; see broker_rabbit.go (TODO).
package broker

import (
	"context"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Publisher enqueues notification tasks for asynchronous delivery.
type Publisher interface {
	Publish(ctx context.Context, task domain.NotifyTask) error
	Close(ctx context.Context) error
}

// Handler processes a single task. Returning an error signals the consumer to
// nack/retry (and eventually dead-letter) the message.
type Handler func(ctx context.Context, task domain.NotifyTask) error

// Consumer delivers enqueued tasks to a Handler until the context is cancelled.
type Consumer interface {
	Consume(ctx context.Context, h Handler) error
	Close(ctx context.Context) error
}
