package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/rh1tech/pheme/api/internal/domain"
)

// Rabbit is a RabbitMQ-backed Publisher and Consumer.
//
// Topology (declared idempotently on connect):
//   - a durable work queue with a dead-letter exchange,
//   - a dead-letter exchange + queue capturing rejected/poison messages.
//
// The publisher uses publisher confirms so Publish only returns nil once the
// broker has accepted the message. The consumer acks only after the handler
// succeeds; handler errors nack (without requeue) so the message is routed to
// the DLQ instead of looping forever.
type Rabbit struct {
	conn    *amqp.Connection
	channel *amqp.Channel
	queue   string
}

const (
	dlxSuffix = ".dlx"
	dlqSuffix = ".dlq"
)

// NewRabbit dials RabbitMQ and declares the queue topology.
func NewRabbit(uri, queue string) (*Rabbit, error) {
	conn, err := amqp.Dial(uri)
	if err != nil {
		return nil, err
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	r := &Rabbit{conn: conn, channel: ch, queue: queue}
	if err := r.declare(); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, err
	}
	return r, nil
}

func (r *Rabbit) declare() error {
	dlx := r.queue + dlxSuffix
	dlq := r.queue + dlqSuffix

	if err := r.channel.ExchangeDeclare(dlx, "fanout", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := r.channel.QueueDeclare(dlq, true, false, false, false, nil); err != nil {
		return err
	}
	if err := r.channel.QueueBind(dlq, "", dlx, false, nil); err != nil {
		return err
	}
	// Primary work queue routes rejected messages to the DLX.
	_, err := r.channel.QueueDeclare(r.queue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": dlx,
	})
	return err
}

// Publish sends a task to the work queue and waits for the broker confirm.
func (r *Rabbit) Publish(ctx context.Context, task domain.NotifyTask) error {
	body, err := json.Marshal(task)
	if err != nil {
		return err
	}
	conf, err := r.channel.PublishWithDeferredConfirmWithContext(ctx, "", r.queue, true, false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now().UTC(),
		})
	if err != nil {
		return err
	}
	ok, err := conf.WaitContext(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("broker did not confirm publish to %q", r.queue)
	}
	return nil
}

// Consume processes tasks until the context is cancelled. Successful handling
// acks the delivery; handler errors nack without requeue, sending the message
// to the dead-letter queue.
func (r *Rabbit) Consume(ctx context.Context, h Handler) error {
	if err := r.channel.Qos(16, 0, false); err != nil {
		return err
	}
	deliveries, err := r.channel.Consume(r.queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("rabbitmq delivery channel closed")
			}
			var task domain.NotifyTask
			if err := json.Unmarshal(d.Body, &task); err != nil {
				_ = d.Nack(false, false) // malformed: straight to DLQ
				continue
			}
			if err := h(ctx, task); err != nil {
				_ = d.Nack(false, false) // handler failed: DLQ for inspection/retry
				continue
			}
			_ = d.Ack(false)
		}
	}
}

// Close shuts down the channel and connection.
func (r *Rabbit) Close(context.Context) error {
	if r.channel != nil {
		_ = r.channel.Close()
	}
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// Verify Rabbit satisfies both broker roles.
var (
	_ Publisher = (*Rabbit)(nil)
	_ Consumer  = (*Rabbit)(nil)
)
