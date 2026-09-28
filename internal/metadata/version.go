// SPDX-License-Identifier: AGPL-3.0-or-later

package metadata

import "encoding/json"

// Version identifies the extraction that produced cached details. Bump it when
// a parser fix should also reach existing originals.
const Version = 1

// MergeDetails preserves previously recovered fields when a partial read finds
// only some of them. Original bytes, not user edits, are the source of both.
func MergeDetails(stored string, fresh *Details) string {
	encoded := fresh.Encode()
	if encoded == "" {
		return stored
	}
	var old, fields map[string]json.RawMessage
	if json.Unmarshal([]byte(stored), &old) != nil || old == nil {
		return encoded
	}
	if json.Unmarshal([]byte(encoded), &fields) != nil {
		return stored
	}
	for key, value := range fields {
		old[key] = value
	}
	merged, err := json.Marshal(old)
	if err != nil {
		return stored
	}
	return string(merged)
}
