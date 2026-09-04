// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestRequireStrongRecentSessionPreservesAssuranceErrors(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0).UTC()
	recent := model.Principal{
		UserID: model.NewUserID(), SessionID: model.NewSessionID(),
		CredentialID:   model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationMultiFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: now.Add(-time.Minute), MFACompletedAt: model.OptionalTimeFrom(now.Add(-time.Minute)),
	}
	pat := model.Principal{
		UserID: model.NewUserID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialPersonalAccessToken, AuthenticationMethod: "personal_access_token",
		ClientType: model.SessionClientCLI, CredentialScopes: []string{string(model.ActionMailManage)},
	}
	tests := []struct {
		name      string
		principal model.Principal
		wantCode  string
	}{
		{name: "invalid principal", principal: model.Principal{}, wantCode: "authentication.invalid_token"},
		{name: "personal access token", principal: pat, wantCode: "authentication.invalid_token"},
		{name: "single factor", principal: func() model.Principal {
			value := recent
			value.AuthenticationStrength = model.AuthenticationSingleFactor
			value.MFACompletedAt = model.OptionalTime{}
			return value
		}(), wantCode: "authentication.strong_required"},
		{name: "stale", principal: func() model.Principal {
			value := recent
			value.AuthenticatedAt = now.Add(-time.Hour)
			value.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Hour))
			return value
		}(), wantCode: "authentication.reauthentication_required"},
		{name: "recent strong session", principal: recent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := requireStrongRecentSession(test.principal, now, 15*time.Minute)
			if test.wantCode == "" {
				if err != nil {
					t.Fatalf("requireStrongRecentSession() error = %v", err)
				}
				return
			}
			if !Is(err, test.wantCode) {
				t.Fatalf("requireStrongRecentSession() error = %v, want %s", err, test.wantCode)
			}
		})
	}
}
