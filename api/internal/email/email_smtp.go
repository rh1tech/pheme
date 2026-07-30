package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
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
	addr        string // host:port
	host        string // host alone, for PlainAuth / TLS ServerName
	from        string // full From header, e.g. "Pheme <noreply@example.com>"
	fromAddr    string // bare envelope address, e.g. "noreply@example.com"
	auth        smtp.Auth
	insecureTLS bool // skip STARTTLS cert verification (for an internal relay)
	now         func() time.Time
}

// NewSMTPSender builds an SMTP-backed Sender. from accepts either a bare address
// or a "Name <addr>" form. When user is empty no SMTP AUTH is attempted. Set
// insecureTLS for an internal relay whose certificate name does not match the
// dialed host (e.g. a host Postfix reached via host.docker.internal); the hop is
// on the private docker bridge, and the outbound MX TLS is unaffected.
func NewSMTPSender(host string, port int, from, user, pass string, insecureTLS bool) (*SMTPSender, error) {
	addr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("parse from address %q: %w", from, err)
	}
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	return &SMTPSender{
		addr:        fmt.Sprintf("%s:%d", host, port),
		host:        host,
		from:        from,
		fromAddr:    addr.Address,
		auth:        auth,
		insecureTLS: insecureTLS,
		now:         time.Now,
	}, nil
}

// Send transmits a multipart/alternative (text + HTML) message. It uses
// opportunistic STARTTLS so the TLS verification mode can be controlled for the
// internal relay hop (net/smtp.SendMail always verifies against the dialed name).
func (s *SMTPSender) Send(_ context.Context, to, subject, text, html string) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return fmt.Errorf("invalid recipient %q: %w", to, err)
	}
	msg, err := s.build(to, subject, text, html)
	if err != nil {
		return fmt.Errorf("smtp build message: %w", err)
	}

	c, err := smtp.Dial(s.addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.host, InsecureSkipVerify: s.insecureTLS}); err != nil { //nolint:gosec // internal relay hop; opt-in via PHEME_SMTP_INSECURE_TLS
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if s.auth != nil {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(s.auth); err != nil {
				return fmt.Errorf("smtp auth: %w", err)
			}
		}
	}
	if err := c.Mail(s.fromAddr); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return c.Quit()
}

// build assembles a MIME message with CRLF line endings.
func (s *SMTPSender) build(to, subject, text, html string) ([]byte, error) {
	tok, err := randomToken()
	if err != nil {
		return nil, err
	}
	boundary := "pheme-" + tok
	msgID, err := randomToken()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\r\n", args...) }

	w("From: %s", s.from)
	w("To: %s", sanitizeHeader(to))
	w("Subject: %s", sanitizeHeader(subject))
	w("Date: %s", s.now().UTC().Format(time.RFC1123Z))
	w("Message-ID: <%s@%s>", msgID, domainOf(s.fromAddr))
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
	return []byte(b.String()), nil
}

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return addr[i+1:]
	}
	return "localhost"
}

// sanitizeHeader strips CR and LF from an email header value to prevent CRLF injection.
func sanitizeHeader(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
}

func randomToken() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
