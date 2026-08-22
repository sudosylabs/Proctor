// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SetUserEnabledCommand struct {
	ID                        string
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
	Get(context.Context, string) (*model.User, error)
	SetDisabledWithAudit(context.Context, *store.UserDisabledStateChange) (*store.UserDisabledStateResult, error)
}

type accountStateAuthorizer interface {
	AuthorizeAccountStateManage(context.Context, Invocation, string) error
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

func (a *App) SetUserEnabled(ctx context.Context, invocation Invocation, command SetUserEnabledCommand) (*model.User, error) {
	return a.accountStates.SetEnabled(ctx, invocation, command)
}

func (s *accountStateService) SetEnabled(ctx context.Context, invocation Invocation, command SetUserEnabledCommand) (*model.User, error) {
	userID := strings.TrimSpace(command.ID)
	if !model.IsValidId(userID) {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeAccountStateManage(ctx, invocation, userID); err != nil {
		return nil, err
	}
	if invocation.Principal().UserID.String() == userID && !command.Enabled {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	current, err := s.users.Get(ctx, userID)
	if err != nil {
		return nil, accountStateError(err)
	}
	disabled := !command.Enabled
	idempotency, err := newCommandIdempotency(invocation, "user.enabled_state.v1", command.IdempotencyKey, struct {
		UserID  string `json:"user_id"`
		Enabled bool   `json:"enabled"`
	}{userID, command.Enabled})
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
	prepared := &preparedDirectMail{}
	if !command.batchRetainedOutcome {
		prepared, err = s.mail.PrepareAccountStateChanged(appmail.NoticePreparation{Recipient: &recipient, At: now}, command.Enabled)
		if err != nil {
			return nil, accountStateError(err)
		}
	}
	result, err := runAuditedMutation(
		ctx,
		s.audit,
		mutationAttempt{
			Invocation: invocation,
			Action:     model.ActionUserManage,
			Resource:   model.Resource{Type: model.ResourceUser, ID: userID},
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
			value, storeErr := s.users.SetDisabledWithAudit(ctx, input)
			if command.batchReplayed != nil {
				*command.batchReplayed = input.Replayed || input.NoOp
			}
			return value, storeErr
		},
		accountStateError,
	)
	if err != nil {
		return nil, err
	}
	if disabled && result != nil && len(result.RevokedSessions) > 0 {
		s.effects.SessionsRevoked(ctx, userID, result.RevokedSessions, result.RevokedTokenHashes)
	}
	return result.User, nil
}

type accountStateRealtimeEffects struct {
	effects authenticationSecurityEffects
}

func (e accountStateRealtimeEffects) SessionsRevoked(ctx context.Context, userID string, sessions []*model.Session, hashes []string) {
	e.effects.SessionsRevoked(ctx, userID, sessionIds(sessions), hashes)
}

func accountStateError(err error) error {
	if mapped := idempotencyError(err); mapped != nil {
		return mapped
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) && conflict.Constraint == "users_last_system_admin" {
		return NewError("user.last_system_admin").WithField("resource", "user").Wrap(err)
	}
	return userProfileError(err)
}
