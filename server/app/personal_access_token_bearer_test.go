// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersonalAccessTokenBearerResolverPreservesCredentialCeilings(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC)
	raw := model.NewCredentialToken()
	unitID := model.NewAcademicUnitID()
	token := personalAccessTokenForTest(model.NewUserID(), at.Add(-time.Minute))
	token.Scopes = []string{string(model.ActionAcademicUnitView), string(model.ActionClassView)}
	token.AcademicUnitID = unitID
	user := &model.User{ID: token.UserID}
	persistence := &personalAccessTokenResolutionStoreFake{
		resolution: &store.PersonalAccessTokenResolution{Token: token, User: user},
	}
	resolver, err := newPersonalAccessTokenBearerResolver(
		persistence,
		PersonalAccessTokenPolicy{LastUsedUpdateInterval: 5 * time.Minute},
		personalAccessTokenDiagnosticsFake{},
	)
	if err != nil {
		t.Fatal(err)
	}

	principal, err := resolver.ResolveBearer(context.Background(), raw, at)
	if err != nil {
		t.Fatal(err)
	}
	if persistence.hash != model.HashToken(raw) || persistence.hash == raw {
		t.Fatalf("resolved hash = %q", persistence.hash)
	}
	if persistence.at != at.UnixMilli() || persistence.updateInterval != (5*time.Minute).Milliseconds() {
		t.Fatalf("resolve timing = %d/%d", persistence.at, persistence.updateInterval)
	}
	if principal.CredentialType != model.CredentialPersonalAccessToken ||
		principal.CredentialID.String() != token.ID.String() ||
		principal.UserID != user.ID ||
		principal.AcademicUnitID != unitID ||
		len(principal.CredentialScopes) != 2 {
		t.Fatalf("principal = %#v", principal)
	}

	token.AcademicUnitID = model.AcademicUnitID("")
	unscoped, err := resolver.ResolveBearer(context.Background(), raw, at)
	if err != nil {
		t.Fatal(err)
	}
	if !unscoped.AcademicUnitID.IsZero() {
		t.Fatalf("unscoped principal has academic-unit ceiling %q", unscoped.AcademicUnitID)
	}
}

type personalAccessTokenResolutionStoreFake struct {
	store.PersonalAccessTokenStore
	resolution     *store.PersonalAccessTokenResolution
	hash           string
	at             int64
	updateInterval int64
}

func (s *personalAccessTokenResolutionStoreFake) Resolve(
	_ context.Context,
	hash string,
	at int64,
	updateInterval int64,
) (*store.PersonalAccessTokenResolution, error) {
	s.hash, s.at, s.updateInterval = hash, at, updateInterval
	return s.resolution, nil
}

type personalAccessTokenDiagnosticsFake struct{}

func (personalAccessTokenDiagnosticsFake) WarnContext(context.Context, string, error) {}
