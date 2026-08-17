// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SetUserEnabledCommand struct {
	ID      string
	Enabled bool
}

type accountStateStore interface {
	Get(context.Context, string) (*model.User, error)
	SetDisabledWithAudit(context.Context, *store.UserDisabledStateChange) (*store.UserDisabledStateResult, error)
}

type accountStateAuthorizer interface {
	AuthorizeManage(context.Context, Invocation, string) error
}

type accountStateEffects interface {
	SessionsRevoked(context.Context, string, []*model.Session, []string)
}

type accountStateService struct {
	users         accountStateStore
	authorization accountStateAuthorizer
	capabilities  accessPolicyCapabilitySource
	audit         mutationAuditor
	effects       accountStateEffects
	now           func() time.Time
}

func newAccountStateService(users accountStateStore, authorization accountStateAuthorizer, capabilities accessPolicyCapabilitySource,
	audit mutationAuditor, effects accountStateEffects, now func() time.Time,
) *accountStateService {
	return &accountStateService{users: users, authorization: authorization, capabilities: capabilities, audit: audit, effects: effects, now: now}
}

func (a *App) SetUserEnabled(ctx context.Context, invocation Invocation, command SetUserEnabledCommand) (*model.User, error) {
	return a.accountStates.SetEnabled(ctx, invocation, command)
}

func (s *accountStateService) SetEnabled(ctx context.Context, invocation Invocation, command SetUserEnabledCommand) (*model.User, error) {
	userID := strings.TrimSpace(command.ID)
	if !model.IsValidId(userID) {
		return nil, NewError("request.invalid").WithField("field", "user_id")
	}
	if err := s.authorization.AuthorizeManage(ctx, invocation, userID); err != nil {
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
		s.now,
		func(ctx context.Context, reference mutationAttemptReference) (*store.UserDisabledStateResult, error) {
			return s.users.SetDisabledWithAudit(ctx, &store.UserDisabledStateChange{
				ID: userID, ExpectedRevision: current.Revision, Disabled: disabled,
				Capabilities: accessDeploymentCapabilities(s.capabilities.Snapshot()),
				ChangedAt:    reference.MutationAtMillis, RevocationReason: "account disabled by administrator",
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis,
			})
		},
		accountStateError,
	)
	if err != nil {
		return nil, err
	}
	if disabled {
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
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) && conflict.Constraint == "users_last_system_admin" {
		return NewError("user.last_system_admin").WithField("resource", "user").Wrap(err)
	}
	return userProfileError(err)
}
