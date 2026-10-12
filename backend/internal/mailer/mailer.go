// Package mailer sends transactional e-mail (account verification). It is deliberately tiny: one interface, an SMTP
// implementation (implicit TLS on 465 or STARTTLS on 587) and a logging implementation for local development.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Message is one e-mail with a plain-text body and an optional HTML alternative.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer delivers a message. Implementations must be safe for concurrent use.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// SMTPConfig is the connection to an SMTP server.
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string // "Name <address@domain>" or a bare address
	// TLS: "tls" (implicit TLS, usually port 465), "starttls" (usually 587) or "none" (local relays only).
	TLS string
}

// Validate reports a configuration that cannot work.
func (c SMTPConfig) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Host) == "" {
		errs = append(errs, errors.New("SMTP_HOST is required"))
	}
	if c.Port <= 0 || c.Port > 65535 {
		errs = append(errs, errors.New("SMTP_PORT must be a valid port"))
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		errs = append(errs, errors.New("SMTP_FROM must be an address such as \"AI Workforce OS <no-reply@example.com>\""))
	}
	switch c.TLS {
	case "tls", "starttls", "none":
	default:
		errs = append(errs, errors.New(`SMTP_TLS must be "tls", "starttls" or "none"`))
	}
	if (c.User == "") != (c.Pass == "") {
		errs = append(errs, errors.New("SMTP_USER and SMTP_PASS go together"))
	}
	return errors.Join(errs...)
}

// SMTP sends through an SMTP server.
type SMTP struct {
	cfg     SMTPConfig
	timeout time.Duration
}

// NewSMTP validates cfg and returns a Mailer.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &SMTP{cfg: cfg, timeout: 20 * time.Second}, nil
}

var _ Mailer = (*SMTP)(nil)

func hasCRLF(s string) bool { return strings.ContainsAny(s, "\r\n") }

// Send delivers m. The whole exchange is bounded by the context deadline or, if it has none, 20 seconds.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	to, err := mail.ParseAddress(m.To)
	if err != nil || hasCRLF(m.To) {
		return fmt.Errorf("mailer: invalid recipient")
	}
	from, err := mail.ParseAddress(s.cfg.From)
	if err != nil {
		return fmt.Errorf("mailer: invalid sender")
	}
	if hasCRLF(m.Subject) {
		return fmt.Errorf("mailer: invalid subject")
	}
	body, err := build(from, to, m)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(s.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Deadline: deadline}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mailer: connect: %w", err)
	}
	_ = conn.SetDeadline(deadline)
	if s.cfg.TLS == "tls" {
		conn = tls.Client(conn, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mailer: handshake: %w", err)
	}
	defer c.Close()
	if s.cfg.TLS == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mailer: starttls: %w", err)
		}
	}
	if s.cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, s.cfg.Host)); err != nil {
			return fmt.Errorf("mailer: auth: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("mailer: sender rejected: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("mailer: recipient rejected: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mailer: data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return fmt.Errorf("mailer: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: send: %w", err)
	}
	return c.Quit()
}

// build renders the RFC 5322 message: multipart/alternative with quoted-printable UTF-8 parts.
func build(from, to *mail.Address, m Message) ([]byte, error) {
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	domain := "localhost"
	if i := strings.LastIndex(from.Address, "@"); i >= 0 {
		domain = from.Address[i+1:]
	}
	boundary := "wf-" + hex.EncodeToString(id[:])
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from.String())
	h("To", to.String())
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", time.Now().UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id[:])+"@"+domain+">")
	h("MIME-Version", "1.0")
	if m.HTML == "" {
		h("Content-Type", `text/plain; charset="utf-8"`)
		h("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n")
		if err := qp(&b, m.Text); err != nil {
			return nil, err
		}
		return []byte(b.String()), nil
	}
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	for _, part := range []struct{ typ, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + part.typ + "; charset=\"utf-8\"\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := qp(&b, part.body); err != nil {
			return nil, err
		}
		b.WriteString("\r\n")
	}
	b.WriteString("--" + boundary + "--\r\n")
	return []byte(b.String()), nil
}

func qp(w io.Writer, s string) error {
	q := quotedprintable.NewWriter(w)
	if _, err := q.Write([]byte(s)); err != nil {
		return err
	}
	return q.Close()
}

// Log writes messages to the log instead of sending them. For local development only: the verification link is in the log.
type Log struct{ Log *slog.Logger }

var _ Mailer = Log{}

// Send implements Mailer.
func (l Log) Send(_ context.Context, m Message) error {
	lg := l.Log
	if lg == nil {
		lg = slog.Default()
	}
	lg.Warn("mailer: EMAIL_DEV_LOG is on, the message is NOT sent", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}
