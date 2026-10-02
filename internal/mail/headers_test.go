// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"bufio"
	"mime"
	"net/textproto"
	"strings"
	"testing"
)

func headersOf(t *testing.T, raw []byte) textproto.MIMEHeader {
	t.Helper()
	header, err := textproto.NewReader(bufio.NewReader(strings.NewReader(string(raw)))).ReadMIMEHeader()
	if err != nil {
		t.Fatal(err)
	}
	return header
}

func TestMessagesCarryAUniqueMessageID(t *testing.T) {
	first := headersOf(t, buildMessage("Imvault <no-reply@img.example.com>", Message{To: "a@example.com", Subject: "Hi", Body: "x"}))
	second := headersOf(t, buildMessage("Imvault <no-reply@img.example.com>", Message{To: "a@example.com", Subject: "Hi", Body: "x"}))
	id := first.Get("Message-Id")
	if !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@img.example.com>") {
		t.Fatalf("Message-ID = %q", id)
	}
	if id == second.Get("Message-Id") {
		t.Fatal("two messages share a Message-ID")
	}
}

func TestNonASCIIHeadersAreEncoded(t *testing.T) {
	raw := buildMessage("Frühstück <no-reply@example.com>", Message{To: "a@example.com", Subject: "Passwort zurücksetzen", Body: "x"})
	head, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for i := 0; i < len(head); i++ {
		if head[i] > 0x7E {
			t.Fatalf("the header section contains raw non-ASCII: %q", head)
		}
	}
	header := headersOf(t, raw)
	decoder := new(mime.WordDecoder)
	subject, err := decoder.DecodeHeader(header.Get("Subject"))
	if err != nil || subject != "Passwort zurücksetzen" {
		t.Fatalf("subject decodes to %q (%v)", subject, err)
	}
	from, err := decoder.DecodeHeader(header.Get("From"))
	if err != nil || !strings.Contains(from, "Frühstück") {
		t.Fatalf("sender decodes to %q (%v)", from, err)
	}
}

func TestEncodedSubjectsCannotInjectHeaders(t *testing.T) {
	raw := buildMessage("no-reply@example.com", Message{To: "a@example.com", Subject: "Grüße\r\nBcc: victim@example.com", Body: "x"})
	if header := headersOf(t, raw); header.Get("Bcc") != "" {
		t.Fatal("an encoded subject introduced a header")
	}
}
