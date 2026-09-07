// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"strings"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// AdministratorMFAResetCommand is host authority, never a User impersonation
// or network credential. It deliberately has no password or factor input.
type AdministratorMFAResetCommand struct {
	InstitutionID string
	UserID        string
}

type AdministratorMFAResetResult struct {
	RecoveryGeneration   int64
	ReenrollmentRequired bool
}

type administratorRecoveryPolicy struct {
	capabilities accessPolicyCapabilitySource
	mfaEnabled   bool
}

// ResetAdministratorMFA is absent from every network application capability.
// The module root borrows it only while its Server is inert, and the Store
// serializes it against all serving nodes and unreconciled host recovery.
func (a *App) ResetAdministratorMFA(ctx context.Context, command AdministratorMFAResetCommand) (*AdministratorMFAResetResult, error) {
	if a == nil || a.bootstrap == nil {
		return nil, errors.New("administrator MFA reset is unavailable")
	}
	return a.bootstrap.ResetAdministratorMFA(ctx, command)
}

func (s *bootstrapService) ResetAdministratorMFA(ctx context.Context, command AdministratorMFAResetCommand) (*AdministratorMFAResetResult, error) {
	institutionID, institutionErr := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID))
	userID, userErr := model.ParseUserID(strings.TrimSpace(command.UserID))
	if institutionErr != nil || userErr != nil {
		return nil, NewError("administrator_recovery.invalid")
	}
	if !s.recoveryPolicy.mfaEnabled {
		return nil, NewError("authentication.mfa.disabled")
	}
	if s.recoveryPolicy.capabilities == nil {
		return nil, NewError("administrator_recovery.failed").Wrap(errors.New("administrator recovery capabilities are unavailable"))
	}
	result, err := s.installations.ResetAdministratorMFA(ctx, &store.AdministratorMFAReset{
		InstitutionID: institutionID, UserID: userID, MFAEnabled: s.recoveryPolicy.mfaEnabled,
		Capabilities: accessDeploymentCapabilities(s.recoveryPolicy.capabilities.Snapshot()),
	})
	if err != nil {
		return nil, NewError("administrator_recovery.failed").Wrap(err)
	}
	if result == nil || !model.IsValidId(result.RecordID) || result.Recovery == nil || result.Recovery.Validate() != nil ||
		result.Recovery.UserID != userID || !result.Recovery.ReenrollmentRequired {
		return nil, NewError("administrator_recovery.failed").Wrap(errors.New("persistence returned no valid MFA recovery evidence"))
	}
	return &AdministratorMFAResetResult{RecoveryGeneration: result.Recovery.Generation, ReenrollmentRequired: true}, nil
}
