package email

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// The mail path carries the six-digit codes that gate signup and password reset. A malformed
// message is not a cosmetic problem: it is an account nobody can create and a password nobody can
// reset, and the failure appears at the recipient rather than in any log here.

func TestLogSenderRecordsTheMessageAndNeverFails(t *testing.T) {
	var buf bytes.Buffer
	s := NewLogSender(slog.New(slog.NewTextHandler(&buf, nil)))

	if err := s.Send(context.Background(), "to@pheme.test", "Your code", "123456", "<b>123456</b>"); err != nil {
		t.Fatalf("log sender returned an error: %v", err)
	}

	out := buf.String()
	// The whole point of this driver in development: the code must be READABLE in the log, or
	// nobody can complete a signup on a machine with no mail infrastructure.
	for _, want := range []string{"to@pheme.test", "Your code", "123456"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log line does not contain %q: %s", want, out)
		}
	}
	// And it says plainly that nothing was sent, so a log is never mistaken for a delivery.
	if !strings.Contains(out, "not sent") {
		t.Errorf("the log line does not say the mail was not sent: %s", out)
	}
}

func TestNewLogSenderToleratesANilLogger(t *testing.T) {
	s := NewLogSender(nil)
	if s == nil {
		t.Fatal("NewLogSender(nil) returned nil")
	}
	if err := s.Send(context.Background(), "to@pheme.test", "s", "t", "h"); err != nil {
		t.Errorf("send with the default logger: %v", err)
	}
}

func TestNewSMTPSenderAcceptsBothFromForms(t *testing.T) {
	cases := []struct {
		name     string
		from     string
		wantAddr string
	}{
		{"bare address", "noreply@pheme.test", "noreply@pheme.test"},
		{"display name", "Pheme <noreply@pheme.test>", "noreply@pheme.test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSMTPSender("mail.example", 25, tc.from, "", "", false)
			if err != nil {
				t.Fatalf("NewSMTPSender: %v", err)
			}
			// The envelope sender must be the bare address even when the header carries a name —
			// a MAIL FROM containing "Pheme <…>" is rejected by the relay.
			if s.fromAddr != tc.wantAddr {
				t.Errorf("fromAddr = %q, want %q", s.fromAddr, tc.wantAddr)
			}
			if s.from != tc.from {
				t.Errorf("from header = %q, want it preserved verbatim", s.from)
			}
		})
	}
}

// A misconfigured From must fail at construction, when someone can still see it, rather than on the
// first signup at three in the morning.
func TestNewSMTPSenderRejectsAnUnparseableFrom(t *testing.T) {
	for _, bad := range []string{"", "not an address", "@pheme.test", "a@b@c"} {
		if _, err := NewSMTPSender("mail.example", 25, bad, "", "", false); err == nil {
			t.Errorf("NewSMTPSender accepted %q as a From address", bad)
		}
	}
}

// No username means no AUTH attempted. Offering credentials to a relay that does not want them is
// how an internal hop starts failing.
func TestNewSMTPSenderOnlyAuthenticatesWhenGivenAUser(t *testing.T) {
	without, err := NewSMTPSender("mail.example", 25, "a@pheme.test", "", "", false)
	if err != nil {
		t.Fatalf("without user: %v", err)
	}
	if without.auth != nil {
		t.Error("AUTH was configured with no username")
	}

	with, err := NewSMTPSender("mail.example", 25, "a@pheme.test", "user", "pass", false)
	if err != nil {
		t.Fatalf("with user: %v", err)
	}
	if with.auth == nil {
		t.Error("AUTH was not configured despite a username")
	}
}

func TestSendRejectsAnInvalidRecipientBeforeDialing(t *testing.T) {
	// Pointed at a port nothing is listening on: if the address check did not come first, this
	// would fail with a dial error instead, and the caller would retry forever against a bad
	// address.
	s, err := NewSMTPSender("127.0.0.1", 1, "a@pheme.test", "", "", false)
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), "not-an-address", "s", "t", "h")
	if err == nil {
		t.Fatal("an invalid recipient was accepted")
	}
	if !strings.Contains(err.Error(), "invalid recipient") {
		t.Errorf("error = %v, want it to name the recipient rather than the dial", err)
	}
}

func TestBuildProducesAWellFormedMultipartMessage(t *testing.T) {
	s, err := NewSMTPSender("mail.example", 25, "Pheme <noreply@pheme.test>", "", "", false)
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	s.now = func() time.Time { return time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC) }

	msgBytes, err := s.build("to@pheme.test", "Your code", "123456", "<b>123456</b>")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	msg := string(msgBytes)

	for _, want := range []string{
		"From: Pheme <noreply@pheme.test>",
		"To: to@pheme.test",
		"Subject: Your code",
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative;",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Type: text/html; charset=UTF-8",
		"123456",
		"<b>123456</b>",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message is missing %q", want)
		}
	}

	// Headers end with a BLANK LINE. Without it every header after the first is read as body text
	// and the mail arrives as a wall of raw headers.
	if !strings.Contains(msg, "\r\n\r\n") {
		t.Error("no blank line separating headers from the body")
	}
	// CRLF line endings throughout: SMTP requires them, and a bare LF can truncate the message at
	// a strict relay.
	if strings.Contains(strings.ReplaceAll(msg, "\r\n", ""), "\n") {
		t.Error("the message contains a bare LF; SMTP requires CRLF")
	}
	// The Message-ID's domain comes from the sender's address, not from the display name.
	if !strings.Contains(msg, "@pheme.test>") {
		t.Errorf("Message-ID does not use the sender's domain: %s", msg)
	}
	// The date is formatted per RFC 1123 with a numeric zone, which is what RFC 5322 wants.
	if !strings.Contains(msg, "Date: Sun, 19 Jul 2026 12:00:00 +0000") {
		t.Errorf("the Date header is not RFC 1123Z: %s", msg)
	}
}

// Both parts must close with the terminating boundary, or a client shows the message as truncated.
func TestBuildClosesItsMultipartBoundary(t *testing.T) {
	s, _ := NewSMTPSender("mail.example", 25, "a@pheme.test", "", "", false)
	msgBytes, err := s.build("to@pheme.test", "s", "text", "<p>html</p>")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	msg := string(msgBytes)

	start := strings.Index(msg, "boundary=\"")
	if start == -1 {
		t.Fatal("no boundary declared")
	}
	rest := msg[start+len("boundary=\""):]
	boundary := rest[:strings.Index(rest, "\"")]

	if !strings.Contains(msg, "--"+boundary+"--") {
		t.Errorf("the multipart body never closes its boundary %q", boundary)
	}
	if strings.Count(msg, "--"+boundary) < 3 {
		t.Errorf("expected two parts and a terminator for boundary %q", boundary)
	}
}

// Two messages must not share a boundary or a Message-ID. A repeated Message-ID is treated as a
// duplicate by many servers and silently dropped — which would look like mail simply not arriving.
func TestBuildIsUniquePerMessage(t *testing.T) {
	s, _ := NewSMTPSender("mail.example", 25, "a@pheme.test", "", "", false)
	firstBytes, err := s.build("to@pheme.test", "s", "t", "h")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	secondBytes, err := s.build("to@pheme.test", "s", "t", "h")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	first, second := string(firstBytes), string(secondBytes)

	idOf := func(msg string) string {
		for _, line := range strings.Split(msg, "\r\n") {
			if strings.HasPrefix(line, "Message-ID:") {
				return line
			}
		}
		return ""
	}
	if idOf(first) == "" {
		t.Fatal("no Message-ID header")
	}
	if idOf(first) == idOf(second) {
		t.Errorf("two messages share a Message-ID (%s); servers drop repeats as duplicates", idOf(first))
	}
}

func TestDomainOf(t *testing.T) {
	cases := map[string]string{
		"noreply@pheme.test": "pheme.test",
		"a@b@pheme.test":     "pheme.test", // the LAST @ wins
		"no-at-sign":         "localhost",  // never empty: an empty domain makes an invalid Message-ID
		"":                   "localhost",
	}
	for in, want := range cases {
		if got := domainOf(in); got != want {
			t.Errorf("domainOf(%q) = %q, want %q", in, got, want)
		}
	}
}
