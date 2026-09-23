// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"testing"
)

func TestReadConfirmedAdminPassword(t *testing.T) {
	for _, tc := range []struct {
		name      string
		input     [][]byte
		want      string
		wantError string
	}{
		{name: "matching", input: [][]byte{[]byte("a long password"), []byte("a long password")}, want: "a long password"},
		{name: "mismatch", input: [][]byte{[]byte("first password"), []byte("second password")}, wantError: "passwords do not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			index := 0
			got, err := readConfirmedAdminPassword(&output, func() ([]byte, error) {
				password := tc.input[index]
				index++
				return password, nil
			})
			if tc.wantError != "" {
				if err == nil || err.Error() != tc.wantError {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("password = %q, %v; want %q", got, err, tc.want)
			}
			if want := "Administrator password: \nConfirm administrator password: \n"; output.String() != want {
				t.Fatalf("prompts = %q, want %q", output.String(), want)
			}
		})
	}
}

func TestReadConfirmedAdminPasswordPropagatesReadError(t *testing.T) {
	want := errors.New("terminal read failed")
	var output bytes.Buffer
	_, err := readConfirmedAdminPassword(&output, func() ([]byte, error) { return nil, want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
