// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type bootstrapStatusStoreFake struct {
	state *model.InstallationState
	err   error
}

func (s bootstrapStatusStoreFake) Get(context.Context) (*model.InstallationState, error) {
	return s.state, s.err
}

func TestResolveBootstrapProtectionRequiresConfiguredSecretForPristineProduction(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Authentication.Bootstrap.DevelopmentMode = false
	cfg.Server.ListenAddress = "0.0.0.0:8065"
	cfg.Server.PublicURL = "https://proctor.example.edu"
	_, err := resolveBootstrapProtection(context.Background(), cfg, bootstrapStatusStoreFake{
		err: store.NewErrNotFound("installation", "singleton"),
	}, &bytes.Buffer{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bootstrap secret is required")
}

func TestResolveBootstrapProtectionGeneratesAndDisplaysLoopbackDevelopmentSecretOnce(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	policy, err := resolveBootstrapProtection(context.Background(), config.Default(), bootstrapStatusStoreFake{
		err: store.NewErrNotFound("installation", "singleton"),
	}, &output)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(policy.Secret), 43, "policy/output = %#v", policy)
	require.Contains(t, output.String(), policy.Secret, "policy/output = %#v", policy)
	require.Equal(t, 1, strings.Count(output.String(), policy.Secret), "policy/output = %#v", policy)
}

func TestResolveBootstrapProtectionDoesNotRequireReplacementSecretAfterInitialization(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Authentication.Bootstrap.DevelopmentMode = false
	policy, err := resolveBootstrapProtection(context.Background(), cfg, bootstrapStatusStoreFake{
		state: &model.InstallationState{InitializedAt: model.NowUTC(), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID()},
	}, &bytes.Buffer{})
	require.NoError(t, err, "policy=%#v error", policy)
	require.Empty(t, policy.Secret, "policy=%#v error", policy)
}

func TestResolveBootstrapProtectionDoesNotGenerateDevelopmentSecretAfterInitialization(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	policy, err := resolveBootstrapProtection(context.Background(), config.Default(), bootstrapStatusStoreFake{
		state: &model.InstallationState{InitializedAt: model.NowUTC(), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID()},
	}, &output)
	require.NoError(t, err, "policy=%#v output=%q error", policy, output.String())
	require.Empty(t, policy.Secret, "policy=%#v output=%q error", policy, output.String())
	require.Equal(t, 0, output.Len(), "policy=%#v output=%q error", policy, output.String())
}

func TestResolveBootstrapProtectionWithoutInstallationStateUsesConfiguredSecret(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Authentication.Bootstrap.Secret = "operator-provided-bootstrap-secret-32-bytes"
	var output bytes.Buffer
	policy, err := resolveBootstrapProtection(context.Background(), cfg, nil, &output)
	require.NoError(t, err, "resolveBootstrapProtection(nil) = %#v, %v, output %q", policy, err, output.String())
	require.Equal(t, cfg.Authentication.Bootstrap.Secret, policy.Secret, "resolveBootstrapProtection(nil) = %#v, %v, output %q", policy, err, output.String())
	require.Equal(t, 0, output.Len(), "resolveBootstrapProtection(nil) = %#v, %v, output %q", policy, err, output.String())
	cfg.Authentication.Bootstrap.Secret = ""
	policy, err = resolveBootstrapProtection(context.Background(), cfg, nil, &output)
	require.NoError(t, err, "resolveBootstrapProtection(nil, no secret) = %#v, %v, output %q", policy, err, output.String())
	require.Empty(t, policy.Secret, "resolveBootstrapProtection(nil, no secret) = %#v, %v, output %q", policy, err, output.String())
	require.Equal(t, 0, output.Len(), "resolveBootstrapProtection(nil, no secret) = %#v, %v, output %q", policy, err, output.String())
}
