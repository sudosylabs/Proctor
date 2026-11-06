// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// RenewParticipationCommand renews the Participation Lease of one Attempt
// Participation Generation on a named Attempt Connection. Sequence orders
// renewals within the generation, and the continuity credential and security
// coverage renewal authenticate it.
type RenewParticipationCommand struct {
	SecurityCoverage     model.SecurityCoverageRenewal
	AttemptID            model.ExamAttemptID
	ParticipationID      model.AttemptParticipationID
	ConnectionID         model.AttemptConnectionID
	Generation           int64
	Sequence             int64
	ContinuityCredential string
}

// ParticipationRenewal is the candidate-safe acknowledgement of one explicit
// application renewal. It contains authoritative database time but no raw or
// hashed credential material.
type ParticipationRenewal struct {
	SecurityCoverage model.SecurityCoverageResult
	AttemptID        model.ExamAttemptID
	ExamID           model.ExamID
	SittingID        model.ExamSittingID
	CandidateUserID  model.UserID
	ParticipationID  model.AttemptParticipationID
	Generation       int64
	AcceptedSequence int64
	DatabaseTime     time.Time
	LeaseExpiresAt   time.Time
	Duplicate        bool
}

// RenewExamAttemptParticipation authenticates a continuity renewal
// independently of the WebSocket transport ping. Store owns the database
// clock, exclusive expiry boundary, duplicate outcome, and permanent
// generation fence.
func (s *Service) RenewExamAttemptParticipation(ctx context.Context, invocation invocation.Invocation, command RenewParticipationCommand) (_ ParticipationRenewal, resultErr error) {
	defer func() { s.recordOperational("renew", resultErr) }()
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	// Requests that can never renew are refused before admission, so they
	// take no worker, queue place, or allowance from the live lane.
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return ParticipationRenewal{}, apperror.New("authentication.token_invalid")
	}
	if !principal.HasRegisteredDesktopKey() {
		return ParticipationRenewal{}, apperror.New("exam.attempt.registered_desktop_required")
	}
	if !command.AttemptID.IsValid() || !command.ParticipationID.IsValid() || !command.ConnectionID.IsValid() ||
		command.Generation < 1 || command.Sequence < 1 || command.SecurityCoverage.Validate() != nil || !model.IsValidCredentialToken(command.ContinuityCredential) {
		return ParticipationRenewal{}, apperror.Invalid("exam.attempt.invalid", "renewal")
	}
	release, admissionErr := s.enterControl(ctx, invocation, true)
	if admissionErr != nil {
		return ParticipationRenewal{}, admissionErr
	}
	defer release()
	build, err := s.deps.DesktopBuilds.ResolveAttemptDesktopBuild(ctx, principal)
	if err != nil || build.Build.Validate() != nil || build.CompatibilityPolicyRevision < 1 {
		return ParticipationRenewal{}, apperror.New("exam.attempt.desktop_incompatible").Wrap(err)
	}
	stored, err := s.deps.Persistence.RenewParticipation(ctx, &store.ExamAttemptParticipationRenewal{
		SecurityCoverage: command.SecurityCoverage, DesktopBuild: build.Build,
		AttemptID: command.AttemptID, ParticipationID: command.ParticipationID, ConnectionID: command.ConnectionID,
		CandidateUserID: principal.UserID, SessionID: principal.SessionID, Generation: command.Generation,
		DesktopRegistrationID: principal.DesktopRegistrationID, DPoPKeyThumbprint: principal.DPoPKeyThumbprint,
		DesktopCompatibilityPolicyRevision: build.CompatibilityPolicyRevision,
		Sequence:                           command.Sequence, ContinuityCredentialHash: model.HashToken(command.ContinuityCredential),
	})
	if err != nil {
		var conflict *store.ErrConflict
		if errors.As(err, &conflict) && conflict.Constraint == "attempt_participation_expired" {
			return ParticipationRenewal{}, s.expireLapsedParticipation(ctx, command.AttemptID, command.ParticipationID, command.Generation, err)
		}
		return ParticipationRenewal{}, storeError(storeErrors, err)
	}
	if stored == nil || stored.SecurityCoverage.Validate() != nil || stored.AttemptID != command.AttemptID || stored.ParticipationID != command.ParticipationID ||
		!stored.ExamID.IsValid() || !stored.SittingID.IsValid() || stored.CandidateUserID != principal.UserID ||
		stored.Generation != command.Generation || stored.AcceptedSequence != command.Sequence || stored.DatabaseTime.IsZero() ||
		stored.LeaseExpiresAt.IsZero() || !stored.LeaseExpiresAt.After(stored.DatabaseTime) || stored.LeaseExpiresAt.After(stored.DatabaseTime.Add(model.AttemptParticipationInitialLease)) {
		return ParticipationRenewal{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Participation renewal outcome"))
	}
	result := ParticipationRenewal{SecurityCoverage: stored.SecurityCoverage,
		AttemptID: stored.AttemptID, ExamID: stored.ExamID, SittingID: stored.SittingID,
		CandidateUserID: stored.CandidateUserID, ParticipationID: stored.ParticipationID, Generation: stored.Generation,
		AcceptedSequence: stored.AcceptedSequence, DatabaseTime: model.TimeUTC(stored.DatabaseTime),
		LeaseExpiresAt: model.TimeUTC(stored.LeaseExpiresAt), Duplicate: stored.Duplicate,
	}
	if !result.Duplicate {
		if effectErr := s.deps.Effects.ParticipationRenewed(ctx, result); effectErr != nil {
			s.deps.EffectFailures.Report(ctx, "exam_attempt_participation_renewed", effectErr)
		}
	}
	return result, nil
}
