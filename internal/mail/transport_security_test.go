package mail

import (
	"bufio"
	"context"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"imvault/internal/closer"
	"imvault/internal/testutil"
)

func TestSMTPPreservesLeadingDots(t *testing.T) {
	relay := newFakeSMTP(t)
	host, port := relay.addr()
	sender := NewSMTP(Config{Host: host, Port: port, Mode: TLSNone, From: "sender@example.com"})
	body := ".first\n.\n..third\nlast\n"
	if err := sender.Send(t.Context(), Message{To: "alice@example.com", Body: body}); err != nil {
		t.Fatal(err)
	}
	// The fixture captures wire bytes. Decode the SMTP dot protocol once,
	// exactly as the recipient's relay does.
	raw := relay.wait(t).data + ".\r\n"
	decoded, err := textproto.NewReader(bufio.NewReader(strings.NewReader(raw))).ReadDotBytes()
	if err != nil {
		t.Fatal(err)
	}
	_, got, ok := strings.Cut(string(decoded), "\n\n")
	if !ok || got != body {
		t.Fatalf("received body = %q, want %q", got, body)
	}
}

func TestSTARTTLSCannotDowngrade(t *testing.T) {
	relay := newFakeSMTP(t)
	host, port := relay.addr()
	sender := NewSMTP(Config{Host: host, Port: port, Mode: TLSStartTLS, From: "sender@example.com"})
	err := sender.Send(t.Context(), Message{To: "alice@example.com", Subject: "Password reset", Body: "secret reset token"})
	if err == nil {
		t.Fatal("STARTTLS sent a reset token through a relay that advertised no TLS")
	}
	select {
	case <-relay.messages:
		t.Fatal("relay received plaintext mail despite required STARTTLS")
	default:
	}
}

func TestSMTPTimeoutIncludesGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, listener)
	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer closer.Discard(conn) // may run after the test has finished
		<-release // Connected relay never sends a greeting.
	}()
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatal(err)
	}
	sender := NewSMTP(Config{Host: host, Port: port, Mode: TLSNone, Timeout: 30 * time.Millisecond})
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sender.Send(ctx, Message{To: "alice@example.com"}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("silent relay succeeded")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("silent SMTP greeting ignored both configured timeout and context deadline")
	}
}
