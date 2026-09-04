//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestSessionCanonicalIDConstraintsRejectNoncanonicalValues(t *testing.T) {
	persistence := openTestStore(t)
	resetTestStore(t, persistence)
	ctx := context.Background()

	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "session-constraint", Email: "session-constraint@example.edu", DisplayName: "Session Constraint",
	})
	now := model.NowUTC()
	session, credentials, err := persistence.Session().Save(ctx, &model.Session{
		UserID: user.ID, ClientType: model.SessionClientWeb,
		AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		IdleExpiresAt: now.Add(time.Hour), ExpiresAt: now.Add(2 * time.Hour),
	}, []*model.SessionCredential{
		{Kind: model.SessionCredentialAccess, TokenHash: model.HashToken("session-constraint-access"), ExpiresAt: now.Add(30 * time.Minute)},
		{Kind: model.SessionCredentialRefresh, TokenHash: model.HashToken("session-constraint-refresh"), ExpiresAt: now.Add(2 * time.Hour)},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	var refreshID string
	for _, credential := range credentials {
		if credential.Kind == model.SessionCredentialRefresh {
			refreshID = credential.ID.String()
			break
		}
	}
	if refreshID == "" {
		t.Fatal("saved session has no refresh credential")
	}

	tests := []struct {
		name       string
		query      string
		id         string
		constraint string
	}{
		{name: "session id", query: "UPDATE sessions SET id = 'bad' WHERE id = ?", id: session.ID.String(), constraint: "sessions_id_canonical_check"},
		{name: "session user id", query: "UPDATE sessions SET user_id = 'bad' WHERE id = ?", id: session.ID.String(), constraint: "sessions_user_id_canonical_check"},
		{name: "credential id", query: "UPDATE session_credentials SET id = 'bad' WHERE id = ?", id: refreshID, constraint: "session_credentials_id_canonical_check"},
		{name: "credential session id", query: "UPDATE session_credentials SET session_id = 'bad' WHERE id = ?", id: refreshID, constraint: "session_credentials_session_id_canonical_check"},
		{name: "refresh family id", query: "UPDATE session_credentials SET family_id = 'bad' WHERE id = ?", id: refreshID, constraint: "session_credentials_family_id_canonical_check"},
		{name: "refresh parent id", query: "UPDATE session_credentials SET parent_id = 'bad' WHERE id = ?", id: refreshID, constraint: "session_credentials_parent_id_canonical_check"},
		{name: "refresh replacement id", query: "UPDATE session_credentials SET replaced_by_id = 'bad' WHERE id = ?", id: refreshID, constraint: "session_credentials_replaced_by_id_canonical_check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := persistence.GetMaster().Exec(ctx, test.query, test.id)
			var postgresErr *pq.Error
			if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != test.constraint {
				t.Fatalf("constraint error = %#v, want check violation %s", err, test.constraint)
			}
		})
	}
}
