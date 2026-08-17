// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"bytes"
	"context"
	"strings"
	"testing"

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
	if err == nil || !strings.Contains(err.Error(), "bootstrap secret is required") {
		t.Fatalf("resolveBootstrapProtection() error = %v", err)
	}
}

func TestResolveBootstrapProtectionGeneratesAndDisplaysLoopbackDevelopmentSecretOnce(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	policy, err := resolveBootstrapProtection(context.Background(), config.Default(), bootstrapStatusStoreFake{
		err: store.NewErrNotFound("installation", "singleton"),
	}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Secret) < 43 || !strings.Contains(output.String(), policy.Secret) ||
		strings.Count(output.String(), policy.Secret) != 1 {
		t.Fatalf("policy/output = %#v / %q", policy, output.String())
	}
}

func TestResolveBootstrapProtectionDoesNotRequireReplacementSecretAfterInitialization(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Authentication.Bootstrap.DevelopmentMode = false
	policy, err := resolveBootstrapProtection(context.Background(), cfg, bootstrapStatusStoreFake{
		state: &model.InstallationState{InitializedAt: model.NowUTC(), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID()},
	}, &bytes.Buffer{})
	if err != nil || policy.Secret != "" {
		t.Fatalf("policy=%#v error=%v", policy, err)
	}
}

func TestResolveBootstrapProtectionDoesNotGenerateDevelopmentSecretAfterInitialization(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	policy, err := resolveBootstrapProtection(context.Background(), config.Default(), bootstrapStatusStoreFake{
		state: &model.InstallationState{InitializedAt: model.NowUTC(), InstitutionID: model.NewInstitutionID(), AdministratorUserID: model.NewUserID()},
	}, &output)
	if err != nil || policy.Secret != "" || output.Len() != 0 {
		t.Fatalf("policy=%#v output=%q error=%v", policy, output.String(), err)
	}
}
