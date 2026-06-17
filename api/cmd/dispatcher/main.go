// Command dispatcher consumes notification tasks from the broker, persists each
// as a message, fans it out to subscribed devices via push, records deliveries,
// and emits a live event.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rh1tech/pheme/api/internal/broker"
	"github.com/rh1tech/pheme/api/internal/config"
	"github.com/rh1tech/pheme/api/internal/live"
	"github.com/rh1tech/pheme/api/internal/message"
	"github.com/rh1tech/pheme/api/internal/push"
	"github.com/rh1tech/pheme/api/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	_ = config.Load()

	// TODO: swap in MongoDB-backed store, RabbitMQ consumer, FCM/Web Push sender,
	// and Redis-backed live bus. The in-memory wiring below documents the shape
	// of the pipeline; cross-process delivery requires the shared infrastructure.
	db := store.NewMemory()
	consumer := broker.NewMemory(0)
	sender := push.NewLogSender()
	bus := live.NewMemoryBus()

	dispatcher := message.NewDispatcher(db, sender, bus, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		logger.Info("dispatcher shutting down")
		cancel()
	}()

	logger.Info("dispatcher started")
	if err := consumer.Consume(ctx, dispatcher.Handle); err != nil && err != context.Canceled {
		logger.Error("consume", "error", err)
		os.Exit(1)
	}
	_ = consumer.Close(context.Background())
	_ = db.Close(context.Background())
	logger.Info("dispatcher stopped")
}
