// SPDX-License-Identifier: AGPL-3.0-or-later

// Package logging builds the process logger.
//
// Output is logfmt: a flat sequence of key=value pairs, one record per line, so
// that a line can be read by eye and parsed by tools without a schema. slog's
// text handler already emits that shape; this package exists so there is one
// place that decides it, and so the guarantee can be tested.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New returns a logfmt logger writing to w at the given level.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
	}))
}

// LevelFromEnv reads IMVAULT_LOG_LEVEL, defaulting to info.
func LevelFromEnv() slog.Level {
	return ParseLevel(os.Getenv("IMVAULT_LOG_LEVEL"))
}

// ParseLevel maps a name onto a level, falling back to info.
func ParseLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
