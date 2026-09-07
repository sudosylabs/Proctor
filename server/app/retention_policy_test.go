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
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestRetentionPolicyReadAuthorizesExactInstitutionAndReturnsCopy(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	application := &App{retentionPolicy: fixture.service}
	result, err := application.GetRetentionPolicy(t.Context(), fixture.invocation)
	if err != nil || result == nil || !reflect.DeepEqual(result, fixture.policies.policy) {
		t.Fatalf("GetRetentionPolicy = %#v, %v", result, err)
	}
	if fixture.authorization.action != model.ActionRetentionPolicyView ||
		fixture.authorization.resource != (model.Resource{Type: model.ResourceInstitution, ID: fixture.institution.ID.String()}) {
		t.Fatalf("authorized %s on %#v", fixture.authorization.action, fixture.authorization.resource)
	}
	result.SubmissionRetentionDays = 7
	if fixture.policies.policy.SubmissionRetentionDays != 0 {
		t.Fatal("read result shares mutable Store policy")
	}
	fixture.authorization.err = NewError("authorization.denied")
	fixture.policies.getCalls = 0
	result, err = application.GetRetentionPolicy(t.Context(), fixture.invocation)
	if result != nil || !Is(err, "authorization.denied") || fixture.policies.getCalls != 0 {
		t.Fatalf("denied read = %#v, %v; Store calls = %d", result, err, fixture.policies.getCalls)
	}
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
			result, err := fixture.service.Read(t.Context(), fixture.invocation)
			if result != nil || !Is(err, "retention_policy.unavailable") {
				t.Fatalf("read = %#v, %v", result, err)
			}
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
		{"PAT", func(p *model.Principal) { p.CredentialType, p.SessionID = model.CredentialPersonalAccessToken, "" }, "authentication.invalid_token"},
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
			result, err := fixture.service.Replace(t.Context(), NewInvocation(principal, model.RequestMetadata{}), retentionPolicyCommand())
			if result != nil || !Is(err, test.code) || fixture.authorization.calls != 0 || fixture.policies.replaceInput != nil || fixture.audit.attempt.Operation != "" {
				t.Fatalf("unassured replacement = %#v, %v; authorization calls = %d", result, err, fixture.authorization.calls)
			}
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
			f.authorization.err = NewError("authorization.denied")
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
			result, err := fixture.service.Replace(t.Context(), fixture.invocation, command)
			if result != nil || !Is(err, test.code) || fixture.policies.replaceInput != nil || fixture.audit.attempt.Operation != "" {
				t.Fatalf("invalid replacement = %#v, %v", result, err)
			}
		})
	}
}

func TestRetentionPolicyReplacementPassesAtomicAuditAndIdempotency(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	command := retentionPolicyCommand()
	next := fixture.policies.policy.Clone()
	if err := next.Replace(1, command.Settings, fixture.at); err != nil {
		t.Fatal(err)
	}
	fixture.policies.result = &store.RetentionPolicyReplacementResult{Policy: next, Changed: true}
	application := &App{retentionPolicy: fixture.service}
	result, err := application.ReplaceRetentionPolicy(t.Context(), fixture.invocation, command)
	if err != nil || result == nil || !reflect.DeepEqual(result, next) {
		t.Fatalf("replace = %#v, %v", result, err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: fixture.institution.ID.String()}
	if fixture.authorization.action != model.ActionRetentionPolicyManage || fixture.authorization.resource != resource ||
		fixture.audit.attempt.Action != model.ActionRetentionPolicyManage || fixture.audit.attempt.Resource != resource ||
		fixture.audit.attempt.ScopeType != model.RoleScopeInstitution || fixture.audit.attempt.ScopeID != fixture.institution.ID.String() ||
		fixture.audit.attempt.Operation != "replace" || fixture.audit.attempt.Value["candidate_notices"] != false {
		t.Fatalf("replacement audit = %#v", fixture.audit.attempt)
	}
	input, idempotency := fixture.policies.replaceInput, fixture.policies.command
	if input == nil || !reflect.DeepEqual(input.Principal, fixture.invocation.Principal()) || input.RecentAuthenticationTTL != fixture.service.recentAuthenticationTTL || input.ExpectedRevision != 1 ||
		input.Settings != command.Settings || input.AuditEventID != fixture.audit.beginID || input.AuditAt != fixture.at.UnixMilli() ||
		idempotency == nil || idempotency.UserID != input.Principal.UserID || idempotency.Operation != "retention_policy.replace.v1" ||
		idempotency.FingerprintVersion != 1 || idempotency.OutcomeVersion != 1 {
		t.Fatalf("atomic input = %#v; idempotency = %#v", input, idempotency)
	}
	result.AuditRetentionDays = 1
	if next.AuditRetentionDays != command.Settings.AuditRetentionDays {
		t.Fatal("replacement result shares mutable Store policy")
	}
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
		result, err := fixture.service.Replace(t.Context(), fixture.invocation, command)
		if err != nil || result == nil || !reflect.DeepEqual(result, retained) || fixture.policies.getCalls != 0 ||
			fixture.policies.replaceInput.AuditEventID != fixture.audit.beginID || fixture.authorization.calls != 1 {
			t.Fatalf("replayed=%t: result=%#v, %v", replay, result, err)
		}
	}
}

func TestRetentionPolicyReplacementFingerprintIncludesInstitutionSettingsAndRevision(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	command := retentionPolicyCommand()
	if _, err := fixture.service.Replace(t.Context(), fixture.invocation, command); err != nil {
		t.Fatal(err)
	}
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
		if _, err := fixture.service.Replace(t.Context(), fixture.invocation, candidate); err != nil {
			t.Fatal(err)
		}
		if fixture.policies.command.KeyDigest != original.KeyDigest || fixture.policies.command.Fingerprint == original.Fingerprint {
			t.Fatal("a different replacement can replay the same retained outcome")
		}
	}
	fixture.institution.ID = model.NewInstitutionID()
	fixture.policies.result.Policy.InstitutionID = fixture.institution.ID
	if _, err := fixture.service.Replace(t.Context(), fixture.invocation, command); err != nil {
		t.Fatal(err)
	}
	if fixture.policies.command.KeyDigest != original.KeyDigest || fixture.policies.command.Fingerprint == original.Fingerprint {
		t.Fatal("a replacement Institution can replay the previous Institution's outcome")
	}
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
			result, err := fixture.service.Replace(t.Context(), fixture.invocation, retentionPolicyCommand())
			if result != nil || !Is(err, test.code) || fixture.audit.failID != fixture.audit.beginID || fixture.audit.failCode != test.code {
				t.Fatalf("failure = %#v, %v; audit = %s/%s", result, err, fixture.audit.failID, fixture.audit.failCode)
			}
			if test.name == "revision" {
				failure, ok := As(err)
				if !ok || failure.Fields()["current_revision"] != "7" {
					t.Fatalf("revision conflict omitted current revision: %v", err)
				}
			}
		})
	}
}

func TestRetentionPolicyReplacementFailsClosedWhenAuditFails(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	auditFailure := NewError("audit.unavailable")
	fixture.audit.beginErr = auditFailure
	if result, err := fixture.service.Replace(t.Context(), fixture.invocation, retentionPolicyCommand()); result != nil ||
		!errors.Is(err, auditFailure) || fixture.policies.replaceInput != nil {
		t.Fatalf("failed audit preparation = %#v, %v", result, err)
	}
	fixture.audit.beginErr, fixture.audit.failErr = nil, auditFailure
	fixture.policies.replaceErr = &store.ErrRetentionPolicyRevisionConflict{CurrentRevision: 7}
	if result, err := fixture.service.Replace(t.Context(), fixture.invocation, retentionPolicyCommand()); result != nil || !errors.Is(err, auditFailure) {
		t.Fatalf("failed terminal audit = %#v, %v", result, err)
	}
}

func TestRetentionPolicyReplacementRejectsMalformedOrForeignOutcome(t *testing.T) {
	t.Parallel()
	for _, result := range []*store.RetentionPolicyReplacementResult{
		nil, {}, {Policy: &model.RetentionPolicy{}},
		{Policy: model.NewInitialRetentionPolicy(model.NewInstitutionID(), time.Now())},
	} {
		fixture := newRetentionPolicyFixture(t)
		fixture.policies.result = result
		if returned, err := fixture.service.Replace(t.Context(), fixture.invocation, retentionPolicyCommand()); returned != nil || !Is(err, "retention_policy.unavailable") {
			t.Fatalf("malformed Store outcome returned %#v, %v", returned, err)
		}
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
		if _, err := newRetentionPolicyService(candidate.policies, candidate.lifecycle, candidate.institutions, candidate.authorization,
			candidate.audit, candidate.recentAuthenticationTTL, candidate.now); err == nil {
			t.Fatal("incomplete retention policy construction succeeded")
		}
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
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{UserID: model.NewUserID(), SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
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
