// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mail sends the handful of transactional messages imvault needs.
//
// Sending is optional: an instance with no SMTP configuration gets a Sender
// that reports Enabled() == false and logs what it would have sent, so password
// reset still works through administrator-issued links.
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Message is one plain-text email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers messages, or declines to.
type Sender interface {
	// Enabled reports whether messages will actually leave the process.
	Enabled() bool
	Send(ctx context.Context, msg Message) error
}

// TLSMode selects how the connection to the relay is protected.
type TLSMode string

const (
	// TLSStartTLS upgrades an ordinary connection, which is what most relays
	// on port 587 expect.
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit encrypts from the first byte, as port 465 expects.
	TLSImplicit TLSMode = "implicit"
	// TLSNone is for a trusted local relay or a development catcher.
	TLSNone TLSMode = "none"
)

// Config describes an SMTP relay.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	// From is the envelope and header sender, for example
	// "imvault <no-reply@example.com>".
	From string
	// Mode defaults to STARTTLS when empty.
	Mode    TLSMode
	Timeout time.Duration
}

// SMTP sends mail through a relay.
type SMTP struct {
	cfg     Config
	timeout time.Duration
}

// NewSMTP returns a sender for the relay described by cfg.
func NewSMTP(cfg Config) *SMTP {
	if cfg.Mode == "" {
		cfg.Mode = TLSStartTLS
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &SMTP{cfg: cfg, timeout: timeout}
}

// Enabled always reports true: a configured sender is expected to work, and
// failures are surfaced to the caller instead of being silently swallowed.
func (s *SMTP) Enabled() bool { return true }

// Send delivers one message.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp: greeting: %w", err)
	}
	// Quit closes the connection; Close is a safety net for the error paths.
	defer client.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(s.timeout))
	}

	if s.cfg.Mode == TLSStartTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{
				ServerName: s.cfg.Host,
				MinVersion: tls.VersionTLS12,
			}); err != nil {
				return fmt.Errorf("smtp: starttls: %w", err)
			}
		}
		// A relay that does not advertise STARTTLS is used as-is, which is the
		// norm for a local relay on the loopback interface.
	}

	if s.cfg.Username != "" {
		// PlainAuth refuses to hand over credentials on an unencrypted
		// connection unless the relay is on the loopback interface, which is
		// exactly the check we want.
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}

	from := envelopeSender(s.cfg.From)
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp: sender rejected: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp: recipient rejected: %w", err)
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: start body: %w", err)
	}
	if _, err := writer.Write(buildMessage(s.cfg.From, msg)); err != nil {
		writer.Close()
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp: finish body: %w", err)
	}

	return client.Quit()
}

func (s *SMTP) dial(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	conn, err := (&net.Dialer{Timeout: s.timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("smtp: dial %s: %w", address, err)
	}
	if s.cfg.Mode != TLSImplicit {
		return conn, nil
	}
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName: s.cfg.Host,
		MinVersion: tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("smtp: handshake: %w", err)
	}
	return tlsConn, nil
}

// Disabled is a Sender for instances with no relay configured. It logs what it
// was asked to send so a developer can follow a reset link out of the logs.
type Disabled struct {
	Log *slog.Logger
}

// Enabled reports false.
func (d Disabled) Enabled() bool { return false }

// Send logs the message instead of delivering it.
func (d Disabled) Send(_ context.Context, msg Message) error {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	log.Info("mail not configured; message not sent",
		"to", msg.To,
		"subject", msg.Subject,
		"body", msg.Body,
	)
	return nil
}

// envelopeSender extracts the address from a "Name <addr>" value.
func envelopeSender(from string) string {
	if start := strings.LastIndex(from, "<"); start >= 0 {
		if end := strings.Index(from[start:], ">"); end > 0 {
			return from[start+1 : start+end]
		}
	}
	return strings.TrimSpace(from)
}

// buildMessage renders an RFC 5322 plain-text message.
//
// Header values are stripped of newlines: a display name or subject that came
// from user input must not be able to inject extra headers.
func buildMessage(from string, msg Message) []byte {
	var buf bytes.Buffer

	writeHeader(&buf, "From", from)
	writeHeader(&buf, "To", msg.To)
	writeHeader(&buf, "Subject", msg.Subject)
	writeHeader(&buf, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&buf, "MIME-Version", "1.0")
	writeHeader(&buf, "Content-Type", `text/plain; charset="utf-8"`)
	writeHeader(&buf, "Content-Transfer-Encoding", "8bit")
	buf.WriteString("\r\n")

	buf.WriteString(normaliseBody(msg.Body))
	return buf.Bytes()
}

func writeHeader(buf *bytes.Buffer, name, value string) {
	buf.WriteString(name)
	buf.WriteString(": ")
	buf.WriteString(sanitiseHeader(value))
	buf.WriteString("\r\n")
}

// sanitiseHeader removes anything that could terminate the header or start a
// new one.
func sanitiseHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

// normaliseBody applies the CRLF line endings the wire format requires and
// dot-stuffs lines that would otherwise look like the end of the message.
func normaliseBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, ".") {
			lines[i] = "." + line
		}
	}

	return strings.Join(lines, "\r\n")
}
