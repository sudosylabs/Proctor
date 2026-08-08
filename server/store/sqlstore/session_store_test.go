// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
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
		DeviceID: "device", DeviceName: "Device", AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        now.Add(3 * time.Millisecond),
		LastActivityAt:         now.Add(4 * time.Millisecond),
		IdleExpiresAt:          now.Add(5 * time.Millisecond),
		ExpiresAt:              now.Add(6 * time.Millisecond),
		RevokedAt:              model.OptionalTimeFrom(now.Add(7 * time.Millisecond)),
		RevocationReason:       "reason",
	}
	row := newSessionRow(session)
	got := row.model()
	if got.ID != session.ID ||
		!got.CreatedAt.Equal(session.CreatedAt) ||
		!got.UpdatedAt.Equal(session.UpdatedAt) ||
		got.ArchivedAt.Millis() != session.ArchivedAt.Millis() ||
		got.UserID != session.UserID ||
		got.DeviceID != session.DeviceID ||
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
		ExpiresAt: now.Add(3 * time.Millisecond),
		UsedAt:    model.OptionalTimeFrom(now.Add(4 * time.Millisecond)),
		RevokedAt: model.OptionalTimeFrom(now.Add(5 * time.Millisecond)),
	}
	row := newSessionCredentialRow(credential)
	got := row.model()
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
