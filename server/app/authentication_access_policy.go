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
	AllowsExternalProvider(context.Context, string) (bool, error)
	AvailableExternalProviders(context.Context, []model.ExternalAuthenticationProvider) ([]model.ExternalAuthenticationProvider, error)
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

func (p *currentAuthenticationAccessPolicy) AllowsExternalProvider(ctx context.Context, providerID string) (bool, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID == "" {
		return false, nil
	}
	policy, err := p.current(ctx)
	if err != nil {
		return false, err
	}
	_, allowed := policy.ProviderAdmissions[providerID]
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
