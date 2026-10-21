// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
)

func TestSessionRowConversion(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	session := &model.Session{
		ID: model.NewSessionID(), CreatedAt: now, UpdatedAt: now.Add(time.Millisecond),
		ArchivedAt: model.OptionalTimeFrom(now.Add(2 * time.Millisecond)),
		UserID:     model.NewUserID(), ClientType: model.SessionClientDesktop,
		DesktopRegistrationID: model.NewDesktopRegistrationID(),
		DPoPKeyThumbprint:     base64.RawURLEncoding.EncodeToString(make([]byte, 32)), DesktopRelease: "0.1.0",
		DesktopBuildID: "test-build", DesktopPlatform: model.DesktopPlatformDarwin,
		DesktopArchitecture: model.DesktopArchitectureARM64, DesktopRealtimeProtocol: 1,
		DeviceID: "device", DeviceName: "Device", AuthenticationMethod: "oidc", AuthenticationProviderID: "0-campus.oidc",
		ExternalIdentityID:     model.NewExternalIdentityID(),
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        now,
		LastActivityAt:         now.Add(4 * time.Millisecond),
		IdleExpiresAt:          now.Add(5 * time.Millisecond),
		ExpiresAt:              now.Add(6 * time.Millisecond),
		RevokedAt:              model.OptionalTimeFrom(now.Add(7 * time.Millisecond)),
		RevocationReason:       model.SessionRevocationUserSession,
	}
	row := newSessionRow(session)
	got, err := row.model()
	require.NoError(t, err)
	require.Equal(t, session.ID, got.ID, "row.model() = %#v, want %#v", got, session)
	require.True(t, got.CreatedAt.Equal(session.CreatedAt), "row.model() = %#v, want %#v", got, session)
	require.True(t, got.UpdatedAt.Equal(session.UpdatedAt), "row.model() = %#v, want %#v", got, session)
	require.Equal(t, session.ArchivedAt.Millis(), got.ArchivedAt.Millis(), "row.model() = %#v, want %#v", got, session)
	require.Equal(t, session.UserID, got.UserID, "row.model() = %#v, want %#v", got, session)
	require.Equal(t, session.DeviceID, got.DeviceID, "row.model() = %#v, want %#v", got, session)
	require.Equal(t, session.AuthenticationProviderID, got.AuthenticationProviderID, "row.model() = %#v, want %#v", got, session)
	require.Equal(t, session.ExternalIdentityID, got.ExternalIdentityID, "row.model() = %#v, want %#v", got, session)
	require.True(t, got.AuthenticatedAt.Equal(session.AuthenticatedAt), "row.model() = %#v, want %#v", got, session)
	require.Equal(t, session.RevokedAt.Millis(), got.RevokedAt.Millis(), "row.model() = %#v, want %#v", got, session)
	require.Equal(t, session.RevocationReason, got.RevocationReason, "row.model() = %#v, want %#v", got, session)
}

func TestSessionCredentialRowConversion(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	parent := model.NewSessionCredentialID()
	replaced := model.NewSessionCredentialID()
	credential := &model.SessionCredential{
		ID: model.NewSessionCredentialID(), CreatedAt: now, UpdatedAt: now.Add(time.Millisecond),
		ArchivedAt: model.OptionalTimeFrom(now.Add(2 * time.Millisecond)),
		SessionID:  model.NewSessionID(), Kind: model.SessionCredentialRefresh,
		TokenHash: model.HashToken("token"), FamilyID: model.NewID(),
		ParentID: parent, ReplacedByID: replaced,
		ExpiresAt: now.Add(10 * time.Millisecond),
		UsedAt:    model.OptionalTimeFrom(now.Add(4 * time.Millisecond)),
		RevokedAt: model.OptionalTimeFrom(now.Add(5 * time.Millisecond)),
	}
	row := newSessionCredentialRow(credential)
	got, err := row.model()
	require.NoError(t, err)
	require.Equal(t, credential.ID, got.ID, "row.model() = %#v, want %#v", got, credential)
	require.Equal(t, credential.SessionID, got.SessionID, "row.model() = %#v, want %#v", got, credential)
	require.Equal(t, credential.FamilyID, got.FamilyID, "row.model() = %#v, want %#v", got, credential)
	require.Equal(t, credential.ParentID, got.ParentID, "row.model() = %#v, want %#v", got, credential)
	require.Equal(t, credential.ReplacedByID, got.ReplacedByID, "row.model() = %#v, want %#v", got, credential)
	require.True(t, got.ExpiresAt.Equal(credential.ExpiresAt), "row.model() = %#v, want %#v", got, credential)
	require.Equal(t, credential.UsedAt.Millis(), got.UsedAt.Millis(), "row.model() = %#v, want %#v", got, credential)
	require.Equal(t, credential.RevokedAt.Millis(), got.RevokedAt.Millis(), "row.model() = %#v, want %#v", got, credential)
}

func TestSessionRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := newSessionRow(validPersistedSession())
	tests := []struct {
		name  string
		row   sessionRow
		field string
	}{
		{name: "session id", row: replaceSessionRow(valid, func(row *sessionRow) { row.ID = "bad" }), field: "id"},
		{name: "user id", row: replaceSessionRow(valid, func(row *sessionRow) { row.UserID = "bad" }), field: "user_id"},
		{name: "domain state", row: replaceSessionRow(valid, func(row *sessionRow) { row.ClientType = "unknown" }), field: "client_type"},
		{name: "provider identity", row: replaceSessionRow(valid, func(row *sessionRow) { row.AuthenticationProviderID = ".bad" }), field: "authentication_provider_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			requirePersistedStateError(t, err, "session", test.field)
		})
	}
}

func TestSessionCredentialRowRehydrationRejectsInvalidPersistedState(t *testing.T) {
	t.Parallel()

	valid := newSessionCredentialRow(validPersistedSessionCredential())
	tests := []struct {
		name  string
		row   sessionCredentialRow
		field string
	}{
		{name: "credential id", row: replaceSessionCredentialRow(valid, func(row *sessionCredentialRow) { row.ID = "bad" }), field: "id"},
		{name: "session id", row: replaceSessionCredentialRow(valid, func(row *sessionCredentialRow) { row.SessionID = "bad" }), field: "session_id"},
		{name: "family id", row: replaceSessionCredentialRow(valid, func(row *sessionCredentialRow) { row.FamilyID.String = "bad" }), field: "family_id"},
		{name: "parent id", row: replaceSessionCredentialRow(valid, func(row *sessionCredentialRow) {
			row.ParentID.String = "bad"
			row.ParentID.Valid = true
		}), field: "parent_id"},
		{name: "replacement id", row: replaceSessionCredentialRow(valid, func(row *sessionCredentialRow) {
			row.ReplacedByID.String = "bad"
			row.ReplacedByID.Valid = true
		}), field: "replaced_by_id"},
		{name: "non-id domain state", row: replaceSessionCredentialRow(valid, func(row *sessionCredentialRow) { row.TokenHash = "bad" }), field: "token_hash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.row.model()
			requirePersistedStateError(t, err, "session_credential", test.field)
			require.NotContains(t, err.Error(), "bad")
		})
	}
}

func validPersistedSession() *model.Session {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	return &model.Session{
		ID: model.NewSessionID(), CreatedAt: now, UpdatedAt: now,
		UserID: model.NewUserID(), ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt: now, LastActivityAt: now,
		IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour),
	}
}

func validPersistedSessionCredential() *model.SessionCredential {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	return &model.SessionCredential{
		ID: model.NewSessionCredentialID(), CreatedAt: now, UpdatedAt: now,
		SessionID: model.NewSessionID(), Kind: model.SessionCredentialRefresh,
		TokenHash: model.HashToken("credential"), FamilyID: model.NewID(),
		ExpiresAt: now.Add(time.Hour),
	}
}

func replaceSessionRow(row sessionRow, replace func(*sessionRow)) sessionRow {
	replace(&row)
	return row
}

func replaceSessionCredentialRow(row sessionCredentialRow, replace func(*sessionCredentialRow)) sessionCredentialRow {
	replace(&row)
	return row
}
