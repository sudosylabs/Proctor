// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestSessionRowConversion(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	session := &model.Session{
		ID: model.NewSessionID(), CreatedAt: now, UpdatedAt: now.Add(time.Millisecond),
		ArchivedAt: model.OptionalTimeFrom(now.Add(2 * time.Millisecond)),
		UserID:     model.NewUserID(), ClientType: model.SessionClientDesktop,
		DeviceID: "device", DeviceName: "Device", AuthenticationMethod: "oidc", AuthenticationProviderID: "0-campus.oidc",
		ExternalIdentityID:     model.NewExternalIdentityID(),
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        now,
		LastActivityAt:         now.Add(4 * time.Millisecond),
		IdleExpiresAt:          now.Add(5 * time.Millisecond),
		ExpiresAt:              now.Add(6 * time.Millisecond),
		RevokedAt:              model.OptionalTimeFrom(now.Add(7 * time.Millisecond)),
		RevocationReason:       "reason",
	}
	row := newSessionRow(session)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != session.ID ||
		!got.CreatedAt.Equal(session.CreatedAt) ||
		!got.UpdatedAt.Equal(session.UpdatedAt) ||
		got.ArchivedAt.Millis() != session.ArchivedAt.Millis() ||
		got.UserID != session.UserID ||
		got.DeviceID != session.DeviceID || got.AuthenticationProviderID != session.AuthenticationProviderID || got.ExternalIdentityID != session.ExternalIdentityID ||
		!got.AuthenticatedAt.Equal(session.AuthenticatedAt) ||
		got.RevokedAt.Millis() != session.RevokedAt.Millis() ||
		got.RevocationReason != session.RevocationReason {
		t.Fatalf("row.model() = %#v, want %#v", got, session)
	}
}

func TestSessionCredentialRowConversion(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000).UTC()
	parent := model.NewSessionCredentialID()
	replaced := model.NewSessionCredentialID()
	credential := &model.SessionCredential{
		ID: model.NewSessionCredentialID(), CreatedAt: now, UpdatedAt: now.Add(time.Millisecond),
		ArchivedAt: model.OptionalTimeFrom(now.Add(2 * time.Millisecond)),
		SessionID:  model.NewSessionID(), Kind: model.SessionCredentialRefresh,
		TokenHash: model.HashToken("token"), FamilyID: model.NewId(),
		ParentID: parent, ReplacedByID: replaced,
		ExpiresAt: now.Add(10 * time.Millisecond),
		UsedAt:    model.OptionalTimeFrom(now.Add(4 * time.Millisecond)),
		RevokedAt: model.OptionalTimeFrom(now.Add(5 * time.Millisecond)),
	}
	row := newSessionCredentialRow(credential)
	got, err := row.model()
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != credential.ID ||
		got.SessionID != credential.SessionID ||
		got.FamilyID != credential.FamilyID ||
		got.ParentID != credential.ParentID ||
		got.ReplacedByID != credential.ReplacedByID ||
		!got.ExpiresAt.Equal(credential.ExpiresAt) ||
		got.UsedAt.Millis() != credential.UsedAt.Millis() ||
		got.RevokedAt.Millis() != credential.RevokedAt.Millis() {
		t.Fatalf("row.model() = %#v, want %#v", got, credential)
	}
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
			assertPersistedSessionStateError(t, err, "session", test.field)
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
			assertPersistedSessionStateError(t, err, "session_credential", test.field)
			if strings.Contains(err.Error(), "bad") {
				t.Fatalf("persisted-state error exposed raw value: %v", err)
			}
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
		TokenHash: model.HashToken("credential"), FamilyID: model.NewId(),
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

func assertPersistedSessionStateError(t *testing.T, err error, entity, field string) {
	t.Helper()
	var persisted *persistedStateError
	if !errors.As(err, &persisted) {
		t.Fatalf("model() error = %v, want persisted-state error", err)
	}
	if persisted.Entity != entity || persisted.Field != field {
		t.Fatalf("persisted-state context = %s.%s, want %s.%s", persisted.Entity, persisted.Field, entity, field)
	}
}
