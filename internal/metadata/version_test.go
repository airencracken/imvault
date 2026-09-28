// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import "testing"

func TestMergeDetailsPreservesFieldsAcrossPartialReads(t *testing.T) {
	lat, lon := 0.0, -18.25
	fresh := &Details{Latitude: &lat, Longitude: &lon}
	for _, stored := range []string{"", "{}", "null", "broken", `{"camera":"Old Camera","artist":"Photographer"}`} {
		merged := MergeDetails(stored, fresh)
		details := DecodeDetails(merged)
		if details.Location() != "0.00000, -18.25000" {
			t.Fatalf("lost coordinates: %s", merged)
		}
		if old := DecodeDetails(stored); old != nil && (details.Camera != old.Camera || details.Artist != old.Artist) {
			t.Fatal("partial read erased known fields")
		}
		if MergeDetails(merged, fresh) != merged || MergeDetails(merged, nil) != merged {
			t.Fatal("merge is not idempotent or empty read erased details")
		}
	}
}
