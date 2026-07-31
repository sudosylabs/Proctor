// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestSessionRowConversion(t *testing.T) {
	session := &model.Session{
		Id: model.NewId(), CreateAt: 1, UpdateAt: 2, DeleteAt: 3,
		UserId: model.NewId(), ClientType: model.SessionClientDesktop,
		DeviceId: "device", DeviceName: "Device", AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor,
		AuthenticatedAt:        4, LastActivityAt: 5, IdleExpiresAt: 6, ExpiresAt: 7,
		RevokedAt: 8, RevocationReason: "reason",
	}
	row := newSessionRow(session)
	if got := row.model(); *got != *session {
		t.Fatalf("row.model() = %#v, want %#v", got, session)
	}
}

func TestSessionCredentialRowConversion(t *testing.T) {
	credential := &model.SessionCredential{
		Id: model.NewId(), CreateAt: 1, UpdateAt: 2, DeleteAt: 3,
		SessionId: model.NewId(), Kind: model.SessionCredentialRefresh,
		TokenHash: model.HashToken("token"), FamilyId: model.NewId(),
		ParentId: model.NewId(), ReplacedById: model.NewId(),
		ExpiresAt: 4, UsedAt: 5, RevokedAt: 6,
	}
	row := newSessionCredentialRow(credential)
	if got := row.model(); *got != *credential {
		t.Fatalf("row.model() = %#v, want %#v", got, credential)
	}
}
