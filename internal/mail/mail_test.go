// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// received is one message captured by the fake relay.
type received struct {
	from string
	to   []string
	data string
}

// fakeSMTP is a minimal SMTP server: enough of the protocol to accept a message
// and hand it back for assertions.
type fakeSMTP struct {
	listener net.Listener
	messages chan received
	// advertiseStartTLS controls whether the server claims to support STARTTLS.
	// It is deliberately never honoured, so the test does not need a
	// certificate; the point is only to exercise the branch.
	advertiseStartTLS bool
	// requireAuth rejects mail unless an AUTH exchange happened.
	requireAuth bool
	authed      bool
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := &fakeSMTP{
		listener: listener,
		messages: make(chan received, 16),
	}

	go server.serve()

	t.Cleanup(func() { listener.Close() })
	return server
}

func (s *fakeSMTP) addr() (string, int) {
	host, port, _ := net.SplitHostPort(s.listener.Addr().String())
	number := 0
	for _, c := range port {
		number = number*10 + int(c-'0')
	}
	return host, number
}

func (s *fakeSMTP) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTP) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	reply := func(line string) {
		writer.WriteString(line + "\r\n")
		writer.Flush()
	}

	reply("220 fake ESMTP ready")

	var current received
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		upper := strings.ToUpper(command)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			reply("250-fake greets you")
			if s.advertiseStartTLS {
				reply("250-STARTTLS")
			}
			reply("250 AUTH PLAIN")

		case upper == "STARTTLS":
			// The test never negotiates real TLS.
			reply("454 TLS not available in this test")

		case strings.HasPrefix(upper, "AUTH"):
			s.authed = true
			reply("235 authenticated")

		case strings.HasPrefix(upper, "MAIL FROM:"):
			if s.requireAuth && !s.authed {
				reply("530 authentication required")
				continue
			}
			current = received{from: strings.TrimSpace(command[len("MAIL FROM:"):])}
			reply("250 ok")

		case strings.HasPrefix(upper, "RCPT TO:"):
			current.to = append(current.to, strings.TrimSpace(command[len("RCPT TO:"):]))
			reply("250 ok")

		case upper == "DATA":
			reply("354 end with <CRLF>.<CRLF>")
			var body strings.Builder
			for {
				bodyLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(bodyLine, "\r\n") == "." {
					break
				}
				body.WriteString(bodyLine)
			}
			current.data = body.String()
			s.messages <- current
			current = received{}
			reply("250 queued")

		case upper == "QUIT":
			reply("221 bye")
			return

		case upper == "RSET":
			current = received{}
			reply("250 ok")

		default:
			reply("250 ok")
		}
	}
}

// wait returns the next captured message, or fails.
func (s *fakeSMTP) wait(t *testing.T) received {
	t.Helper()

	select {
	case msg := <-s.messages:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no message arrived")
		return received{}
	}
}

func TestSendDeliversThroughARelay(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.addr()

	sender := NewSMTP(Config{
		Host: host,
		Port: port,
		From: "imvault <no-reply@example.com>",
		Mode: TLSNone,
	})

	if !sender.Enabled() {
		t.Fatal("a configured sender should report as enabled")
	}

	err := sender.Send(context.Background(), Message{
		To:      "alice@example.com",
		Subject: "Reset your password",
		Body:    "Click here: https://img.example.com/reset/abc123\n",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	msg := server.wait(t)
	if msg.from != "<no-reply@example.com>" {
		t.Errorf("envelope sender = %q, want the address from the From header", msg.from)
	}
	if len(msg.to) != 1 || msg.to[0] != "<alice@example.com>" {
		t.Errorf("recipients = %v", msg.to)
	}

	for _, want := range []string{
		"From: imvault <no-reply@example.com>",
		"To: alice@example.com",
		"Subject: Reset your password",
		"Content-Type: text/plain",
		"https://img.example.com/reset/abc123",
	} {
		if !strings.Contains(msg.data, want) {
			t.Errorf("message is missing %q:\n%s", want, msg.data)
		}
	}
}

func TestSendAuthenticatesWhenCredentialsAreSet(t *testing.T) {
	server := newFakeSMTP(t)
	server.requireAuth = true
	host, port := server.addr()

	sender := NewSMTP(Config{
		Host:     host,
		Port:     port,
		Username: "relay-user",
		Password: "relay-pass",
		From:     "no-reply@example.com",
		Mode:     TLSNone,
	})

	// PlainAuth only hands over credentials on an unencrypted connection when
	// the relay is on the loopback interface, which this one is.
	if err := sender.Send(context.Background(), Message{
		To:      "alice@example.com",
		Subject: "Hello",
		Body:    "Body",
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	if !server.authed {
		t.Error("the relay was never sent an AUTH exchange")
	}
	server.wait(t)
}

func TestSendReportsDialFailure(t *testing.T) {
	// Nothing is listening on this port.
	sender := NewSMTP(Config{
		Host:    "127.0.0.1",
		Port:    1,
		From:    "no-reply@example.com",
		Mode:    TLSNone,
		Timeout: time.Second,
	})

	if err := sender.Send(context.Background(), Message{
		To:      "alice@example.com",
		Subject: "Hello",
		Body:    "Body",
	}); err == nil {
		t.Fatal("expected a dial failure")
	}
}

func TestSendHonoursContextCancellation(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.addr()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sender := NewSMTP(Config{Host: host, Port: port, From: "a@example.com", Mode: TLSNone})
	if err := sender.Send(ctx, Message{To: "b@example.com", Subject: "x", Body: "y"}); err == nil {
		t.Fatal("expected a cancelled context to stop the send")
	}
}

func TestDisabledSenderReportsItself(t *testing.T) {
	sender := Disabled{}
	if sender.Enabled() {
		t.Error("a disabled sender must not claim to be enabled")
	}
	if err := sender.Send(context.Background(), Message{To: "a@example.com", Subject: "x"}); err != nil {
		t.Errorf("a disabled sender should not error: %v", err)
	}
}

func TestHeadersCannotBeInjected(t *testing.T) {
	raw := buildMessage("no-reply@example.com", Message{
		To:      "alice@example.com\r\nBcc: attacker@example.com",
		Subject: "Reset\r\nX-Injected: yes",
		Body:    "Body",
	})
	message := string(raw)

	// Header section and body are separated by exactly one blank line.
	head, body, found := strings.Cut(message, "\r\n\r\n")
	if !found {
		t.Fatal("no header/body separator")
	}

	// The property that matters is that no *header line* was introduced. The
	// injected text may survive inside the original value, which is harmless.
	allowed := map[string]bool{
		"From": true, "To": true, "Subject": true, "Date": true,
		"MIME-Version": true, "Content-Type": true,
		"Content-Transfer-Encoding": true,
	}

	lines := strings.Split(head, "\r\n")
	names := make([]string, 0, len(lines))
	for _, line := range lines {
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("header line without a colon: %q", line)
		}
		names = append(names, name)
		if !allowed[name] {
			t.Errorf("an unexpected header was injected: %q", name)
		}
	}

	for _, injected := range []string{"Bcc", "X-Injected"} {
		for _, name := range names {
			if name == injected {
				t.Errorf("%s became a real header", injected)
			}
		}
	}
	if len(names) != len(allowed) {
		t.Errorf("header count = %d, want %d: %v", len(names), len(allowed), names)
	}

	if body != "Body" {
		t.Errorf("body = %q, want %q", body, "Body")
	}
}

func TestBodyLineEndingsAndDotStuffing(t *testing.T) {
	message := string(buildMessage("a@example.com", Message{
		To:      "b@example.com",
		Subject: "s",
		Body:    "line one\n.hidden\nline three\r\nline four",
	}))

	if strings.Contains(message, "\n") && strings.Contains(strings.ReplaceAll(message, "\r\n", ""), "\n") {
		t.Error("a bare newline survived into the message")
	}
	if !strings.Contains(message, "\r\n..hidden\r\n") {
		t.Errorf("a leading dot was not stuffed:\n%q", message)
	}
	if !strings.Contains(message, "line four") {
		t.Error("the last line was lost")
	}
}

func TestEnvelopeSenderExtractsTheAddress(t *testing.T) {
	tests := map[string]string{
		"imvault <no-reply@example.com>": "no-reply@example.com",
		"no-reply@example.com":           "no-reply@example.com",
		"  spaced@example.com  ":         "spaced@example.com",
	}
	for in, want := range tests {
		if got := envelopeSender(in); got != want {
			t.Errorf("envelopeSender(%q) = %q, want %q", in, got, want)
		}
	}
}

// Ensure the fake server satisfies the parts of net.Conn we rely on.
var _ io.Closer = (*net.TCPConn)(nil)
