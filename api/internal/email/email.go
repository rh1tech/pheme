// Package email sends transactional mail (verification codes, password-reset
// codes). It follows the project's interface + driver convention: LogSender is
// the zero-dependency default that logs the message (handy in development) and
// SMTPSender relays through a configured SMTP server in production.
package email

import (
	"context"
	"log/slog"
)

// Sender delivers a single transactional email. Implementations must be safe
// for concurrent use.
type Sender interface {
	Send(ctx context.Context, to, subject, text, html string) error
}

// LogSender records the message via slog instead of sending it. Used by default
// so the services run with no mail infrastructure; in development the 6-digit
// code is visible in the app logs.
type LogSender struct {
	logger *slog.Logger
}

// NewLogSender returns a LogSender. A nil logger falls back to slog.Default.
func NewLogSender(logger *slog.Logger) *LogSender {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogSender{logger: logger}
}

// Send logs the email rather than transmitting it.
func (s *LogSender) Send(_ context.Context, to, subject, text, _ string) error {
	s.logger.Info("email (log driver — not sent)", "to", to, "subject", subject, "body", text)
	return nil
}
