// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
//
// Adapted from Mattermost server/channels/app/user.go access-token lifecycle.
// Proctor requires finite lifetimes, explicit known-action scopes, recent
// interactive authentication for creation, optional academic-unit ceilings,
// hashed persistence, and durable security auditing.

package app

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	personalAccessTokenMutationPreparationLifetime = 5 * time.Minute
)

// CreatePersonalAccessTokenCommand creates a new PAT for the interactive caller.
type CreatePersonalAccessTokenCommand struct {
	Description    string
	Scopes         []string
	AcademicUnitID model.AcademicUnitID
	ExpiresAt      int64
}

// ListPersonalAccessTokensQuery lists the caller's PATs.
type ListPersonalAccessTokensQuery struct{}

// RevokePersonalAccessTokenCommand revokes one owned PAT.
type RevokePersonalAccessTokenCommand struct {
	TokenID model.PersonalAccessTokenID
}

// SetPersonalAccessTokenDisabledCommand enables or disables an owned PAT.
type SetPersonalAccessTokenDisabledCommand struct {
	TokenID  model.PersonalAccessTokenID
	Disabled bool
}

type personalAccessTokenAuditor interface {
	Prepare(
		context.Context, model.Principal, model.Action, model.Resource,
		model.RequestMetadata, map[string]any, map[string]any,
	) (*model.AuditEvent, error)
}

type personalAccessTokenScopeAuthorizer interface {
	CanDelegateActionsAtScope(context.Context, model.Principal, []string, model.RoleScopeType, string) (bool, error)
}

type personalAccessTokenUserStore interface {
	Get(context.Context, model.UserID) (*model.User, error)
}

// personalAccessTokenAdministrationServiceDependencies are the collaborators of personalAccessTokenAdministrationService;
// newPersonalAccessTokenAdministrationService validates them once and personalAccessTokenAdministrationService keeps them in deps.
type personalAccessTokenAdministrationServiceDependencies struct {
	Tokens                  store.PersonalAccessTokenStore
	Users                   personalAccessTokenUserStore
	AcademicUnits           store.AcademicUnitStore
	Institutions            store.InstitutionStore
	Audit                   personalAccessTokenAuditor
	Authorization           personalAccessTokenScopeAuthorizer
	Mail                    personalAccessTokenSecurityNoticeMailPreparer
	Policy                  PersonalAccessTokenPolicy
	RecentAuthenticationTTL time.Duration
	NewCredential           func() string
	Now                     func() time.Time
}

type personalAccessTokenAdministrationService struct {
	deps personalAccessTokenAdministrationServiceDependencies
}

func newPersonalAccessTokenAdministrationService(deps personalAccessTokenAdministrationServiceDependencies) (*personalAccessTokenAdministrationService, error) {
	if deps.Tokens == nil {
		return nil, errors.New("personal access token administration store is required")
	}
	if deps.Users == nil {
		return nil, errors.New("personal access token user store is required")
	}
	if deps.AcademicUnits == nil {
		return nil, errors.New("personal access token academic unit store is required")
	}
	if deps.Institutions == nil {
		return nil, errors.New("personal access token institution store is required")
	}
	if deps.Audit == nil {
		return nil, errors.New("personal access token audit is required")
	}
	if deps.Authorization == nil {
		return nil, errors.New("personal access token scope authorization is required")
	}
	if deps.Mail == nil {
		return nil, errors.New("personal access token mail preparer is required")
	}
	if deps.NewCredential == nil {
		return nil, errors.New("personal access token credential generator is required")
	}
	if deps.Now == nil {
		return nil, errors.New("personal access token clock is required")
	}
	return &personalAccessTokenAdministrationService{deps: deps}, nil
}

func (s *personalAccessTokenAdministrationService) CreatePersonalAccessToken(
	ctx context.Context,
	invocation invocation.Invocation,
	command CreatePersonalAccessTokenCommand,
) (*model.PersonalAccessTokenCreation, error) {
	principal := invocation.Principal()
	at := model.TimeUTC(s.deps.Now())
	if err := requireInteractiveSession(
		principal, true, at, s.deps.RecentAuthenticationTTL,
	); err != nil {
		return nil, err
	}
	settings := s.deps.Policy
	if command.ExpiresAt <= 0 {
		return nil, invalidPersonalAccessTokenRequest("expires_at")
	}
	normalizedScopes, err := normalizePersonalAccessTokenScopes(command.Scopes)
	if err != nil {
		return nil, err
	}
	if command.AcademicUnitID != "" {
		if !model.AcademicUnitID(command.AcademicUnitID).IsValid() {
			return nil, invalidPersonalAccessTokenRequest("academic_unit_id")
		}
		if _, err = s.deps.AcademicUnits.Get(ctx, command.AcademicUnitID); err != nil {
			return nil, personalAccessTokenFailure("academic_unit", err)
		}
	}
	targetScopeType := model.RoleScopeInstitution
	targetScopeID := ""
	if command.AcademicUnitID != "" {
		targetScopeType = model.RoleScopeAcademicUnit
		targetScopeID = command.AcademicUnitID.String()
	} else {
		institution, lookupErr := s.deps.Institutions.GetSingleton(ctx)
		if lookupErr != nil {
			return nil, personalAccessTokenFailure("institution", lookupErr)
		}
		targetScopeID = institution.ID.String()
	}
	allowed, err := s.deps.Authorization.CanDelegateActionsAtScope(
		ctx, principal, normalizedScopes, targetScopeType, targetScopeID,
	)
	if err != nil {
		return nil, personalAccessTokenFailure("authorization", err)
	}
	if !allowed {
		return nil, invalidPersonalAccessTokenRequest("scopes")
	}

	rawCredential := s.deps.NewCredential()
	candidate := &model.PersonalAccessToken{
		UserID:         principal.UserID,
		Description:    command.Description,
		TokenHash:      model.HashToken(rawCredential),
		Scopes:         normalizedScopes,
		AcademicUnitID: model.AcademicUnitID(command.AcademicUnitID),
		ExpiresAt:      model.TimeFromMillis(command.ExpiresAt),
	}
	parameters := map[string]any{
		"description": command.Description, "scopes": normalizedScopes,
		"academic_unit_id": command.AcademicUnitID, "expires_at": command.ExpiresAt,
	}
	resource, err := s.auditResource(ctx)
	if err != nil {
		return nil, err
	}
	auditDraft, appErr := s.deps.Audit.Prepare(
		ctx, principal, model.ActionPersonalAccessTokenCreate, resource, invocation.RequestMetadata(),
		parameters, nil,
	)
	if appErr != nil {
		return nil, appErr
	}
	prepared, err := s.deps.Tokens.PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: principal.UserID, Kind: store.PersonalAccessTokenMutationCreate,
		AuditEvent: auditDraft, Lifetime: personalAccessTokenMutationPreparationLifetime,
	})
	if err != nil {
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	notice, err := s.prepareSecurityNotice(ctx, principal.UserID, candidate, model.MailTemplateIdentityPersonalAccessTokenCreated, prepared.ActionAt)
	if err != nil {
		return nil, s.failPreparedMutation(ctx, prepared.ID.String(), err)
	}
	result, err := s.deps.Tokens.Create(ctx, &store.PersonalAccessTokenCreationMutation{
		Token: candidate, MaximumActive: settings.MaximumPerUser,
		MinimumLifetime: settings.MinimumLifetime, MaximumLifetime: settings.MaximumLifetime,
		PreparationID: prepared.ID, Notice: notice,
	})
	if err != nil {
		return nil, s.failPreparedMutation(ctx, prepared.ID.String(), err)
	}
	return &model.PersonalAccessTokenCreation{
		Token: result.Token, Credential: rawCredential,
	}, nil
}

func (s *personalAccessTokenAdministrationService) ListPersonalAccessTokens(
	ctx context.Context,
	invocation invocation.Invocation,
	_ ListPersonalAccessTokensQuery,
) ([]*model.PersonalAccessToken, error) {
	principal := invocation.Principal()
	if err := requireInteractiveSession(principal, false, s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
		return nil, err
	}
	tokens, err := s.deps.Tokens.ListByUser(ctx, principal.UserID)
	if err != nil {
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	return tokens, nil
}

func (s *personalAccessTokenAdministrationService) RevokePersonalAccessToken(
	ctx context.Context,
	invocation invocation.Invocation,
	command RevokePersonalAccessTokenCommand,
) (*model.PersonalAccessToken, error) {
	principal := invocation.Principal()
	if err := requireInteractiveSession(principal, false, s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
		return nil, err
	}
	if !command.TokenID.IsValid() {
		return nil, invalidPersonalAccessTokenRequest("personal_access_token_id")
	}
	current, err := s.deps.Tokens.Get(ctx, command.TokenID)
	if err != nil || current.UserID != principal.UserID {
		if err == nil {
			err = store.NewErrNotFound("personal_access_token", command.TokenID.String())
		}
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	if current.RevokedAt.Valid {
		return current, nil
	}
	resource, err := s.auditResource(ctx)
	if err != nil {
		return nil, err
	}
	auditDraft, appErr := s.deps.Audit.Prepare(
		ctx, principal, model.ActionPersonalAccessTokenRevoke, resource, invocation.RequestMetadata(),
		map[string]any{"personal_access_token_id": command.TokenID},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	prepared, err := s.deps.Tokens.PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: principal.UserID, TokenID: command.TokenID, Kind: store.PersonalAccessTokenMutationRevoke,
		AuditEvent: auditDraft, Lifetime: personalAccessTokenMutationPreparationLifetime,
	})
	if err != nil {
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	notice, err := s.prepareSecurityNotice(ctx, principal.UserID, current, model.MailTemplateIdentityPersonalAccessTokenRevoked, prepared.ActionAt)
	if err != nil {
		return nil, s.failPreparedMutation(ctx, prepared.ID.String(), err)
	}
	result, err := s.deps.Tokens.Revoke(ctx, &store.PersonalAccessTokenRevocation{
		ID: command.TokenID, UserID: principal.UserID, PreparationID: prepared.ID, Notice: notice,
	})
	if err != nil {
		return nil, s.failPreparedMutation(ctx, prepared.ID.String(), err)
	}
	return result.Token, nil
}

func (s *personalAccessTokenAdministrationService) SetPersonalAccessTokenDisabled(
	ctx context.Context,
	invocation invocation.Invocation,
	command SetPersonalAccessTokenDisabledCommand,
) (*model.PersonalAccessToken, error) {
	principal := invocation.Principal()
	at := model.TimeUTC(s.deps.Now())
	action := model.ActionPersonalAccessTokenDisable
	if !command.Disabled {
		action = model.ActionPersonalAccessTokenEnable
	}
	if err := requireInteractiveSession(
		principal, !command.Disabled, at, s.deps.RecentAuthenticationTTL,
	); err != nil {
		return nil, err
	}
	if !command.TokenID.IsValid() {
		return nil, invalidPersonalAccessTokenRequest("personal_access_token_id")
	}
	current, err := s.deps.Tokens.Get(ctx, command.TokenID)
	if err != nil || current.UserID != principal.UserID {
		if err == nil {
			err = store.NewErrNotFound("personal_access_token", command.TokenID.String())
		}
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	if command.Disabled == current.DisabledAt.Valid {
		return current, nil
	}
	resource, err := s.auditResource(ctx)
	if err != nil {
		return nil, err
	}
	auditDraft, appErr := s.deps.Audit.Prepare(
		ctx, principal, action, resource, invocation.RequestMetadata(),
		map[string]any{
			"personal_access_token_id": command.TokenID,
			"disabled":                 command.Disabled,
		},
		current.Auditable(),
	)
	if appErr != nil {
		return nil, appErr
	}
	key := model.MailTemplateIdentityPersonalAccessTokenEnabled
	if command.Disabled {
		key = model.MailTemplateIdentityPersonalAccessTokenDisabled
	}
	kind := store.PersonalAccessTokenMutationEnable
	if command.Disabled {
		kind = store.PersonalAccessTokenMutationDisable
	}
	prepared, err := s.deps.Tokens.PrepareMutation(ctx, &store.PersonalAccessTokenMutationPreparation{
		UserID: principal.UserID, TokenID: command.TokenID, Kind: kind,
		AuditEvent: auditDraft, Lifetime: personalAccessTokenMutationPreparationLifetime,
	})
	if err != nil {
		return nil, personalAccessTokenFailure("personal_access_token", err)
	}
	notice, err := s.prepareSecurityNotice(ctx, principal.UserID, current, key, prepared.ActionAt)
	if err != nil {
		return nil, s.failPreparedMutation(ctx, prepared.ID.String(), err)
	}
	result, err := s.deps.Tokens.ChangeState(ctx, &store.PersonalAccessTokenStateMutation{
		ID: command.TokenID, UserID: principal.UserID, Disabled: command.Disabled,
		MaximumActive: s.deps.Policy.MaximumPerUser, PreparationID: prepared.ID, Notice: notice,
	})
	if err != nil {
		return nil, s.failPreparedMutation(ctx, prepared.ID.String(), err)
	}
	return result.Token, nil
}

func (s *personalAccessTokenAdministrationService) prepareSecurityNotice(
	ctx context.Context,
	userID model.UserID,
	token *model.PersonalAccessToken,
	key model.MailTemplateKey,
	at time.Time,
) (store.PersonalAccessTokenSecurityNotice, error) {
	user, err := s.deps.Users.Get(ctx, userID)
	if err != nil || user == nil || user.ID != userID {
		return store.PersonalAccessTokenSecurityNotice{}, personalAccessTokenFailure("user", err)
	}
	prepared, err := s.deps.Mail.PreparePersonalAccessTokenSecurityNotice(mail.PersonalAccessTokenPreparation{
		Recipient: user, TemplateKey: key, Description: token.Description,
		ExpiresAt: token.ExpiresAt, ActionAt: at, ActionCount: len(token.Scopes),
		AcademicUnitScoped: !token.AcademicUnitID.IsZero(),
	})
	if err != nil {
		return store.PersonalAccessTokenSecurityNotice{}, apperror.New("mail.unavailable").Wrap(err)
	}
	return store.PersonalAccessTokenSecurityNotice{
		Occurrence: prepared.Occurrence, Delivery: prepared.Delivery, Job: prepared.Job,
		ExpiresAt: token.ExpiresAt,
	}, nil
}

func requireInteractiveSession(
	principal model.Principal,
	recent bool,
	now time.Time,
	recentAuthenticationTTL time.Duration,
) error {
	if principal.Validate() != nil ||
		principal.CredentialType != model.CredentialSessionAccess {
		return apperror.New("authentication.session_required")
	}
	if recent && !principal.IsRecentlyAuthenticated(
		now,
		recentAuthenticationTTL,
	) {
		return apperror.New("authentication.reauthentication_required")
	}
	return nil
}

func normalizePersonalAccessTokenScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 || len(scopes) > model.PersonalAccessTokenScopeMaxCount {
		return nil, invalidPersonalAccessTokenRequest("scopes")
	}
	result := append([]string(nil), scopes...)
	sort.Strings(result)
	for index, scope := range result {
		if !model.IsPersonalAccessTokenAction(scope) ||
			(index > 0 && result[index-1] == scope) {
			return nil, invalidPersonalAccessTokenRequest("scopes")
		}
	}
	return result, nil
}

func (s *personalAccessTokenAdministrationService) auditResource(
	ctx context.Context,
) (model.Resource, error) {
	institution, err := s.deps.Institutions.GetSingleton(ctx)
	if err != nil {
		return model.Resource{}, personalAccessTokenFailure("institution", err)
	}
	return model.Resource{
		Type: model.ResourceInstitution,
		ID:   institution.ID.String(),
	}, nil
}

func (s *personalAccessTokenAdministrationService) failPreparedMutation(
	ctx context.Context,
	preparationID string,
	err error,
) error {
	mapped := personalAccessTokenFailure("personal_access_token", err)
	code := "personal_access_token.unavailable"
	if failure, ok := apperror.As(mapped); ok {
		code = failure.Code()
	}
	if terminalErr := s.deps.Tokens.FailMutation(ctx, &store.PersonalAccessTokenMutationFailure{PreparationID: model.PersonalAccessTokenPreparationID(preparationID), ErrorCode: code}); terminalErr != nil {
		return apperror.New("audit.unavailable").Wrap(terminalErr)
	}
	return mapped
}

type personalAccessTokenAuditAdapter struct {
	audit *auditService
}

func (a personalAccessTokenAuditAdapter) Prepare(
	ctx context.Context,
	principal model.Principal,
	action model.Action,
	resource model.Resource,
	metadata model.RequestMetadata,
	parameters map[string]any,
	prior map[string]any,
) (*model.AuditEvent, error) {
	event, err := a.audit.PrepareCriticalAction(
		ctx, principal, action, resource, metadata, parameters, prior,
	)
	if err != nil {
		return nil, err
	}
	return event, nil
}

func invalidPersonalAccessTokenRequest(field string) error {
	return apperror.New("personal_access_token.invalid").WithField("field", field)
}

// personalAccessTokenFailure presents a failed token operation. Application
// errors from notice preparation and delegation checks are not the caller's
// concern, so they are presented as token unavailability like any other
// non-Store failure.
func personalAccessTokenFailure(resource string, err error) error {
	if _, ok := apperror.As(err); ok {
		return apperror.New("personal_access_token.unavailable").WithField("resource", resource).Wrap(err)
	}
	return apperror.StoreTable{
		Rules: []apperror.StoreRule{{Kind: apperror.StoreConflict, Entity: "personal_access_token",
			Constraint: "personal_access_tokens_maximum_per_user", Code: "personal_access_token.maximum_reached"}},
		NotFound:    "resource.not_found",
		Unavailable: "personal_access_token.unavailable",
		Fields:      map[string]string{"resource": resource},
	}.Translate(err)
}
