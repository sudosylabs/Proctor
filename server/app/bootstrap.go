// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Mattermost's first-user administrator behavior was used as a product
// reference. Proctor replaces its process-local first-user check with an
// explicit, audited, PostgreSQL-serialized installation aggregate.

package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (a *App) GetInstallationStatus(
	ctx context.Context,
) (*model.InstallationStatus, *model.AppError) {
	if a.Store() == nil || a.Store().Installation() == nil {
		return nil, bootstrapUnavailableError(
			"GetInstallationStatus",
			store.NewErrNotFound("installation_store", ""),
		)
	}
	state, err := a.Store().Installation().Get(ctx)
	if err != nil {
		if store.IsNotFound(err) {
			return &model.InstallationStatus{Initialized: false}, nil
		}
		return nil, bootstrapUnavailableError("GetInstallationStatus", err)
	}
	if !state.IsValid() {
		return nil, bootstrapUnavailableError(
			"GetInstallationStatus",
			errors.New("persisted installation state is invalid"),
		)
	}
	return &model.InstallationStatus{Initialized: true}, nil
}

func (a *App) BootstrapInstallation(
	ctx context.Context,
	institution *model.Institution,
	administrator *model.User,
	password string,
	metadata model.RequestMetadata,
	source string,
) (*model.InstallationBootstrapResult, *model.AppError) {
	if institution == nil || administrator == nil {
		return nil, model.NewAppError(
			"BootstrapInstallation", "request.invalid", nil, "", http.StatusBadRequest,
		).WithSafeFields(map[string]string{"field": "bootstrap"})
	}
	if a.Store() == nil || a.Store().Installation() == nil {
		return nil, bootstrapUnavailableError(
			"BootstrapInstallation",
			store.NewErrNotFound("installation_store", ""),
		)
	}
	status, appErr := a.GetInstallationStatus(ctx)
	if appErr != nil {
		return nil, appErr
	}
	if status.Initialized {
		return nil, model.NewAppError(
			"BootstrapInstallation",
			"installation.already_initialized",
			nil,
			"",
			http.StatusConflict,
		)
	}
	if appErr := a.checkBootstrapRateLimit(ctx, source); appErr != nil {
		return nil, appErr
	}
	hash, err := a.authentication.hasher.Hash(password)
	if err != nil {
		return nil, model.NewAppError(
			"BootstrapInstallation",
			"authentication.password.invalid",
			nil,
			"",
			http.StatusBadRequest,
		).WithSafeFields(map[string]string{"field": "password"})
	}
	result, err := a.Store().Installation().Bootstrap(ctx, &store.InstallationBootstrap{
		Institution:   institution,
		Administrator: administrator,
		PasswordHash:  hash,
		Role: &model.Role{
			Name:        model.SystemAdministratorRoleName,
			DisplayName: "System Administrator",
			Description: "Protected administrator role for this Proctor installation.",
			Permissions: model.AllActions(),
			BuiltIn:     true,
		},
		RoleBinding: &model.RoleBinding{},
		AuditEvent: &model.AuditEvent{
			Action:     "installation.bootstrap",
			RequestId:  metadata.RequestId,
			NodeId:     a.Cluster().NodeID(),
			ClientType: "bootstrap",
			AuthMethod: "bootstrap",
			IPAddress:  metadata.IPAddress,
			UserAgent:  metadata.UserAgent,
		},
	})
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) {
			return nil, model.NewAppError(
				"BootstrapInstallation",
				"installation.already_initialized",
				nil,
				"",
				http.StatusConflict,
			).Wrap(err)
		}
		return nil, bootstrapUnavailableError("BootstrapInstallation", err)
	}
	return result, nil
}

func (a *App) checkBootstrapRateLimit(
	ctx context.Context,
	source string,
) *model.AppError {
	settings := a.Config().Authentication.LoginRateLimit
	key := "authentication/bootstrap/source/" + digestCacheKey(normalizeLoginSource(source))
	count, err := a.Cache().Add(ctx, key, 1, settings.Window.Duration)
	if err != nil {
		return rateLimitUnavailableError("BootstrapInstallation.rate_limit", err)
	}
	if count > int64(settings.MaximumSourceAttempts) {
		return model.NewAppError(
			"BootstrapInstallation",
			"authentication.rate_limited",
			nil,
			"",
			http.StatusTooManyRequests,
		)
	}
	return nil
}

func bootstrapUnavailableError(where string, err error) *model.AppError {
	return model.NewAppError(
		where,
		"installation.unavailable",
		nil,
		"",
		http.StatusInternalServerError,
	).Wrap(err)
}
