// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestCurrentAuthenticationAccessPolicyReturnsExactExternalProviderAdmission(t *testing.T) {
	policy := model.NewInitialAccessPolicy(model.NewAccessPolicyID(), time.UnixMilli(1_000))
	policy.ProviderAdmissions["campus"] = model.ProviderAdmissionInvitationRequired
	reader, err := newCurrentAuthenticationAccessPolicy(&accessPolicyStoreFake{
		snapshot: &store.AccessPolicySnapshot{Policy: policy},
	})
	if err != nil {
		t.Fatal(err)
	}

	admission, err := reader.ExternalProviderAdmission(context.Background(), " CAMPUS ")
	if err != nil || admission.Mode != model.ProviderAdmissionInvitationRequired || !admission.InvitationAdmissionEnabled {
		t.Fatalf("ExternalProviderAdmission() = %#v, %v", admission, err)
	}
	admission, err = reader.ExternalProviderAdmission(context.Background(), "removed")
	if err != nil || admission.Mode != "" || !admission.InvitationAdmissionEnabled {
		t.Fatalf("ExternalProviderAdmission(removed) = %#v, %v", admission, err)
	}
}

type authenticationAccessPolicyFake struct {
	local                       bool
	providers                   map[string]bool
	providerModes               map[string]model.ProviderAdmissionMode
	invitationAdmissionDisabled bool
	err                         error
}

func allowAllAuthenticationAccessPolicy() authenticationAccessPolicy {
	return authenticationAccessPolicyFake{local: true, providers: map[string]bool{"campus": true, "campus-a": true, "campus-b": true}}
}

func (p authenticationAccessPolicyFake) AllowsLocalLogin(context.Context) (bool, error) {
	return p.local, p.err
}

func (p authenticationAccessPolicyFake) ExternalProviderAdmission(_ context.Context, providerID string) (externalProviderAdmissionPolicy, error) {
	result := externalProviderAdmissionPolicy{InvitationAdmissionEnabled: !p.invitationAdmissionDisabled}
	if mode := p.providerModes[providerID]; mode != "" {
		result.Mode = mode
		return result, p.err
	}
	if p.providers[providerID] {
		result.Mode = model.ProviderAdmissionAutoProvision
		return result, p.err
	}
	return result, p.err
}

func (p authenticationAccessPolicyFake) AvailableExternalProviders(_ context.Context, configured []model.ExternalAuthenticationProvider) ([]model.ExternalAuthenticationProvider, error) {
	if p.err != nil {
		return nil, p.err
	}
	available := make([]model.ExternalAuthenticationProvider, 0, len(configured))
	for _, provider := range configured {
		if p.providers[provider.Id] {
			available = append(available, provider)
		}
	}
	return available, nil
}
