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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
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
func (s *bootstrapService) ResetAdministratorMFA(ctx context.Context, system invocation.System, command AdministratorMFAResetCommand) (*AdministratorMFAResetResult, error) {
	if !system.Valid() {
		return nil, apperror.New("administrator_recovery.invalid")
	}
	institutionID, institutionErr := model.ParseInstitutionID(strings.TrimSpace(command.InstitutionID))
	userID, userErr := model.ParseUserID(strings.TrimSpace(command.UserID))
	if institutionErr != nil || userErr != nil {
		return nil, apperror.New("administrator_recovery.invalid")
	}
	if !s.deps.RecoveryPolicy.mfaEnabled {
		return nil, apperror.New("authentication.mfa.disabled")
	}
	if s.deps.RecoveryPolicy.capabilities == nil {
		return nil, apperror.New("administrator_recovery.unavailable").Wrap(errors.New("administrator recovery capabilities are unavailable"))
	}
	result, err := s.deps.Installations.ResetAdministratorMFA(ctx, &store.AdministratorMFAReset{
		InstitutionID: institutionID, UserID: userID, MFAEnabled: s.deps.RecoveryPolicy.mfaEnabled,
		Capabilities: accessDeploymentCapabilities(s.deps.RecoveryPolicy.capabilities.Snapshot()),
	})
	if err != nil {
		return nil, apperror.New("administrator_recovery.unavailable").Wrap(err)
	}
	if result == nil || !result.RecordID.IsValid() || result.Recovery == nil || result.Recovery.Validate() != nil ||
		result.Recovery.UserID != userID || !result.Recovery.ReenrollmentRequired {
		return nil, apperror.New("administrator_recovery.unavailable").Wrap(errors.New("persistence returned no valid MFA recovery evidence"))
	}
	return &AdministratorMFAResetResult{RecoveryGeneration: result.Recovery.Generation, ReenrollmentRequired: true}, nil
}
