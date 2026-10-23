// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server

import (
	"context"
	"errors"
)

// EffectiveConfiguration is the redacted display of the configuration a node
// would run with: built-in defaults, the required file, then the PROCTOR_
// environment overlay.
type EffectiveConfiguration struct {
	// Document is the indented PascalCase JSON document with every secret
	// replaced by "[redacted]". An empty secret stays empty.
	Document []byte
	// EnvironmentOverrides names, without values, the environment variables
	// that replaced a file value.
	EnvironmentOverrides []string
}

// ShowEffectiveConfiguration loads and strictly validates the configuration
// like ValidateConfig and returns its redacted effective form. It constructs
// no infrastructure and never writes the file.
func ShowEffectiveConfiguration(ctx context.Context, configPath string) (_ EffectiveConfiguration, resultErr error) {
	configuration, err := openConfiguration(ctx, configPath)
	if err != nil {
		return EffectiveConfiguration{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, configuration.Close()) }()
	document, err := configuration.Get().RedactedJSON()
	if err != nil {
		return EffectiveConfiguration{}, err
	}
	return EffectiveConfiguration{
		Document:             document,
		EnvironmentOverrides: configuration.EnvironmentOverrides(),
	}, nil
}
