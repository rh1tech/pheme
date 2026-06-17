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

	"github.com/rh1tech/pheme/api/internal/bootstrap"
	"github.com/rh1tech/pheme/api/internal/config"
	"github.com/rh1tech/pheme/api/internal/message"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()
	b := bootstrap.New(cfg, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := b.Store(ctx)
	if err != nil {
		logger.Error("store init", "error", err)
		os.Exit(1)
	}
	consumer, err := b.Consumer()
	if err != nil {
		logger.Error("consumer init", "error", err)
		os.Exit(1)
	}
	sender, err := b.Push(ctx)
	if err != nil {
		logger.Error("push init", "error", err)
		os.Exit(1)
	}
	bus, err := b.Live()
	if err != nil {
		logger.Error("live init", "error", err)
		os.Exit(1)
	}

	dispatcher := message.NewDispatcher(db, sender, bus, logger)

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

	closeCtx := context.Background()
	_ = consumer.Close(closeCtx)
	_ = db.Close(closeCtx)
	_ = b.Close()
	logger.Info("dispatcher stopped")
}
