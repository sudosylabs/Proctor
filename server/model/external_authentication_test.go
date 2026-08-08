// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "testing"

func TestSafeRelativeURL(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"/", true},
		{"/exams?active=true", true},
		{"https://evil.example", false},
		{"//evil.example/path", false},
		{"/path#fragment", false},
		{"/path\\other", false},
		{"relative", false},
	}
	for _, test := range tests {
		if got := IsSafeRelativeURL(test.value); got != test.valid {
			t.Errorf("IsSafeRelativeURL(%q) = %v", test.value, got)
		}
	}
}

func TestExternalLoginStateLifecycle(t *testing.T) {
	state := &ExternalLoginState{
		Provider:    "Campus-CAS",
		StateHash:   HashToken(NewCredentialToken()),
		BindingHash: HashToken(NewCredentialToken()),
		ReturnTo:    "/exams",
		ClientType:  SessionClientDesktop,
		ExpiresAt: TimeFromMillis(GetMillis()+60_000),
	}
	state.PrepareCreate(NewExternalLoginStateID(), NowUTC())
	if state.Provider != "campus-cas" {
		t.Fatalf("PreSave() provider = %q", state.Provider)
	}
	if appErr := state.Validate(); appErr != nil {
		t.Fatalf("IsValid() = %v", appErr)
	}
	state.ClientType = SessionClientCLI
	if appErr := state.Validate(); appErr == nil {
		t.Fatal("CLI external login state was accepted")
	}
}

func TestExternalAuthenticationCallbackRequiresOneBoundedValue(t *testing.T) {
	callback := ExternalAuthenticationCallback{Values: map[string][]string{
		"state": {"one"},
	}}
	if value, err := callback.SingleValue("state", 16); err != nil ||
		value != "one" {
		t.Fatalf("SingleValue() = %q, %v", value, err)
	}
	callback.Values["state"] = []string{"one", "two"}
	if _, err := callback.SingleValue("state", 16); err == nil {
		t.Fatal("duplicate callback value was accepted")
	}
	callback.Values["state"] = []string{"value-that-is-too-long"}
	if _, err := callback.SingleValue("state", 4); err == nil {
		t.Fatal("oversized callback value was accepted")
	}
}
