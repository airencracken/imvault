// SPDX-License-Identifier: AGPL-3.0-or-later
package web

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestSharedSignInRecordsOnlySuccessfulActiveAccounts(t *testing.T) {
	h := newHarnessTransport(t, nil, mailOff, false)
	user := h.seedUser("sharedactivity")
	request := httptest.NewRequest("GET", "http://example.test/auth/shared/callback", nil)
	if _, err := h.srv.sharedSignIn(httptest.NewRecorder(), request, user.ID); err != nil {
		t.Fatal(err)
	}
	report, err := h.store.Analytics(t.Context(), time.Now())
	if err != nil || report.Logins != 1 {
		t.Fatal(report, err)
	}
	h.enableTwoFactorFor(t, user.ID)
	destination, err := h.srv.sharedSignIn(httptest.NewRecorder(), request, user.ID)
	if err != nil || destination != "/login/2fa" {
		t.Fatal(destination, err)
	}
	report, err = h.store.Analytics(t.Context(), time.Now())
	if err != nil || report.Logins != 1 {
		t.Fatal("unverified second factor counted", report, err)
	}
}
