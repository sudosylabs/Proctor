// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/model"
)

type authenticationAccessPolicyFake struct {
	local     bool
	providers map[string]bool
	err       error
}

func allowAllAuthenticationAccessPolicy() authenticationAccessPolicy {
	return authenticationAccessPolicyFake{local: true, providers: map[string]bool{"campus": true, "campus-a": true, "campus-b": true}}
}

func (p authenticationAccessPolicyFake) AllowsLocalLogin(context.Context) (bool, error) {
	return p.local, p.err
}

func (p authenticationAccessPolicyFake) AllowsExternalProvider(_ context.Context, providerID string) (bool, error) {
	return p.providers[providerID], p.err
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
