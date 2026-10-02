// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// A MiB figure too large to hold in bytes is refused rather than wrapping
// round to a negative number, which would read as "no limit".
func TestAdminMegabytesRefusesOverflow(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
		ok   bool
	}{
		{"", 0, true},
		{"0", 0, true},
		{"512", 512, true},
		{strconv.FormatInt(maxAdminMegabytes, 10), maxAdminMegabytes, true},
		{strconv.FormatInt(maxAdminMegabytes+1, 10), 0, false},
		{"9223372036854775807", 0, false},
		{"-1", 0, false},
		{"1e3", 0, false},
	} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{"quota_mb": {tc.raw}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		got, ok := adminMegabytes(req, "quota_mb")
		if got != tc.want || ok != tc.ok {
			t.Errorf("adminMegabytes(%q) = %d, %v; want %d, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
		if ok && got*1024*1024 < 0 {
			t.Errorf("adminMegabytes(%q) accepted a value that overflows", tc.raw)
		}
	}
}
