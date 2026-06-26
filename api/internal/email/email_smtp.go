package email

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

// SMTPSender relays mail through an SMTP server. In production this points at a
// local Postfix instance (which DKIM-signs and delivers direct-to-MX); auth is
// optional, so an internal no-auth relay works by leaving user/pass empty.
type SMTPSender struct {
	addr     string // host:port
	host     string // host alone, for PlainAuth
	from     string // full From header, e.g. "Pheme <noreply@app.example.com>"
	fromAddr string // bare envelope address, e.g. "noreply@app.example.com"
	auth     smtp.Auth
	now      func() time.Time
}

// NewSMTPSender builds an SMTP-backed Sender. from accepts either a bare address
// or a "Name <addr>" form. When user is empty no SMTP AUTH is attempted.
func NewSMTPSender(host string, port int, from, user, pass string) (*SMTPSender, error) {
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("parse from address %q: %w", from, err)
	}
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	return &SMTPSender{
		addr:     fmt.Sprintf("%s:%d", host, port),
		host:     host,
		from:     from,
		fromAddr: addr.Address,
		auth:     auth,
		now:      time.Now,
	}, nil
}

// Send transmits a multipart/alternative (text + HTML) message.
func (s *SMTPSender) Send(_ context.Context, to, subject, text, html string) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("invalid recipient %q: %w", to, err)
	}
	msg := s.build(to, subject, text, html)
	if err := smtp.SendMail(s.addr, s.auth, s.fromAddr, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send: %w", err)
	}
	return nil
}

// build assembles a MIME message with CRLF line endings.
func (s *SMTPSender) build(to, subject, text, html string) []byte {
	boundary := "pheme-" + randomToken()
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\r\n", args...) }

	w("From: %s", s.from)
	w("To: %s", to)
	w("Subject: %s", subject)
	w("Date: %s", s.now().UTC().Format(time.RFC1123Z))
	w("Message-ID: <%s@%s>", randomToken(), domainOf(s.fromAddr))
	w("MIME-Version: 1.0")
	w(`Content-Type: multipart/alternative; boundary="%s"`, boundary)
	w("") // end of headers

	w("--%s", boundary)
	w("Content-Type: text/plain; charset=UTF-8")
	w("Content-Transfer-Encoding: 8bit")
	w("")
	w("%s", text)

	w("--%s", boundary)
	w("Content-Type: text/html; charset=UTF-8")
	w("Content-Transfer-Encoding: 8bit")
	w("")
	w("%s", html)

	w("--%s--", boundary)
	return []byte(b.String())
}

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return addr[i+1:]
	}
	return "localhost"
}

func randomToken() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
