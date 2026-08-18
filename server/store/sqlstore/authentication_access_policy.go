// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
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

func requireExactExternalIdentity(ctx context.Context, executor sqlxExecutor, userID model.UserID, providerID string, identityID model.ExternalIdentityID) error {
	if providerID == "" {
		if identityID.IsZero() {
			return nil
		}
		return store.NewErrInvalidInput("session", "external_identity_id", identityID.String())
	}
	if !identityID.IsValid() {
		return store.NewErrInvalidInput("session", "external_identity_id", identityID.String())
	}
	var found string
	err := executor.Get(ctx, &found, `SELECT id FROM external_identities WHERE id=? AND user_id=? AND provider=? AND archived_at IS NULL FOR SHARE`, identityID.String(), userID.String(), providerID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrAuthenticationMethodDisabled
	}
	if err != nil {
		return fmt.Errorf("validate session external identity: %w", err)
	}
	return nil
}

func requireCurrentLocalLogin(ctx context.Context, executor sqlxExecutor) error {
	return requireCurrentAuthenticationMethod(ctx, executor, "password", "")
}

func requireCurrentExternalProvider(ctx context.Context, executor sqlxExecutor, providerID string) error {
	_, err := currentExternalProviderAdmission(ctx, executor, providerID)
	return err
}

func currentExternalProviderAdmission(ctx context.Context, executor sqlxExecutor, providerID string) (model.ProviderAdmissionMode, error) {
	policy, err := getAccessPolicy(ctx, executor, "FOR SHARE")
	if err != nil {
		return "", err
	}
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	mode, allowed := policy.ProviderAdmissions[providerID]
	if !allowed {
		return "", store.ErrAuthenticationMethodDisabled
	}
	return mode, nil
}
