// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		t.Run(test.value, func(t *testing.T) {
			assert.Equal(t, test.valid, IsSafeRelativeURL(test.value), "IsSafeRelativeURL(%q)", test.value)
		})
	}
}

func TestExternalLoginStateLifecycle(t *testing.T) {
	state := &ExternalLoginState{
		Provider:    "Campus-CAS",
		StateHash:   HashToken(NewCredentialToken()),
		BindingHash: HashToken(NewCredentialToken()),
		ReturnTo:    "/exams",
		ClientType:  SessionClientDesktop,
		ExpiresAt:   TimeFromMillis(GetMillis() + 60_000),
	}
	state.PrepareCreate(NewExternalLoginStateID(), NowUTC())
	require.Equal(t, ExternalAuthenticationPurposeLogin, state.Purpose)
	require.Equal(t, "campus-cas", state.Provider)
	require.NoError(t, state.Validate())
	state.ClientType = SessionClientCLI
	require.Error(t, state.Validate(), "CLI external login state was accepted")
}

func TestExternalLoginStateConnectPurposeRequiresExactUser(t *testing.T) {
	state := &ExternalLoginState{
		Provider: "campus", Purpose: ExternalAuthenticationPurposeConnect,
		TargetUserID: NewUserID(), AuditEventID: NewAuditEventID().String(), StateHash: HashToken(NewCredentialToken()),
		BindingHash: HashToken(NewCredentialToken()), ReturnTo: "/account/security",
		ClientType: SessionClientWeb, ExpiresAt: TimeFromMillis(GetMillis() + 60_000),
	}
	state.PrepareCreate(NewExternalLoginStateID(), NowUTC())
	require.NoError(t, state.Validate())
	state.TargetUserID = ""
	require.Error(t, state.Validate(), "connect state without target User was accepted")
}

func TestExternalLoginStateInvitationAdmissionBindsOnlyInvitation(t *testing.T) {
	at := NowUTC().Truncate(time.Millisecond)
	state := &ExternalLoginState{
		Provider: "campus", Purpose: ExternalAuthenticationPurposeInvitationAdmission,
		InvitationID: NewInvitationID(), StateHash: HashToken(NewCredentialToken()),
		BindingHash: HashToken(NewCredentialToken()), ReturnTo: "/join",
		ClientType: SessionClientWeb, ExpiresAt: at.Add(time.Minute),
	}
	state.PrepareCreate(NewExternalLoginStateID(), at)
	require.NoError(t, state.Validate())
	state.TargetUserID = NewUserID()
	require.Error(t, state.Validate(), "invitation admission accepted a target User")
}

func TestExternalAuthenticationCallbackRequiresOneBoundedValue(t *testing.T) {
	callback := ExternalAuthenticationCallback{Values: map[string][]string{
		"state": {"one"},
	}}
	value, err := callback.SingleValue("state", 16)
	require.NoError(t, err, "SingleValue()")
	require.Equal(t, "one", value, "SingleValue()")
	callback.Values["state"] = []string{"one", "two"}
	_, err = callback.SingleValue("state", 16)
	require.Error(t, err, "duplicate callback value was accepted")
	callback.Values["state"] = []string{"value-that-is-too-long"}
	_, err = callback.SingleValue("state", 4)
	require.Error(t, err, "oversized callback value was accepted")
}
