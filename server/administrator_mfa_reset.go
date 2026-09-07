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

	"github.com/sudosylabs/proctor/server/app"
)

// AdministratorMFAResetCommand identifies the exact existing administrator
// whose factor access the host operator is resetting. It contains no proof,
// password, alternate identity, or authentication-policy change.
type AdministratorMFAResetCommand struct {
	InstitutionID string
	UserID        string
}

type AdministratorMFAResetResult struct {
	RecoveryGeneration   int64
	ReenrollmentRequired bool
}

// ResetAdministratorMFA constructs and closes an inert graph without starting
// workers or transports. Its named Store aggregate rejects live serving nodes.
func ResetAdministratorMFA(ctx context.Context, configPath string, command AdministratorMFAResetCommand) (_ *AdministratorMFAResetResult, resultErr error) {
	var options []Option
	if configPath != "" {
		options = append(options, WithConfigPath(configPath))
	}
	node, err := New(ctx, options...)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, node.Close()) }()
	return node.ResetAdministratorMFA(ctx, command)
}

// ResetAdministratorMFA permanently becomes unavailable when this Server
// begins running. Host authority never appears as an impersonated Principal.
func (s *Server) ResetAdministratorMFA(ctx context.Context, command AdministratorMFAResetCommand) (*AdministratorMFAResetResult, error) {
	if s == nil {
		return nil, errors.New("server is nil")
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.state != nodeInert {
		return nil, errors.New("administrator MFA reset requires an inert server")
	}
	if s.components.administratorRecovery == nil {
		return nil, errors.New("administrator MFA reset is unavailable")
	}
	result, err := s.components.administratorRecovery.ResetAdministratorMFA(ctx, app.AdministratorMFAResetCommand{
		InstitutionID: command.InstitutionID, UserID: command.UserID,
	})
	if err != nil {
		return nil, err
	}
	if result == nil || !result.ReenrollmentRequired || result.RecoveryGeneration < 1 {
		return nil, errors.New("administrator MFA reset returned no recovery restriction")
	}
	return &AdministratorMFAResetResult{RecoveryGeneration: result.RecoveryGeneration, ReenrollmentRequired: true}, nil
}
