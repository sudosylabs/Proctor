// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"strings"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s *invitationService) ListInvitations(ctx context.Context, invocation invocation.Invocation, query ListInvitationsQuery) (InvitationAdministrationPage, error) {
	visibility, err := s.deps.Authorization.Visibility(ctx, invocation, model.ActionInvitationView)
	if err != nil {
		return InvitationAdministrationPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	targetEmail := strings.ToLower(strings.TrimSpace(query.TargetEmail))
	if query.Limit < 1 || query.Limit > 200 || (query.Purpose != "" && !query.Purpose.IsValid()) ||
		(query.State != "" && !query.State.IsValid()) || (targetEmail != "" && !model.IsValidEmail(targetEmail)) ||
		(query.TargetID != "" && !model.IsValidOpaqueID(query.TargetID)) ||
		(query.BeforeID.IsValid() != !query.BeforeCreatedAt.IsZero()) {
		return InvitationAdministrationPage{}, apperror.New("invitation.query.invalid")
	}
	page, err := s.deps.Store.List(ctx, store.InvitationListOptions{Visibility: visibility, Purpose: query.Purpose, State: query.State,
		TargetEmail: targetEmail, TargetID: strings.TrimSpace(query.TargetID), CreatedAfter: query.CreatedAfter,
		CreatedBefore: query.CreatedBefore, BeforeCreatedAt: query.BeforeCreatedAt, BeforeID: query.BeforeID, Limit: query.Limit})
	if err != nil {
		return InvitationAdministrationPage{}, invitationStoreErrors.Translate(err)
	}
	result := InvitationAdministrationPage{Items: []InvitationAdministrationView{}, More: page.More}
	for _, item := range page.Items {
		result.Items = append(result.Items, invitationAdministrationView(item))
	}
	return result, nil
}

func (s *invitationService) GetInvitation(ctx context.Context, invocation invocation.Invocation, id model.InvitationID) (InvitationAdministrationView, error) {
	if !id.IsValid() {
		return InvitationAdministrationView{}, apperror.New("request.invalid").WithField("field", "invitation_id")
	}
	visibility, err := s.deps.Authorization.Visibility(ctx, invocation, model.ActionInvitationView)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	record, err := s.deps.Store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationStoreErrors.Translate(err)
	}
	return invitationAdministrationView(record), nil
}

func (s *invitationService) ResendInvitation(ctx context.Context, invocation invocation.Invocation, command ResendInvitationCommand) (InvitationAdministrationView, error) {
	id := command.ID
	if !id.IsValid() || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, apperror.New("request.invalid").WithField("field", "invitation_id")
	}
	visibility, err := s.deps.Authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.deps.Store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationStoreErrors.Translate(err)
	}
	if command.BatchScopeID != "" {
		if current.Invitation == nil || current.Invitation.ScopeType != command.BatchScopeType || current.Invitation.ScopeID != command.BatchScopeID {
			return InvitationAdministrationView{}, apperror.New("resource.not_found")
		}
		if invitationPurposeRequiresInteractiveBatch(current.Invitation.Purpose) {
			if err = requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
				return InvitationAdministrationView{}, err
			}
		}
	}
	idempotency, err := idempotency.Prepare(invocation, "invitation.resend", command.IdempotencyKey,
		invitationLifecycleIdempotencySemantic{ID: id.String(), Revision: command.ExpectedRevision})
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "resend", Value: current.Invitation.Auditable()}
	if recovered, ok, recoverErr := s.recoverInvitationAdministrationOutcome(ctx, idempotency, attempt); ok {
		return recovered, recoverErr
	}
	if command.batchDuplicate {
		if !s.deps.Mail.Enabled() {
			return InvitationAdministrationView{}, apperror.New("invitation.mail_unavailable")
		}
		view, duplicateErr := s.recordDuplicateInvitationView(ctx, idempotency, attempt,
			store.InvitationBatchDuplicate{LifecycleID: id, ExpectedRevision: command.ExpectedRevision,
				CanonicalFingerprint: command.batchCanonicalFingerprint}, command.batchCanonicalKey)
		return InvitationAdministrationView{InvitationView: view}, duplicateErr
	}
	result, err := runAuditedMutation(ctx, s.deps.Audit, attempt, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationAdministrationCommandResult, error) {
			if idempotency != nil {
				replayed, replayErr := s.deps.Store.ReplayAdministration(ctx, &store.InvitationReplay{AuditEventID: reference.ID, AuditAt: reference.MutationAt}, idempotency)
				if replayErr == nil {
					if replayed.Duplicate {
						return nil, apperror.New("onboarding_batch.duplicate")
					}
					return replayed, nil
				}
				if !store.IsNotFound(replayErr) {
					return nil, replayErr
				}
			}
			if !s.deps.Mail.Enabled() {
				return nil, apperror.New("invitation.mail_unavailable")
			}
			at := model.TimeUTC(reference.MutationAt)
			rawClaim := s.deps.NewClaim()
			if !model.IsValidCredentialToken(rawClaim) {
				return nil, apperror.New("invitation.unavailable")
			}
			actionURL, linkErr := accountCredentialLink(s.deps.PublicURL, "/join", rawClaim)
			if linkErr != nil {
				return nil, apperror.New("invitation.unavailable").Wrap(linkErr)
			}
			prepared, prepareErr := s.deps.Mail.PrepareInvitationResend(current.Invitation, actionURL, invocation.Principal().UserID, at)
			if prepareErr != nil {
				return nil, apperror.New("invitation.mail_unavailable").Wrap(prepareErr)
			}
			input := &store.InvitationResend{ID: id, ExpectedRevision: command.ExpectedRevision,
				ClaimHash: model.HashInvitationClaim(rawClaim), Occurrence: prepared.Occurrence, Delivery: prepared.Delivery,
				DeliveryJob: prepared.Job, ActorUserID: invocation.Principal().UserID,
				AuditEventID: reference.ID, AuditAt: reference.MutationAt}
			if idempotency != nil {
				value, storeErr := s.deps.Store.ResendIdempotently(ctx, input, idempotency)
				if storeErr == nil && value.Duplicate {
					return nil, apperror.New("onboarding_batch.duplicate")
				}
				return value, storeErr
			}
			record, storeErr := s.deps.Store.Resend(ctx, input)
			return &store.InvitationAdministrationCommandResult{Record: record}, storeErr
		}, invitationStoreErrors.Translate)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	view := invitationAdministrationView(result.Record)
	view.Replayed = result.Replayed
	return view, nil
}

func (s *invitationService) RevokeInvitation(ctx context.Context, invocation invocation.Invocation, command RevokeInvitationCommand) (InvitationAdministrationView, error) {
	id := command.ID
	if !id.IsValid() || command.ExpectedRevision < 1 {
		return InvitationAdministrationView{}, apperror.New("request.invalid").WithField("field", "invitation_id")
	}
	visibility, err := s.deps.Authorization.Visibility(ctx, invocation, model.ActionInvitationManage)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	current, err := s.deps.Store.GetForAdministration(ctx, id, visibility)
	if err != nil {
		return InvitationAdministrationView{}, invitationStoreErrors.Translate(err)
	}
	if command.BatchScopeID != "" {
		if current.Invitation == nil || current.Invitation.ScopeType != command.BatchScopeType || current.Invitation.ScopeID != command.BatchScopeID {
			return InvitationAdministrationView{}, apperror.New("resource.not_found")
		}
		if invitationPurposeRequiresInteractiveBatch(current.Invitation.Purpose) {
			if err = requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
				return InvitationAdministrationView{}, err
			}
		}
	}
	idempotency, err := idempotency.Prepare(invocation, "invitation.revoke", command.IdempotencyKey,
		invitationLifecycleIdempotencySemantic{ID: id.String(), Revision: command.ExpectedRevision})
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	attempt := mutationAttempt{Invocation: invocation, Action: model.ActionInvitationManage,
		Resource: invitationResource(current.Invitation), ScopeType: current.Invitation.ScopeType, ScopeID: current.Invitation.ScopeID,
		Operation: "revoke", Value: current.Invitation.Auditable()}
	if recovered, ok, recoverErr := s.recoverInvitationAdministrationOutcome(ctx, idempotency, attempt); ok {
		return recovered, recoverErr
	}
	if command.batchDuplicate {
		view, duplicateErr := s.recordDuplicateInvitationView(ctx, idempotency, attempt,
			store.InvitationBatchDuplicate{LifecycleID: id, ExpectedRevision: command.ExpectedRevision,
				CanonicalFingerprint: command.batchCanonicalFingerprint}, command.batchCanonicalKey)
		return InvitationAdministrationView{InvitationView: view}, duplicateErr
	}
	result, err := runAuditedMutation(ctx, s.deps.Audit, attempt, s.deps.Now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.InvitationAdministrationCommandResult, error) {
			if idempotency != nil {
				replayed, replayErr := s.deps.Store.ReplayAdministration(ctx, &store.InvitationReplay{AuditEventID: reference.ID, AuditAt: reference.MutationAt}, idempotency)
				if replayErr == nil {
					if replayed.Duplicate {
						return nil, apperror.New("onboarding_batch.duplicate")
					}
					return replayed, nil
				}
				if !store.IsNotFound(replayErr) {
					return nil, replayErr
				}
			}
			at := model.TimeUTC(reference.MutationAt)
			prepared, prepareErr := s.deps.Mail.PrepareInvitationRevocation(current.Invitation, invocation.Principal().UserID, at)
			if prepareErr != nil {
				return nil, apperror.New("invitation.mail_unavailable").Wrap(prepareErr)
			}
			input := &store.InvitationRevocation{ID: id, ExpectedRevision: command.ExpectedRevision,
				ActorUserID: invocation.Principal().UserID, RevocationNotice: &store.PreparedMail{Occurrence: prepared.Occurrence,
					Delivery: prepared.Delivery, Job: prepared.Job}, AuditEventID: reference.ID, AuditAt: reference.MutationAt}
			if idempotency != nil {
				value, storeErr := s.deps.Store.RevokeIdempotently(ctx, input, idempotency)
				if storeErr == nil && value.Duplicate {
					return nil, apperror.New("onboarding_batch.duplicate")
				}
				return value, storeErr
			}
			record, storeErr := s.deps.Store.Revoke(ctx, input)
			return &store.InvitationAdministrationCommandResult{Record: record}, storeErr
		}, invitationStoreErrors.Translate)
	if err != nil {
		return InvitationAdministrationView{}, err
	}
	view := invitationAdministrationView(result.Record)
	view.Replayed = result.Replayed
	return view, nil
}
