// SPDX-License-Identifier: AGPL-3.0-or-later
package metadata

import "testing"

func TestCaptureDatePreservesCameraWallTimeAndRejectsMalformedData(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"taken":"29 February 2024 at 23:59"}`, "2024-02-29T23:59:00"},
		{`{"taken":"1 January 0001 at 00:00"}`, "0001-01-01T00:00:00"},
		{`{"taken":"31 December 9999 at 23:59"}`, "9999-12-31T23:59:00"},
		{`{"taken":"29 February 2023 at 10:00"}`, ""},
		{`{"taken":"1 January 0000 at 10:00"}`, ""},
		{`{"taken":"<script>alert(1)</script>"}`, ""},
		{`{"taken":42}`, ""}, {`{broken`, ""}, {`null`, ""}, {``, ""},
	} {
		if got := CaptureDate(tc.raw); got != tc.want {
			t.Errorf("%s: %q want %q", tc.raw, got, tc.want)
		}
	}
}
