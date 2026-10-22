// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SetUserEnabledCommand struct {
	ID                        model.UserID
	Enabled                   bool
	IdempotencyKey            string
	batchReplayed             *bool
	batchAuthorization        *store.CommandAuthorization
	batchMetadata             *store.CommandBatch
	batchRetainedOutcome      bool
	onboardingImportID        model.OnboardingImportID
	onboardingImportRowNumber int
}

type accountStateStore interface {
	Get(context.Context, model.UserID) (*model.User, error)
	SetDisabled(context.Context, *store.UserDisabledStateChange) (*store.UserDisabledStateResult, error)
}

type accountStateAuthorizer interface {
	AuthorizeAccountStateManage(context.Context, invocation.Invocation, string) error
}

type accountStateEffects interface {
	SessionsRevoked(context.Context, string, []*model.Session, []string)
}

type accountStateService struct {
	users         accountStateStore
	authorization accountStateAuthorizer
	capabilities  accessPolicyCapabilitySource
	audit         mutationAuditor
	mail          accountStateMailPreparer
	effects       accountStateEffects
	now           func() time.Time
}

func newAccountStateService(users accountStateStore, authorization accountStateAuthorizer, capabilities accessPolicyCapabilitySource,
	audit mutationAuditor, mail accountStateMailPreparer, effects accountStateEffects, now func() time.Time,
) *accountStateService {
	return &accountStateService{users: users, authorization: authorization, capabilities: capabilities, audit: audit, mail: mail, effects: effects, now: now}
}

func (s *accountStateService) SetUserEnabled(ctx context.Context, invocation invocation.Invocation, command SetUserEnabledCommand) (*model.User, error) {
	userID := command.ID
	if !userID.IsValid() {
		return nil, apperror.New("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeAccountStateManage(ctx, invocation, userID.String()); err != nil {
		return nil, err
	}
	if invocation.Principal().UserID == userID && !command.Enabled {
		return nil, apperror.New("request.invalid").WithField("field", "user_id")
	}
	current, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, accountStateStoreErrors.Translate(err)
	}
	disabled := !command.Enabled
	idempotency, err := idempotency.Prepare(invocation, "user.enabled_state", command.IdempotencyKey, struct {
		UserID  string `json:"user_id"`
		Enabled bool   `json:"enabled"`
	}{userID.String(), command.Enabled})
	if err != nil {
		return nil, err
	}
	bindOnboardingImportCommand(idempotency, command.onboardingImportID, command.onboardingImportRowNumber)
	bindAcademicAdministrationAuthorization(idempotency, command.batchAuthorization)
	bindAcademicAdministrationBatch(idempotency, command.batchMetadata)
	alreadySatisfied := current.DisabledAt.Valid == disabled
	if alreadySatisfied && idempotency == nil {
		return current, nil
	}
	now := model.TimeFromMillis(s.now().UnixMilli())
	recipient := *current
	// Preparation freezes recipient fields only. SQL remains authoritative for
	// whether the state change is a no-op, so make the snapshot mail-eligible
	// even when a concurrent re-enable could turn a stale no-op into a mutation.
	recipient.DisabledAt = model.OptionalTime{}
	prepared := &store.PreparedMail{}
	if !command.batchRetainedOutcome {
		prepared, err = s.mail.PrepareAccountStateChanged(mail.NoticePreparation{Recipient: &recipient, At: now}, command.Enabled)
		if err != nil {
			return nil, accountStateStoreErrors.Translate(err)
		}
	}
	result, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionUserManage,
			Resource:   model.Resource{Type: model.ResourceUser, ID: userID.String()},
			Operation:  "set_disabled",
			Value:      map[string]any{"user_id": userID, "disabled": disabled},
			Prior:      current.Auditable(),
		},
		func() time.Time { return now },
		func(ctx context.Context, reference mutationAttemptReference) (*store.UserDisabledStateResult, error) {
			input := &store.UserDisabledStateChange{
				ID: userID, ExpectedRevision: current.Revision, Disabled: disabled,
				Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()),
				Occurrence:   prepared.Occurrence, Delivery: prepared.Delivery, DeliveryJob: prepared.Job,
				ChangedAt: reference.MutationAtMillis, RevocationReason: model.SessionRevocationAccountDisabled,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis, Command: idempotency,
			}
			value, storeErr := s.users.SetDisabled(ctx, input)
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
		},
		accountStateStoreErrors.Translate,
	)
	if err != nil {
		return nil, err
	}
	if disabled && result != nil && len(result.RevokedSessions) > 0 {
		s.effects.SessionsRevoked(ctx, userID.String(), result.RevokedSessions, result.RevokedTokenHashes)
	}
	return result.User, nil
}

type accountStateRealtimeEffects struct {
	effects authenticationSecurityEffects
}

func (e accountStateRealtimeEffects) SessionsRevoked(ctx context.Context, userID string, sessions []*model.Session, hashes []string) {
	e.effects.SessionsRevoked(ctx, userID, sessionIDs(sessions), hashes)
}

var accountStateStoreErrors = apperror.AdministrationStoreTable("user", "user.conflict", "user.invalid", map[string]apperror.Code{
	"users_last_system_admin": "user.last_system_admin",
})
