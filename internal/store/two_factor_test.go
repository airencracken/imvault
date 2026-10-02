// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
)

func TestEnablingTOTPNeedsTheSecretThatWasConfirmed(t *testing.T) {
	s, ctx := newTestStore(t)
	user := mustUser(t, s, ctx, "enrolling")
	if err := s.BeginTOTP(ctx, user.ID, "first-secret"); err != nil {
		t.Fatal(err)
	}
	// Another tab starts over between the code check and the enable.
	if err := s.BeginTOTP(ctx, user.ID, "second-secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, user.ID, "first-secret", 100); !errors.Is(err, ErrTOTPChanged) {
		t.Fatalf("enabled a secret that was no longer pending: %v", err)
	}
	after, err := s.UserByID(ctx, user.ID)
	if err != nil || after.TOTPEnabled {
		t.Fatalf("an unconfirmed secret was switched on: %+v, %v", after, err)
	}
	if err := s.EnableTOTP(ctx, user.ID, "", 100); !errors.Is(err, ErrTOTPChanged) {
		t.Fatalf("an empty secret enabled the second factor: %v", err)
	}
	if err := s.EnableTOTP(ctx, user.ID, "second-secret", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, user.ID, "second-secret", 101); !errors.Is(err, ErrTOTPChanged) {
		t.Fatalf("enabling twice was not refused: %v", err)
	}
}

func TestTheEnrolmentCodeCannotBeReplayedToSignIn(t *testing.T) {
	s, ctx := newTestStore(t)
	user := mustUser(t, s, ctx, "replayed")
	if err := s.BeginTOTP(ctx, user.ID, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(ctx, user.ID, "secret", 500); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.AcceptTOTPStep(ctx, user.ID, 500); err != nil || ok {
		t.Fatalf("the confirming code's step was accepted again: %v, %v", ok, err)
	}
	if ok, err := s.AcceptTOTPStep(ctx, user.ID, 501); err != nil || !ok {
		t.Fatalf("the next step was refused: %v, %v", ok, err)
	}
}
