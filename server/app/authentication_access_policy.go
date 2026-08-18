// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// authenticationAccessPolicy is the consumer-owned view of current access
// policy needed by authentication entry points. Terminal persistence
// operations independently recheck the authoritative policy in their
// transaction.
type authenticationAccessPolicy interface {
	AllowsLocalLogin(context.Context) (bool, error)
	ExternalProviderAdmission(context.Context, string) (externalProviderAdmissionPolicy, error)
	AvailableExternalProviders(context.Context, []model.ExternalAuthenticationProvider) ([]model.ExternalAuthenticationProvider, error)
}

type externalProviderAdmissionPolicy struct {
	Mode                       model.ProviderAdmissionMode
	InvitationAdmissionEnabled bool
}

type currentAuthenticationAccessPolicy struct {
	policies store.AccessPolicyStore
}

func newCurrentAuthenticationAccessPolicy(policies store.AccessPolicyStore) (*currentAuthenticationAccessPolicy, error) {
	if policies == nil {
		return nil, errors.New("authentication access policy store is required")
	}
	return &currentAuthenticationAccessPolicy{policies: policies}, nil
}

func (p *currentAuthenticationAccessPolicy) AllowsLocalLogin(ctx context.Context) (bool, error) {
	policy, err := p.current(ctx)
	if err != nil {
		return false, err
	}
	return policy.LocalLoginEnabled, nil
}

func (p *currentAuthenticationAccessPolicy) ExternalProviderAdmission(ctx context.Context, providerID string) (externalProviderAdmissionPolicy, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID == "" {
		return externalProviderAdmissionPolicy{}, nil
	}
	policy, err := p.current(ctx)
	if err != nil {
		return externalProviderAdmissionPolicy{}, err
	}
	return externalProviderAdmissionPolicy{
		Mode:                       policy.ProviderAdmissions[providerID],
		InvitationAdmissionEnabled: policy.InvitationAdmissionEnabled,
	}, nil
}

func (p *currentAuthenticationAccessPolicy) AllowsDesktopAuthorization(ctx context.Context, method, providerID string) (bool, error) {
	policy, err := p.current(ctx)
	if err != nil || !policy.DesktopAuthorizationEnabled {
		return false, err
	}
	if method == "password" {
		return providerID == "" && policy.LocalLoginEnabled, nil
	}
	if providerID == "" {
		return false, nil
	}
	_, allowed := policy.ProviderAdmissions[strings.ToLower(strings.TrimSpace(providerID))]
	return allowed, nil
}

func (p *currentAuthenticationAccessPolicy) AvailableExternalProviders(
	ctx context.Context,
	configured []model.ExternalAuthenticationProvider,
) ([]model.ExternalAuthenticationProvider, error) {
	policy, err := p.current(ctx)
	if err != nil {
		return nil, err
	}
	available := make([]model.ExternalAuthenticationProvider, 0, len(configured))
	for _, provider := range configured {
		if _, allowed := policy.ProviderAdmissions[provider.Id]; allowed {
			available = append(available, provider)
		}
	}
	return available, nil
}

func (p *currentAuthenticationAccessPolicy) current(ctx context.Context) (*model.AccessPolicy, error) {
	snapshot, err := p.policies.Get(ctx, 0)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Policy == nil || snapshot.Policy.Validate() != nil {
		return nil, errors.New("current authentication access policy is invalid")
	}
	return snapshot.Policy, nil
}
