// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"strings"

	"github.com/sudosylabs/proctor/server/store"
)

func requireCurrentAuthenticationMethod(
	ctx context.Context,
	executor sqlxExecutor,
	method string,
	providerID string,
) error {
	policy, err := getAccessPolicy(ctx, executor, "FOR SHARE")
	if err != nil {
		return err
	}
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID != "" {
		if _, allowed := policy.ProviderAdmissions[providerID]; allowed {
			return nil
		}
		return store.ErrAuthenticationMethodDisabled
	}
	if method == "password" {
		if policy.LocalLoginEnabled {
			return nil
		}
		return store.ErrAuthenticationMethodDisabled
	}
	return store.NewErrInvalidInput("session", "authentication_provider_id", providerID)
}

func requireCurrentLocalLogin(ctx context.Context, executor sqlxExecutor) error {
	return requireCurrentAuthenticationMethod(ctx, executor, "password", "")
}

func requireCurrentExternalProvider(ctx context.Context, executor sqlxExecutor, providerID string) error {
	policy, err := getAccessPolicy(ctx, executor, "FOR SHARE")
	if err != nil {
		return err
	}
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if _, allowed := policy.ProviderAdmissions[providerID]; !allowed {
		return store.ErrAuthenticationMethodDisabled
	}
	return nil
}
