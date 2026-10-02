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
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"mime"
	"net"
	netmail "net/mail"
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
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}

	conn, err := s.dial(ctx)
	if err != nil {
		return err
	}
	// Every path below ends with the connection closed, by Quit on success.
	// A second close reports an error nobody can act on, so it is dropped.
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp: set deadline: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: greeting: %w", err)
	}
	// Quit closes the connection; Close is a safety net for the error paths.
	defer func() { _ = client.Close() }()

	if s.cfg.Mode == TLSStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp: relay does not support required STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{
			ServerName: s.cfg.Host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
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
		// The write already failed; the close cannot add anything useful.
		_ = writer.Close()
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
		_ = conn.Close() // the handshake error is the one worth reporting
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

	writeHeader(&buf, "From", encodeAddress(from))
	writeHeader(&buf, "To", msg.To)
	writeHeader(&buf, "Subject", mime.QEncoding.Encode("utf-8", sanitiseHeader(msg.Subject)))
	writeHeader(&buf, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&buf, "Message-ID", messageID(envelopeSender(from)))
	writeHeader(&buf, "MIME-Version", "1.0")
	writeHeader(&buf, "Content-Type", `text/plain; charset="utf-8"`)
	writeHeader(&buf, "Content-Transfer-Encoding", "8bit")
	buf.WriteString("\r\n")

	buf.WriteString(normaliseBody(msg.Body))
	return buf.Bytes()
}

// encodeAddress renders a "Name <addr>" sender with any non-ASCII display name
// encoded as RFC 2047 requires. A value that does not parse is passed through.
func encodeAddress(from string) string {
	for i := 0; i < len(from); i++ {
		if from[i] > 0x7E {
			if parsed, err := netmail.ParseAddress(sanitiseHeader(from)); err == nil {
				return parsed.String()
			}
			return from
		}
	}
	return from // already plain ASCII, and kept exactly as configured
}

// messageID returns a unique Message-ID in the sender's domain. Many relays
// and spam filters treat a message without one as suspect.
func messageID(sender string) string {
	domain := "localhost"
	if at := strings.LastIndexByte(sender, '@'); at >= 0 && at+1 < len(sender) {
		domain = sender[at+1:]
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		// crypto/rand does not fail on supported systems; the time still
		// makes the identifier unique on this host.
		return fmt.Sprintf("<%d@%s>", time.Now().UnixNano(), domain)
	}
	return "<" + hex.EncodeToString(id[:]) + "@" + domain + ">"
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

// normaliseBody applies CRLF line endings. smtp.Client.Data handles dot stuffing.
func normaliseBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")

	return strings.ReplaceAll(body, "\n", "\r\n")
}
