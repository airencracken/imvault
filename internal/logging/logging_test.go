// SPDX-License-Identifier: AGPL-3.0-or-later

package logging

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"
)

// parseLogfmt parses one logfmt line into its pairs, failing on anything that is
// not valid logfmt. It is written here rather than imported so the test does not
// share an implementation with the code it is checking.
func parseLogfmt(t *testing.T, line string) map[string]string {
	t.Helper()

	out := map[string]string{}
	i := 0
	n := len(line)

	for i < n {
		for i < n && (line[i] == ' ') {
			i++
		}
		if i >= n {
			break
		}

		start := i
		for i < n && line[i] != '=' && line[i] != ' ' {
			i++
		}
		if i >= n || line[i] != '=' {
			t.Fatalf("key %q has no value in %q", line[start:i], line)
		}
		key := line[start:i]
		i++ // consume '='

		var value string
		if i < n && line[i] == '"' {
			i++
			var b strings.Builder
			for {
				if i >= n {
					t.Fatalf("unterminated quoted value in %q", line)
				}
				if line[i] == '\\' {
					if i+1 >= n {
						t.Fatalf("dangling escape in %q", line)
					}
					switch line[i+1] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					default:
						b.WriteByte(line[i+1])
					}
					i += 2
					continue
				}
				if line[i] == '"' {
					i++
					break
				}
				b.WriteByte(line[i])
				i++
			}
			value = b.String()
		} else {
			start = i
			for i < n && line[i] != ' ' {
				if line[i] == '"' {
					t.Fatalf("quote inside a bare value in %q", line)
				}
				i++
			}
			value = line[start:i]
		}

		if _, seen := out[key]; seen {
			t.Fatalf("key %q appears twice in %q", key, line)
		}
		out[key] = value
	}

	return out
}

// records splits logger output into parsed records.
func records(t *testing.T, buf *bytes.Buffer) []map[string]string {
	t.Helper()

	raw := strings.TrimRight(buf.String(), "\n")
	if raw == "" {
		return nil
	}

	var out []map[string]string
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			t.Fatal("output contains a blank line")
		}
		out = append(out, parseLogfmt(t, line))
	}
	return out
}

func TestOutputIsOneLogfmtRecordPerLine(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelDebug)

	log.Debug("starting", "addr", ":8080", "data_dir", "/var/lib/imvault")
	log.Info("request", "method", "POST", "path", "/upload", "status", 200, "duration", "46ms")
	log.Warn("csrf rejection", "method", "POST", "path", "/logout")

	got := records(t, &buf)
	if len(got) != 3 {
		t.Fatalf("got %d records, want 3", len(got))
	}

	// Every record carries the three keys a log pipeline relies on.
	for i, record := range got {
		for _, key := range []string{"time", "level", "msg"} {
			if _, ok := record[key]; !ok {
				t.Errorf("record %d is missing %q: %+v", i, key, record)
			}
		}
	}

	if got[0]["level"] != "DEBUG" || got[0]["msg"] != "starting" {
		t.Errorf("first record = %+v", got[0])
	}
	if got[0]["addr"] != ":8080" {
		t.Errorf("addr = %q, want the value as written", got[0]["addr"])
	}
	if got[1]["status"] != "200" {
		t.Errorf("a numeric value should round-trip: %+v", got[1])
	}
	// A message with a space is quoted, and unquotes back to what was logged.
	if got[2]["msg"] != "csrf rejection" {
		t.Errorf("msg = %q, want %q", got[2]["msg"], "csrf rejection")
	}
}

func TestAwkwardValuesStayOnOneLine(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelDebug)

	// The values that can break a naive format: newlines, quotes, equals signs,
	// tabs and non-ASCII text.
	log.Error("panic serving request",
		"stack", "goroutine 1 [running]:\nmain.main()\n\t/app/main.go:42 +0x1a",
	)
	log.Error("could not parse", "error", `unexpected "}" at line 3`)
	log.Info("signed in", "username", "a=b c")
	log.Info("unusual", "note", "unicode: ✓ ok")
	log.Info("empty", "value", "")
	log.Info("trailing", "value", "ends with a space ")

	got := records(t, &buf)
	if len(got) != 6 {
		t.Fatalf("got %d records, want 6: %s", len(got), buf.String())
	}

	// A stack trace must not spill onto a second line.
	if !strings.Contains(got[0]["stack"], "\n") {
		t.Errorf("stack did not round-trip its newlines: %q", got[0]["stack"])
	}
	if !strings.Contains(got[0]["stack"], "main.go:42") {
		t.Errorf("stack is truncated: %q", got[0]["stack"])
	}

	if got[1]["error"] != `unexpected "}" at line 3` {
		t.Errorf("error = %q", got[1]["error"])
	}
	if got[2]["username"] != "a=b c" {
		t.Errorf("username = %q", got[2]["username"])
	}
	if got[3]["note"] != "unicode: ✓ ok" {
		t.Errorf("note = %q", got[3]["note"])
	}
	if got[4]["value"] != "" {
		t.Errorf("empty value = %q, want empty", got[4]["value"])
	}
	if got[5]["value"] != "ends with a space " {
		t.Errorf("value = %q, want the trailing space preserved", got[5]["value"])
	}
}

func TestErrorsAreEscapedRatherThanBroken(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelDebug)

	log.Error("request failed", "error", errors.New("connection refused\nretrying"))

	got := records(t, &buf)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	if !strings.Contains(got[0]["error"], "connection refused") {
		t.Errorf("error = %q", got[0]["error"])
	}
}

func TestGroupsFlattenToPrefixedKeys(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelDebug)

	log.Info("request", slog.Group("http",
		slog.String("method", "GET"),
		slog.Int("status", 404),
	))

	got := records(t, &buf)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	// Nested groups become dotted keys, which is still flat logfmt.
	if got[0]["http.method"] != "GET" {
		t.Errorf("http.method = %q, want GET: %+v", got[0]["http.method"], got[0])
	}
	if got[0]["http.status"] != "404" {
		t.Errorf("http.status = %q, want 404", got[0]["http.status"])
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelWarn)

	log.Debug("invisible")
	log.Info("invisible")
	log.Warn("visible")

	got := records(t, &buf)
	if len(got) != 1 || got[0]["msg"] != "visible" {
		t.Errorf("got %+v, want only the warning", got)
	}
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":     slog.LevelDebug,
		"DEBUG":     slog.LevelDebug,
		"  info  ":  slog.LevelInfo,
		"warn":      slog.LevelWarn,
		"warning":   slog.LevelWarn,
		"error":     slog.LevelError,
		"":          slog.LevelInfo,
		"nonsense":  slog.LevelInfo,
		"Info":      slog.LevelInfo,
		"WARN":      slog.LevelWarn,
		"anything ": slog.LevelInfo,
	}

	for in, want := range tests {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNumbersAndBooleansRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelDebug)

	log.Info("counts",
		"int", 42,
		"int64", int64(1<<40),
		"float", 1.5,
		"bool", true,
		"size", "60.3 KiB",
	)

	got := records(t, &buf)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}

	want := map[string]string{
		"int":   "42",
		"int64": strconv.FormatInt(1<<40, 10),
		"float": "1.5",
		"bool":  "true",
		"size":  "60.3 KiB",
	}
	for key, expected := range want {
		if got[0][key] != expected {
			t.Errorf("%s = %q, want %q", key, got[0][key], expected)
		}
	}
}

func TestFatalErrorsGoThroughTheLogger(t *testing.T) {
	// main logs a startup failure rather than printing it, so even the last
	// thing a broken process says is a logfmt record.
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)

	err := fmt.Errorf("IMVAULT_MAX_UPLOAD_BYTES must be positive, got -1")
	log.Error("imvault", "error", err)

	got := records(t, &buf)
	if len(got) != 1 {
		t.Fatalf("got %d records, want 1", len(got))
	}
	if got[0]["msg"] != "imvault" {
		t.Errorf("msg = %q", got[0]["msg"])
	}
	if !strings.Contains(got[0]["error"], "must be positive") {
		t.Errorf("error = %q", got[0]["error"])
	}
}
