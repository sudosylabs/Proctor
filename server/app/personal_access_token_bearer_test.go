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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPersonalAccessTokenBearerResolverPreservesCredentialCeilings(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 12, 13, 0, 0, 123456789, time.FixedZone("test", 2*60*60))
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
	require.NoError(t, err)

	principal, err := resolver.ResolveBearer(context.Background(), raw, at)
	require.NoError(t, err)
	require.Equal(t, model.HashToken(raw), persistence.hash)
	require.NotEqual(t, raw, persistence.hash)
	require.True(t, persistence.at.Equal(model.TimeUTC(at)), "resolve timing = %s", persistence.at)
	require.Same(t, time.UTC, persistence.at.Location(), "resolve timing = %s", persistence.at)
	require.Equal(t, 5*time.Minute, persistence.updateInterval, "resolve timing = %s", persistence.at)
	require.Equal(t, model.CredentialPersonalAccessToken, principal.CredentialType)
	require.Equal(t, token.ID.String(), principal.CredentialID.String())
	require.Equal(t, user.ID, principal.UserID)
	require.Equal(t, unitID, principal.AcademicUnitID)
	require.Len(t, principal.CredentialScopes, 2)

	token.AcademicUnitID = model.AcademicUnitID("")
	unscoped, err := resolver.ResolveBearer(context.Background(), raw, at)
	require.NoError(t, err)
	require.True(t, unscoped.AcademicUnitID.IsZero())
}

type personalAccessTokenResolutionStoreFake struct {
	store.PersonalAccessTokenStore
	resolution     *store.PersonalAccessTokenResolution
	hash           string
	at             time.Time
	updateInterval time.Duration
}

func (s *personalAccessTokenResolutionStoreFake) Resolve(
	_ context.Context,
	hash string,
	at time.Time,
	updateInterval time.Duration,
) (*store.PersonalAccessTokenResolution, error) {
	s.hash, s.at, s.updateInterval = hash, at, updateInterval
	return s.resolution, nil
}

type personalAccessTokenDiagnosticsFake struct{}

func (personalAccessTokenDiagnosticsFake) WarnContext(context.Context, string, error) {}
