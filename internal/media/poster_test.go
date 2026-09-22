// SPDX-License-Identifier: AGPL-3.0-or-later

package media

import (
	"bytes"
	"context"
	"testing"
	"time"
)

type invalidPosterTool struct{ VideoTool }

func (invalidPosterTool) Available() bool { return true }
func (invalidPosterTool) Probe(context.Context, string) (VideoInfo, error) {
	return VideoInfo{Width: 32, Height: 16, DurationMS: 100}, nil
}
func (invalidPosterTool) Poster(context.Context, string, time.Duration, int) ([]byte, error) {
	return []byte("invalid frame"), nil
}

func TestUndecodableVideoPosterIsReportedAsAFallback(t *testing.T) {
	processor := NewProcessor(32, 64, 82, 0, invalidPosterTool{})
	result, err := processor.ProcessVideo(t.Context(), bytes.NewReader(nil), "unused-by-test-tool", FormatWebM)
	if err != nil {
		t.Fatal(err)
	}
	if result.Thumb == nil || len(result.Warnings) == 0 {
		t.Fatal("placeholder was silently reported as a successful poster")
	}
}
