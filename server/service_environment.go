// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and NOTICE in the server module root for
// license and attribution information.
// SPDX-License-Identifier: Apache-2.0
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost service environment selection; see NOTICE.

package server

import (
	"errors"
	"os"
	"strings"
)

// ServiceEnvironment selects process behavior independently of persisted
// deployment configuration. Only dev skips Desktop login compatibility checks.
type ServiceEnvironment string

const (
	ServiceEnvironmentProduction ServiceEnvironment = "production"
	ServiceEnvironmentTest       ServiceEnvironment = "test"
	ServiceEnvironmentDev        ServiceEnvironment = "dev"
)

func readServiceEnvironment() (ServiceEnvironment, error) {
	return parseServiceEnvironment(os.Getenv("PROCTOR_SERVICE_ENVIRONMENT"))
}

func parseServiceEnvironment(value string) (ServiceEnvironment, error) {
	environment := ServiceEnvironment(strings.ToLower(strings.TrimSpace(value)))
	if environment == "" {
		return defaultServiceEnvironment, nil
	}
	if !environment.valid() {
		return "", errors.New("PROCTOR_SERVICE_ENVIRONMENT must be production, test, or dev")
	}
	return environment, nil
}

func (environment ServiceEnvironment) valid() bool {
	return environment == ServiceEnvironmentProduction || environment == ServiceEnvironmentTest || environment == ServiceEnvironmentDev
}
