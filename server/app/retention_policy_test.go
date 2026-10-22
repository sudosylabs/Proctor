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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRetentionPolicyReadAuthorizesExactInstitutionAndReturnsCopy(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	application := &App{retentionPolicyService: fixture.service}
	result, err := application.GetRetentionPolicy(t.Context(), fixture.invocation)
	require.NoError(t, err, "GetRetentionPolicy")
	require.NotNil(t, result, "GetRetentionPolicy")
	require.Equal(t, fixture.policies.policy, result, "GetRetentionPolicy")
	require.Equal(t, model.ActionRetentionPolicyView, fixture.authorization.action, "authorized")
	require.Equal(t, model.Resource{Type: model.ResourceInstitution, ID: fixture.institution.ID.String()}, fixture.authorization.resource, "authorized")
	result.SubmissionRetentionDays = 7
	require.Equal(t, 0, fixture.policies.policy.SubmissionRetentionDays, "read result shares mutable Store policy")
	fixture.authorization.err = apperror.New("authorization.denied")
	fixture.policies.getCalls = 0
	result, err = application.GetRetentionPolicy(t.Context(), fixture.invocation)
	require.Nil(t, result, "denied read = %#v, %v; Store calls", result, err)
	require.True(t, apperror.Is(err, "authorization.denied"), "denied read = %#v, %v; Store calls", result, err)
	require.Equal(t, 0, fixture.policies.getCalls, "denied read = %#v, %v; Store calls", result, err)
}

func TestRetentionPolicyReadRejectsUnavailableOrForeignFacts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		alter func(*retentionPolicyFixture)
	}{
		{"missing Institution", func(f *retentionPolicyFixture) { f.service.institutions = &accessPolicyInstitutionFake{} }},
		{"missing policy", func(f *retentionPolicyFixture) { f.policies.policy = nil }},
		{"Store failure", func(f *retentionPolicyFixture) { f.policies.getErr = errors.New("unavailable") }},
		{"invalid policy", func(f *retentionPolicyFixture) { f.policies.policy.Revision = 0 }},
		{"foreign policy", func(f *retentionPolicyFixture) { f.policies.policy.InstitutionID = model.NewInstitutionID() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRetentionPolicyFixture(t)
			test.alter(&fixture)
			result, err := fixture.service.GetRetentionPolicy(t.Context(), fixture.invocation)
			require.Nil(t, result, "read = %#v, %v", result, err)
			require.True(t, apperror.Is(err, "retention_policy.unavailable"), "read = %#v, %v", result, err)
		})
	}
}

func TestRetentionPolicyReplacementRequiresStrongRecentSessionBeforeWork(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		alter func(*model.Principal)
		code  string
	}{
		{"PAT", func(p *model.Principal) { p.CredentialType, p.SessionID = model.CredentialPersonalAccessToken, "" }, "authentication.token_invalid"},
		{"single factor", func(p *model.Principal) {
			p.AuthenticationStrength, p.MFACompletedAt = model.AuthenticationSingleFactor, model.OptionalTime{}
		}, "authentication.strong_required"},
		{"expired assurance", func(p *model.Principal) {
			p.AuthenticatedAt, p.MFACompletedAt = time.Unix(1, 0), model.OptionalTimeFrom(time.Unix(1, 0))
		}, "authentication.reauthentication_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRetentionPolicyFixture(t)
			principal := fixture.invocation.Principal()
			test.alter(&principal)
			result, err := fixture.service.ReplaceRetentionPolicy(t.Context(), NewInvocation(principal, model.RequestMetadata{}), retentionPolicyCommand())
			require.Nil(t, result, "unassured replacement = %#v, %v; authorization calls", result, err)
			require.True(t, apperror.Is(err, test.code), "unassured replacement = %#v, %v; authorization calls", result, err)
			require.Equal(t, 0, fixture.authorization.calls, "unassured replacement = %#v, %v; authorization calls", result, err)
			require.Nil(t, fixture.policies.replaceInput, "unassured replacement = %#v, %v; authorization calls", result, err)
			require.Empty(t, fixture.audit.attempt.Operation, "unassured replacement = %#v, %v; authorization calls", result, err)
		})
	}
}

func TestRetentionPolicyReplacementRejectsInvalidIntentBeforeAuditAndStore(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name  string
		alter func(*retentionPolicyFixture, *ReplaceRetentionPolicyCommand)
		code  string
	}{
		{"authorization denied", func(f *retentionPolicyFixture, _ *ReplaceRetentionPolicyCommand) {
			f.authorization.err = apperror.New("authorization.denied")
		}, "authorization.denied"},
		{"missing revision", func(_ *retentionPolicyFixture, c *ReplaceRetentionPolicyCommand) { c.ExpectedRevision = 0 }, "retention_policy.invalid"},
		{"negative period", func(_ *retentionPolicyFixture, c *ReplaceRetentionPolicyCommand) {
			c.Settings.SubmissionRetentionDays = -1
		}, "retention_policy.invalid"},
		{"unbounded period", func(_ *retentionPolicyFixture, c *ReplaceRetentionPolicyCommand) {
			c.Settings.DeletionGraceDays = 36501
		}, "retention_policy.invalid"},
		{"export beyond ceiling", func(_ *retentionPolicyFixture, c *ReplaceRetentionPolicyCommand) { c.Settings.ExportRetentionDays = 8 }, "retention_policy.invalid"},
		{"missing idempotency", func(_ *retentionPolicyFixture, c *ReplaceRetentionPolicyCommand) { c.IdempotencyKey = "" }, "idempotency.key_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRetentionPolicyFixture(t)
			command := retentionPolicyCommand()
			test.alter(&fixture, &command)
			result, err := fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, command)
			require.Nil(t, result, "invalid replacement = %#v, %v", result, err)
			require.True(t, apperror.Is(err, test.code), "invalid replacement = %#v, %v", result, err)
			require.Nil(t, fixture.policies.replaceInput, "invalid replacement = %#v, %v", result, err)
			require.Empty(t, fixture.audit.attempt.Operation, "invalid replacement = %#v, %v", result, err)
		})
	}
}

func TestRetentionPolicyReplacementPassesAtomicAuditAndIdempotency(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	command := retentionPolicyCommand()
	next := fixture.policies.policy.Clone()
	require.NoError(t, next.Replace(1, command.Settings, fixture.at))
	fixture.policies.result = &store.RetentionPolicyReplacementResult{Policy: next, Changed: true}
	application := &App{retentionPolicyService: fixture.service}
	result, err := application.ReplaceRetentionPolicy(t.Context(), fixture.invocation, command)
	require.NoError(t, err, "replace")
	require.NotNil(t, result, "replace")
	require.Equal(t, next, result, "replace")
	resource := model.Resource{Type: model.ResourceInstitution, ID: fixture.institution.ID.String()}
	require.Equal(t, model.ActionRetentionPolicyManage, fixture.authorization.action)
	require.Equal(t, resource, fixture.authorization.resource)
	require.Equal(t, model.ActionRetentionPolicyManage, fixture.audit.attempt.Action)
	require.Equal(t, resource, fixture.audit.attempt.Resource)
	require.Equal(t, model.RoleScopeInstitution, fixture.audit.attempt.ScopeType)
	require.Equal(t, fixture.institution.ID.String(), fixture.audit.attempt.ScopeID)
	require.Equal(t, "replace", fixture.audit.attempt.Operation)
	require.Equal(t, false, fixture.audit.attempt.Value["candidate_notices"])
	input, idempotency := fixture.policies.replaceInput, fixture.policies.command
	require.NotNil(t, input, "atomic input")
	require.Equal(t, fixture.invocation.Principal(), input.Principal, "atomic input")
	require.Equal(t, fixture.service.recentAuthenticationTTL, input.RecentAuthenticationTTL, "atomic input")
	require.Equal(t, int64(1), input.ExpectedRevision, "atomic input")
	require.Equal(t, command.Settings, input.Settings, "atomic input")
	require.Equal(t, model.AuditEventID(fixture.audit.beginID), input.AuditEventID, "atomic input")
	require.Equal(t, fixture.at.UnixMilli(), input.AuditAt, "atomic input")
	require.NotNil(t, idempotency, "atomic input")
	require.Equal(t, input.Principal.UserID, idempotency.UserID, "atomic input")
	require.Equal(t, "retention_policy.replace", idempotency.Operation, "atomic input")
	result.AuditRetentionDays = 1
	require.Equal(t, command.Settings.AuditRetentionDays, next.AuditRetentionDays, "replacement result shares mutable Store policy")
}

func TestRetentionPolicyReplacementReplayAndNoOpUseStoreOutcome(t *testing.T) {
	t.Parallel()
	for _, replay := range []bool{false, true} {
		fixture := newRetentionPolicyFixture(t)
		retained := fixture.policies.policy.Clone()
		if replay {
			retained.Revision = 2
		}
		fixture.policies.result = &store.RetentionPolicyReplacementResult{Policy: retained, Replayed: replay}
		command := retentionPolicyCommand()
		command.Settings = model.RetentionPolicySettings{}
		result, err := fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, command)
		require.NoError(t, err, "replayed=%t: result", replay)
		require.NotNil(t, result, "replayed=%t: result", replay)
		require.Equal(t, retained, result, "replayed=%t: result", replay)
		require.Equal(t, 0, fixture.policies.getCalls, "replayed=%t: result", replay)
		require.Equal(t, model.AuditEventID(fixture.audit.beginID), fixture.policies.replaceInput.AuditEventID, "replayed=%t: result", replay)
		require.Equal(t, 1, fixture.authorization.calls, "replayed=%t: result", replay)
	}
}

func TestRetentionPolicyReplacementFingerprintIncludesInstitutionSettingsAndRevision(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	command := retentionPolicyCommand()
	_, err := fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, command)
	require.NoError(t, err)
	original := *fixture.policies.command
	for _, change := range []func(*ReplaceRetentionPolicyCommand){
		func(c *ReplaceRetentionPolicyCommand) { c.ExpectedRevision++ },
		func(c *ReplaceRetentionPolicyCommand) { c.Settings.SubmissionRetentionDays++ },
		func(c *ReplaceRetentionPolicyCommand) { c.Settings.IntegrityRetentionDays++ },
		func(c *ReplaceRetentionPolicyCommand) { c.Settings.AuditRetentionDays++ },
		func(c *ReplaceRetentionPolicyCommand) { c.Settings.ExportRetentionDays++ },
		func(c *ReplaceRetentionPolicyCommand) { c.Settings.DeletionGraceDays++ },
	} {
		candidate := command
		change(&candidate)
		_, err = fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, candidate)
		require.NoError(t, err)
		require.Equal(t, original.KeyDigest, fixture.policies.command.KeyDigest, "a different replacement can replay the same retained outcome")
		require.NotEqual(t, original.Fingerprint, fixture.policies.command.Fingerprint, "a different replacement can replay the same retained outcome")
	}
	fixture.institution.ID = model.NewInstitutionID()
	fixture.policies.result.Policy.InstitutionID = fixture.institution.ID
	_, err = fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, command)
	require.NoError(t, err)
	require.Equal(t, original.KeyDigest, fixture.policies.command.KeyDigest, "a replacement Institution can replay the previous Institution's outcome")
	require.NotEqual(t, original.Fingerprint, fixture.policies.command.Fingerprint, "a replacement Institution can replay the previous Institution's outcome")
}

func TestRetentionPolicyReplacementMapsFailuresAndTerminalizesAudit(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{"revision", &store.ErrRetentionPolicyRevisionConflict{CurrentRevision: 7}, "retention_policy.revision_conflict"},
		{"administrator revoked", &store.ErrConflict{Resource: "authorization", Constraint: "authority"}, "authorization.denied"},
		{"credential revoked", &store.ErrConflict{Resource: "authorization", Constraint: "credential"}, "authorization.denied"},
		{"assurance expired", &store.ErrConflict{Resource: "authorization", Constraint: "assurance"}, "authorization.denied"},
		{"idempotency conflict", &store.ErrIdempotencyConflict{}, "idempotency.conflict"},
		{"idempotency busy", &store.ErrIdempotencyInProgress{}, "idempotency.in_progress"},
		{"Store unavailable", errors.New("database unavailable"), "retention_policy.unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRetentionPolicyFixture(t)
			fixture.policies.replaceErr = test.err
			result, err := fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, retentionPolicyCommand())
			require.Nil(t, result, "failure = %#v, %v; audit = %s", result, err, fixture.audit.failID)
			require.True(t, apperror.Is(err, test.code), "failure = %#v, %v; audit = %s", result, err, fixture.audit.failID)
			require.Equal(t, fixture.audit.beginID, fixture.audit.failID, "failure = %#v, %v; audit = %s", result, err, fixture.audit.failID)
			require.Equal(t, test.code, fixture.audit.failCode, "failure = %#v, %v; audit = %s", result, err, fixture.audit.failID)
			if test.name == "revision" {
				failure, ok := apperror.As(err)
				require.True(t, ok)
				require.Equal(t, "7", failure.Fields()["current_revision"])
			}
		})
	}
}

func TestRetentionPolicyReplacementFailsClosedWhenAuditFails(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	auditFailure := apperror.New("audit.unavailable")
	fixture.audit.beginErr = auditFailure
	result, err := fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, retentionPolicyCommand())
	require.Nil(t, result, "failed audit preparation")
	require.ErrorIs(t, err, auditFailure, "failed audit preparation")
	require.Nil(t, fixture.policies.replaceInput, "failed audit preparation")
	fixture.audit.beginErr, fixture.audit.failErr = nil, auditFailure
	fixture.policies.replaceErr = &store.ErrRetentionPolicyRevisionConflict{CurrentRevision: 7}
	result, err = fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, retentionPolicyCommand())
	require.Nil(t, result, "failed terminal audit")
	require.ErrorIs(t, err, auditFailure, "failed terminal audit")
}

func TestRetentionPolicyReplacementRejectsMalformedOrForeignOutcome(t *testing.T) {
	t.Parallel()
	for _, result := range []*store.RetentionPolicyReplacementResult{
		nil, {}, {Policy: &model.RetentionPolicy{}},
		{Policy: model.NewInitialRetentionPolicy(model.NewInstitutionID(), time.Now())},
	} {
		fixture := newRetentionPolicyFixture(t)
		fixture.policies.result = result
		returned, err := fixture.service.ReplaceRetentionPolicy(t.Context(), fixture.invocation, retentionPolicyCommand())
		require.Nil(t, returned, "malformed Store outcome returned %#v, %v", returned, err)
		require.True(t, apperror.Is(err, "retention_policy.unavailable"), "malformed Store outcome returned %#v, %v", returned, err)
	}
}

func TestRetentionPolicyConstructionRejectsIncompleteDependencies(t *testing.T) {
	t.Parallel()
	for _, omit := range []func(*retentionPolicyService){
		func(s *retentionPolicyService) { s.policies = nil },
		func(s *retentionPolicyService) { s.lifecycle = nil },
		func(s *retentionPolicyService) { s.institutions = nil },
		func(s *retentionPolicyService) { s.authorization = nil },
		func(s *retentionPolicyService) { s.audit = nil },
		func(s *retentionPolicyService) { s.recentAuthenticationTTL = 0 },
		func(s *retentionPolicyService) { s.now = nil },
	} {
		fixture := newRetentionPolicyFixture(t)
		candidate := *fixture.service
		omit(&candidate)
		_, err := newRetentionPolicyService(candidate.policies, candidate.lifecycle, candidate.institutions, candidate.authorization,
			candidate.audit, candidate.recentAuthenticationTTL, candidate.now)
		require.Error(t, err, "incomplete retention policy construction succeeded")
	}
}

type retentionPolicyFixture struct {
	service       *retentionPolicyService
	policies      *retentionPolicyStoreFake
	authorization *retentionPolicyAuthorizationFake
	audit         *accessPolicyAuditFake
	institution   *model.Institution
	invocation    Invocation
	at            time.Time
}

func newRetentionPolicyFixture(t *testing.T) retentionPolicyFixture {
	t.Helper()
	at := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	institution := &model.Institution{ID: model.NewInstitutionID()}
	policy := model.NewInitialRetentionPolicy(institution.ID, at.Add(-time.Hour))
	policies := &retentionPolicyStoreFake{policy: policy, result: &store.RetentionPolicyReplacementResult{Policy: policy}}
	authorization := &retentionPolicyAuthorizationFake{}
	audit := &accessPolicyAuditFake{beginID: model.NewAuditEventID().String()}
	service, err := newRetentionPolicyService(policies, &retentionLifecycleFake{}, &accessPolicyInstitutionFake{institution: institution}, authorization, audit, time.Minute,
		func() time.Time { return at })
	require.NoError(t, err)
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt: at, MFACompletedAt: model.OptionalTimeFrom(at), ClientType: model.SessionClientWeb}
	return retentionPolicyFixture{service: service, policies: policies, authorization: authorization, audit: audit, institution: institution,
		invocation: NewInvocation(principal, model.RequestMetadata{RequestID: "retention-policy"}), at: at}
}

func retentionPolicyCommand() ReplaceRetentionPolicyCommand {
	return ReplaceRetentionPolicyCommand{ExpectedRevision: 1, IdempotencyKey: "retention-edit",
		Settings: model.RetentionPolicySettings{SubmissionRetentionDays: 365, IntegrityRetentionDays: 90, AuditRetentionDays: 730, ExportRetentionDays: 6, DeletionGraceDays: 30}}
}

type retentionPolicyStoreFake struct {
	policy       *model.RetentionPolicy
	getCalls     int
	getErr       error
	replaceInput *store.RetentionPolicyReplacement
	command      *store.CommandIdempotency
	result       *store.RetentionPolicyReplacementResult
	replaceErr   error
}

func (s *retentionPolicyStoreFake) Get(context.Context) (*model.RetentionPolicy, error) {
	s.getCalls++
	return s.policy, s.getErr
}

func (s *retentionPolicyStoreFake) Replace(_ context.Context, input *store.RetentionPolicyReplacement, command *store.CommandIdempotency) (*store.RetentionPolicyReplacementResult, error) {
	s.replaceInput, s.command = input, command
	return s.result, s.replaceErr
}

type retentionPolicyAuthorizationFake struct {
	action   model.Action
	resource model.Resource
	calls    int
	err      error
}

func (a *retentionPolicyAuthorizationFake) Authorize(_ context.Context, _ Invocation, action model.Action, resource model.Resource) error {
	a.action, a.resource = action, resource
	a.calls++
	return a.err
}
