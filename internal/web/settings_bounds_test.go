// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"strconv"
	"testing"
	"time"
)

// Values too large to represent are refused rather than wrapping round.
func TestSettingsParsersRefuseOverflow(t *testing.T) {
	for _, raw := range []string{
		strconv.FormatInt(maxRetentionHours+1, 10),
		strconv.FormatInt(maxRetentionHours/24+1, 10) + "d",
		"9223372036854775807",
	} {
		if window, err := parseRetention(raw); err == nil {
			t.Errorf("parseRetention(%q) = %s, want a refusal", raw, window)
		}
	}
	if window, err := parseRetention(strconv.FormatInt(maxRetentionHours, 10)); err != nil || window <= 0 {
		t.Errorf("the longest representable window was refused: %s %v", window, err)
	}
	if _, err := parseMegabytes(strconv.FormatInt(maxAdminMegabytes+1, 10)); err == nil {
		t.Error("parseMegabytes accepted a value that overflows")
	}
	if got, err := parseMegabytes("1"); err != nil || got != 1<<20 {
		t.Errorf("parseMegabytes(1) = %d %v", got, err)
	}
	if _, err := parseRetention("-5"); err == nil {
		t.Error("a negative window was accepted")
	}
	if got, err := parseRetention("7d"); err != nil || got != 7*24*time.Hour {
		t.Errorf("parseRetention(7d) = %s %v", got, err)
	}
}
