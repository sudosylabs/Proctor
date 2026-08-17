// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/config"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type bootstrapStatusStore interface {
	Get(context.Context) (*model.InstallationState, error)
}

func resolveBootstrapProtection(
	ctx context.Context,
	cfg config.Config,
	installations bootstrapStatusStore,
	output io.Writer,
) (app.BootstrapProtectionPolicy, error) {
	if cfg.Authentication.Bootstrap.Secret != "" {
		return app.BootstrapProtectionPolicy{Secret: cfg.Authentication.Bootstrap.Secret}, nil
	}

	state, err := installations.Get(ctx)
	if err == nil {
		if validateErr := state.Validate(); validateErr != nil {
			return app.BootstrapProtectionPolicy{}, fmt.Errorf("validate installation bootstrap state: %w", validateErr)
		}
		return app.BootstrapProtectionPolicy{}, nil
	}
	if !store.IsNotFound(err) {
		return app.BootstrapProtectionPolicy{}, fmt.Errorf("determine installation bootstrap state: %w", err)
	}

	if cfg.Authentication.Bootstrap.DevelopmentMode {
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return app.BootstrapProtectionPolicy{}, fmt.Errorf("generate development bootstrap secret: %w", err)
		}
		secret := base64.RawURLEncoding.EncodeToString(secretBytes)
		if output == nil {
			return app.BootstrapProtectionPolicy{}, errors.New("development bootstrap secret output is unavailable")
		}
		if _, err := fmt.Fprintf(output, "Proctor development bootstrap secret: %s\n", secret); err != nil {
			return app.BootstrapProtectionPolicy{}, fmt.Errorf("display development bootstrap secret: %w", err)
		}
		return app.BootstrapProtectionPolicy{Secret: secret}, nil
	}
	return app.BootstrapProtectionPolicy{}, errors.New("bootstrap secret is required for an uninitialized network-accessible installation")
}
