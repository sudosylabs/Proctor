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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type desktopCompatibilityPolicyStoreFake struct {
	policy       *model.DesktopCompatibilityPolicy
	getErr       error
	replaceInput *store.DesktopCompatibilityPolicyReplacement
	command      *store.CommandIdempotency
	result       *store.DesktopCompatibilityPolicyReplacementResult
	replaceErr   error
}

func (s *desktopCompatibilityPolicyStoreFake) Get(context.Context) (*model.DesktopCompatibilityPolicy, error) {
	return s.policy.Clone(), s.getErr
}

func (s *desktopCompatibilityPolicyStoreFake) Replace(
	_ context.Context,
	input *store.DesktopCompatibilityPolicyReplacement,
	command *store.CommandIdempotency,
) (*store.DesktopCompatibilityPolicyReplacementResult, error) {
	s.replaceInput, s.command = input, command
	return s.result, s.replaceErr
}

func desktopCompatibilityServiceFixture(
	t *testing.T,
) (*desktopCompatibilityService, *desktopCompatibilityPolicyStoreFake, *accessPolicyAuthorizationFake, *accessPolicyAuditFake, invocation.Invocation) {
	t.Helper()
	at := time.UnixMilli(10_000)
	institution, err := model.NewInstitution(
		model.NewInstitutionID(),
		"northbridge",
		"Northbridge",
		"",
		at,
	)
	require.NoError(t, err)
	policy := model.NewInitialDesktopCompatibilityPolicy(institution.ID, at)
	persistence := &desktopCompatibilityPolicyStoreFake{policy: policy}
	authorization := &accessPolicyAuthorizationFake{}
	audit := &accessPolicyAuditFake{beginID: model.NewID()}
	service, err := newDesktopCompatibilityService(desktopCompatibilityServiceDependencies{Policies: persistence, Institutions: &accessPolicyInstitutionFake{institution: institution}, Authorization: authorization, Audit: audit, Builds: []model.DesktopBuildTuple{
		testDesktopBuildTuple(
			"1.2.3",
			"darwin-current",
			model.DesktopPlatformDarwin,
			model.DesktopArchitectureARM64,
			1,
		),
	}, SkipBuildChecks: false, RecentAuthenticationTTL: time.Minute, Now: func() time.Time { return at.Add(30 * time.Second) }})
	require.NoError(t, err)
	principal := model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewID()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt:        at,
		MFACompletedAt:         model.OptionalTimeFrom(at.Add(20 * time.Second)),
		ClientType:             model.SessionClientWeb,
	}
	return service, persistence, authorization, audit,
		invocation.New(principal, model.RequestMetadata{RequestID: "request-1"})
}

func TestDesktopBuildCatalogRejectsInvalidAndAmbiguousEntries(t *testing.T) {
	t.Parallel()

	valid := testDesktopBuildTuple("1.2.3", "build-123", model.DesktopPlatformDarwin, model.DesktopArchitectureARM64, 1)
	tests := []struct {
		name    string
		entries []model.DesktopBuildTuple
	}{
		{name: "invalid", entries: []model.DesktopBuildTuple{{}}},
		{name: "duplicate build id", entries: []model.DesktopBuildTuple{valid, valid}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := newDesktopBuildCatalog(test.entries)
			require.Error(t, err, "newDesktopBuildCatalog() accepted invalid entries")
		})
	}
}

func TestDesktopCompatibilityEvaluationUsesCatalogAndInstitutionPolicy(t *testing.T) {
	t.Parallel()

	catalog, err := newDesktopBuildCatalog([]model.DesktopBuildTuple{
		testDesktopBuildTuple("1.2.3", "darwin-old", model.DesktopPlatformDarwin, model.DesktopArchitectureARM64, 2),
		testDesktopBuildTuple("1.4.0", "darwin-current", model.DesktopPlatformDarwin, model.DesktopArchitectureARM64, 3),
		testDesktopBuildTuple("1.3.0", "linux-current", model.DesktopPlatformLinux, model.DesktopArchitectureX64, 1),
	})
	require.NoError(t, err)
	policy := model.NewInitialDesktopCompatibilityPolicy(model.NewInstitutionID(), time.UnixMilli(100))
	require.NoError(t, policy.Replace(1, model.DesktopCompatibilityPolicySettings{
		MinimumDesktopRelease:  "1.2.3",
		RevokedDesktopBuildIDs: []string{"darwin-old"},
		AdministratorMessage:   "Update before the next exam.",
		Availability:           model.DesktopAvailabilityReady,
	}, time.UnixMilli(200)))

	tests := []struct {
		name              string
		query             DesktopCompatibilityQuery
		wantCompatibility DesktopCompatibilityState
		wantReason        string
	}{
		{
			name: "compatible exact tuple",
			query: DesktopCompatibilityQuery{DesktopRelease: "1.4.0", DesktopBuildID: "darwin-current",
				Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 3},
			wantCompatibility: DesktopCompatibilityCompatible,
			wantReason:        "compatible",
		},
		{
			name: "revoked exact build",
			query: DesktopCompatibilityQuery{DesktopRelease: "1.2.3", DesktopBuildID: "darwin-old",
				Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 2},
			wantCompatibility: DesktopCompatibilityUpdateRequired,
			wantReason:        "build_revoked",
		},
		{
			name: "release too old",
			query: DesktopCompatibilityQuery{DesktopRelease: "1.1.0", DesktopBuildID: "old",
				Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 2},
			wantCompatibility: DesktopCompatibilityUpdateRequired,
			wantReason:        "release_too_old",
		},
		{
			name: "release too new",
			query: DesktopCompatibilityQuery{DesktopRelease: "2.0.0", DesktopBuildID: "future",
				Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 3},
			wantCompatibility: DesktopCompatibilityServerIncompatible,
			wantReason:        "release_too_new",
		},
		{
			name: "realtime protocol too old",
			query: DesktopCompatibilityQuery{DesktopRelease: "1.4.0", DesktopBuildID: "darwin-current",
				Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 1},
			wantCompatibility: DesktopCompatibilityUpdateRequired,
			wantReason:        "realtime_protocol_too_old",
		},
		{
			name: "realtime protocol too new",
			query: DesktopCompatibilityQuery{DesktopRelease: "1.4.0", DesktopBuildID: "darwin-current",
				Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 4},
			wantCompatibility: DesktopCompatibilityServerIncompatible,
			wantReason:        "realtime_protocol_too_new",
		},
		{
			name: "unverified build",
			query: DesktopCompatibilityQuery{DesktopRelease: "1.4.0", DesktopBuildID: "darwin-rebuilt",
				Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 3},
			wantCompatibility: DesktopCompatibilityServerIncompatible,
			wantReason:        "build_unrecognized",
		},
		{
			name: "unsupported target",
			query: DesktopCompatibilityQuery{DesktopRelease: "1.4.0", DesktopBuildID: "freebsd",
				Platform: "freebsd", Architecture: "arm64", RealtimeProtocol: 3},
			wantCompatibility: DesktopCompatibilityUnsupportedTarget,
			wantReason:        "unsupported_target",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := catalog.evaluate(policy, test.query)
			require.NoError(t, err)
			require.Equal(t, test.wantCompatibility, result.Compatibility)
			require.Equal(t, test.wantReason, result.Reason)
			if test.query.Platform == "darwin" {
				require.Equal(t, "1.2.3", result.MinimumDesktopRelease)
				require.Equal(t, "1.4.0", result.MaximumDesktopRelease)
				require.Equal(t, 2, result.MinimumRealtimeProtocol)
				require.Equal(t, 3, result.MaximumRealtimeProtocol)
			}
		})
	}
}

func TestDesktopCompatibilityEvaluationRejectsMalformedSelectors(t *testing.T) {
	t.Parallel()

	catalog, err := newDesktopBuildCatalog([]model.DesktopBuildTuple{
		testDesktopBuildTuple("1.2.3", "build-123", model.DesktopPlatformDarwin, model.DesktopArchitectureARM64, 1),
	})
	require.NoError(t, err)
	policy := model.NewInitialDesktopCompatibilityPolicy(model.NewInstitutionID(), time.UnixMilli(100))
	tests := []struct {
		name  string
		query DesktopCompatibilityQuery
	}{
		{name: "release", query: DesktopCompatibilityQuery{DesktopRelease: "1.2", DesktopBuildID: "build-123", Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 1}},
		{name: "build", query: DesktopCompatibilityQuery{DesktopRelease: "1.2.3", DesktopBuildID: "build/id", Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 1}},
		{name: "platform", query: DesktopCompatibilityQuery{DesktopRelease: "1.2.3", DesktopBuildID: "build-123", Platform: "Darwin", Architecture: "arm64", RealtimeProtocol: 1}},
		{name: "architecture", query: DesktopCompatibilityQuery{DesktopRelease: "1.2.3", DesktopBuildID: "build-123", Platform: "darwin", Architecture: "arm 64", RealtimeProtocol: 1}},
		{name: "realtime", query: DesktopCompatibilityQuery{DesktopRelease: "1.2.3", DesktopBuildID: "build-123", Platform: "darwin", Architecture: "arm64", RealtimeProtocol: -1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := catalog.evaluate(policy, test.query)
			require.True(t, apperror.Is(err, "request.invalid"), "evaluate() error = %v", err)
		})
	}
}

func TestDesktopCompatibilityServiceEvaluatesAuthoritativePolicy(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, _ := desktopCompatibilityServiceFixture(t)
	require.NoError(t, persistence.policy.Replace(1, model.DesktopCompatibilityPolicySettings{
		RevokedDesktopBuildIDs: []string{"darwin-current"},
		AdministratorMessage:   "Use another verified build.",
		Availability:           model.DesktopAvailabilityReady,
	}, time.UnixMilli(11_000)))

	result, err := service.EvaluateDesktopCompatibility(context.Background(), DesktopCompatibilityQuery{
		DesktopRelease:   "1.2.3",
		DesktopBuildID:   "darwin-current",
		Platform:         "darwin",
		Architecture:     "arm64",
		RealtimeProtocol: 1,
	})
	require.NoError(t, err)
	require.Equal(t, DesktopCompatibilityUpdateRequired, result.Compatibility)
	require.Equal(t, "build_revoked", result.Reason)
	require.Equal(t, "Use another verified build.", result.AdministratorMessage)
	require.Equal(t, int64(2), result.PolicyRevision)
}

func TestDesktopCompatibilityPolicyReadUsesAdministrativeViewAuthorization(t *testing.T) {
	t.Parallel()
	service, persistence, authorization, _, invocation := desktopCompatibilityServiceFixture(t)

	policy, err := service.GetDesktopCompatibilityPolicy(context.Background(), invocation)
	require.NoError(t, err)
	require.Equal(t, model.ActionAccessPolicyView, authorization.action, "authorization")
	require.NotSame(t, persistence.policy, policy, "authorization")
	require.Equal(t, persistence.policy.InstitutionID, policy.InstitutionID, "authorization")
}

func TestDesktopCompatibilityPolicyReplacementRequiresStrongRecentSystemAdministratorSession(t *testing.T) {
	t.Parallel()
	service, persistence, authorization, audit, invocation := desktopCompatibilityServiceFixture(t)
	settings := model.DesktopCompatibilityPolicySettings{
		MinimumDesktopRelease:  "1.2.3",
		RevokedDesktopBuildIDs: []string{"darwin-old"},
		AdministratorMessage:   "Update before the exam.",
		Availability:           model.DesktopAvailabilityReady,
	}
	updated := persistence.policy.Clone()
	require.NoError(t, updated.Replace(1, settings, time.UnixMilli(11_000)))
	persistence.result = &store.DesktopCompatibilityPolicyReplacementResult{
		Policy:  updated,
		Changed: true,
	}

	result, err := service.ReplaceDesktopCompatibilityPolicy(context.Background(), invocation, ReplaceDesktopCompatibilityPolicyCommand{
		ExpectedRevision: 1,
		Settings:         settings,
		IdempotencyKey:   "desktop-policy-1",
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), result.Revision, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	require.Equal(t, model.ActionDesktopCompatibilityPolicyManage, authorization.action, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	require.NotNil(t, persistence.replaceInput, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	require.Equal(t, invocation.Principal().UserID, persistence.replaceInput.ActorID, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	require.Equal(t, model.AuditEventID(audit.beginID), persistence.replaceInput.AuditEventID, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	require.NotNil(t, persistence.command, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	require.Equal(t, "desktop_compatibility_policy.replace", persistence.command.Operation, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	require.Equal(t, model.ActionDesktopCompatibilityPolicyManage, audit.attempt.Action, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	require.Equal(t, model.RoleScopeInstitution, audit.attempt.ScopeType, "result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	encodedAudit := ""
	for key, value := range audit.attempt.Value {
		encodedAudit += key + "=" + valueString(value)
	}
	for _, secret := range append(settings.RevokedDesktopBuildIDs, settings.AdministratorMessage) {
		require.NotContains(t, encodedAudit, secret, "audit attempt exposed policy content")
	}
}

func TestDesktopCompatibilityPolicyReplacementRejectsWeakOrStaleCredentialsBeforePersistence(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, call := desktopCompatibilityServiceFixture(t)
	settings := persistence.policy.Settings()
	tests := []struct {
		name     string
		mutate   func(model.Principal) model.Principal
		wantCode string
	}{
		{
			name: "single factor",
			mutate: func(principal model.Principal) model.Principal {
				principal.AuthenticationStrength = model.AuthenticationSingleFactor
				principal.MFACompletedAt = model.OptionalTime{}
				return principal
			},
			wantCode: "authentication.strong_required",
		},
		{
			name: "stale",
			mutate: func(principal model.Principal) model.Principal {
				principal.AuthenticatedAt = time.UnixMilli(-120_000)
				principal.MFACompletedAt = model.OptionalTimeFrom(time.UnixMilli(-120_000))
				return principal
			},
			wantCode: "authentication.reauthentication_required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.ReplaceDesktopCompatibilityPolicy(
				context.Background(),
				invocation.New(test.mutate(call.Principal()), model.RequestMetadata{}),
				ReplaceDesktopCompatibilityPolicyCommand{
					ExpectedRevision: 1,
					Settings:         settings,
					IdempotencyKey:   "desktop-policy-assurance",
				},
			)
			require.True(t, apperror.Is(err, test.wantCode), "error = %v, want %s", err, test.wantCode)
		})
	}
	require.Nil(t, persistence.replaceInput, "persistence was called for a rejected assurance level")
}

func TestDesktopCompatibilityPolicyReplacementFailsClosedWhenAuditBeginFails(t *testing.T) {
	t.Parallel()
	service, persistence, _, audit, invocation := desktopCompatibilityServiceFixture(t)
	auditError := apperror.New("audit.unavailable")
	audit.beginErr = auditError

	_, err := service.ReplaceDesktopCompatibilityPolicy(context.Background(), invocation, ReplaceDesktopCompatibilityPolicyCommand{
		ExpectedRevision: 1,
		Settings:         persistence.policy.Settings(),
		IdempotencyKey:   "desktop-policy-audit-unavailable",
	})
	require.ErrorIs(t, err, auditError, "Replace() error")
	require.Nil(t, persistence.replaceInput, "persistence was called after the audit begin failed")
}

func TestDesktopCompatibilityPolicyReplacementMapsRevisionAndSystemAdministratorRaces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		storeErr error
		wantCode string
	}{
		{
			name:     "revision",
			storeErr: &store.ErrDesktopCompatibilityPolicyRevisionConflict{CurrentRevision: 4},
			wantCode: "desktop_compatibility_policy.revision_conflict",
		},
		{
			name: "system administrator changed",
			storeErr: store.NewErrConflict(
				"desktop_compatibility_policy",
				"actor_not_system_administrator",
				nil,
			),
			wantCode: "authorization.denied",
		},
		{
			name:     "store unavailable",
			storeErr: errors.New("database unavailable"),
			wantCode: "desktop_compatibility_policy.unavailable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, persistence, _, audit, invocation := desktopCompatibilityServiceFixture(t)
			persistence.replaceErr = test.storeErr
			_, err := service.ReplaceDesktopCompatibilityPolicy(context.Background(), invocation, ReplaceDesktopCompatibilityPolicyCommand{
				ExpectedRevision: 1,
				Settings:         persistence.policy.Settings(),
				IdempotencyKey:   "failed-" + test.name,
			})
			require.True(t, apperror.Is(err, test.wantCode), "error=%v fail_code", err)
			require.Equal(t, test.wantCode, audit.failCode, "error=%v fail_code", err)
		})
	}
}

func valueString(value any) string {
	return fmt.Sprint(value)
}

func testDesktopBuildTuple(
	release string,
	buildID string,
	platform model.DesktopPlatform,
	architecture model.DesktopArchitecture,
	realtimeProtocol int,
) model.DesktopBuildTuple {
	return model.DesktopBuildTuple{
		DesktopRelease:                          release,
		DesktopBuildID:                          buildID,
		Platform:                                platform,
		Architecture:                            architecture,
		RealtimeProtocol:                        realtimeProtocol,
		AttemptConfigurationManifestFingerprint: model.CurrentAttemptConfigurationManifestFingerprint(),
		DesktopSettingsRegistryFingerprint:      "fnv1a64:" + strings.Repeat("b", 16),
		DesktopTarget:                           string(platform) + "-" + string(architecture), ConfigurationManifest: model.EmptyAttemptConfigurationManifest(),
		CapabilityMatrixIdentity: "matrix-" + buildID,
	}
}

func TestDevelopmentCompatibilitySkipsBuildChecksButKeepsAvailabilityAndValidation(t *testing.T) {
	t.Parallel()
	strict, persistence, _, _, _ := desktopCompatibilityServiceFixture(t)
	development, err := newDesktopCompatibilityService(desktopCompatibilityServiceDependencies{Policies: strict.deps.Policies, Institutions: strict.deps.Institutions, Authorization: strict.deps.Authorization, Audit: strict.deps.Audit, Builds: nil, SkipBuildChecks: true, RecentAuthenticationTTL: time.Minute, Now: strict.deps.Now})
	require.NoError(t, err)
	persistence.policy.MinimumDesktopRelease = "9.0.0"
	persistence.policy.RevokedDesktopBuildIDs = []string{"dev-build"}
	for _, query := range []DesktopCompatibilityQuery{
		{DesktopRelease: "0.0.0-private", DesktopBuildID: "dev-build", Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 1},
		{DesktopRelease: "100.0.0", DesktopBuildID: "next-build", Platform: "linux", Architecture: "x64", RealtimeProtocol: 99},
	} {
		result, evaluateErr := development.EvaluateDesktopCompatibility(t.Context(), query)
		require.NoError(t, evaluateErr, "dev build rejected: %#v", result)
		require.Equal(t, DesktopCompatibilityCompatible, result.Compatibility, "dev build rejected: %#v", result)
		require.Equal(t, persistence.policy.Revision, result.PolicyRevision, "dev build rejected: %#v", result)
		require.Empty(t, result.MinimumDesktopRelease, "bypass invented supported bounds")
		require.Empty(t, result.MaximumDesktopRelease, "bypass invented supported bounds")
		require.Equal(t, 0, result.MinimumRealtimeProtocol, "bypass invented supported bounds")
		require.Equal(t, 0, result.MaximumRealtimeProtocol, "bypass invented supported bounds")
		enforced, evaluateErr := strict.EvaluateDesktopCompatibility(t.Context(), query)
		require.NoError(t, evaluateErr, "strict environment admitted build: %#v", enforced)
		require.NotEqual(t, DesktopCompatibilityCompatible, enforced.Compatibility, "strict environment admitted build: %#v", enforced)
	}
	query := DesktopCompatibilityQuery{DesktopRelease: "0.0.0-private", DesktopBuildID: "dev-build", Platform: "darwin", Architecture: "arm64", RealtimeProtocol: 1}
	persistence.policy.Availability = model.DesktopAvailabilityMaintenance
	result, err := development.EvaluateDesktopCompatibility(t.Context(), query)
	require.NoError(t, err, "maintenance ignored: %#v", result)
	require.Equal(t, model.DesktopAvailabilityMaintenance, result.Availability, "maintenance ignored: %#v", result)
	_, err = development.EvaluateDesktopCompatibility(t.Context(), DesktopCompatibilityQuery{})
	require.True(t, apperror.Is(err, "request.invalid"), "malformed selectors accepted: %v", err)
	persistence.getErr = errors.New("store offline")
	_, err = development.EvaluateDesktopCompatibility(t.Context(), query)
	require.True(t, apperror.Is(err, "desktop_compatibility_policy.unavailable"), "policy outage ignored: %v", err)
	persistence.getErr = nil
	persistence.policy.Revision = 0
	_, err = development.EvaluateDesktopCompatibility(t.Context(), query)
	require.True(t, apperror.Is(err, "desktop_compatibility_policy.unavailable"), "corrupt policy ignored: %v", err)
}

func TestDevelopmentLoginDoesNotBypassProtectedAttemptBuildChecks(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, invocation := desktopCompatibilityServiceFixture(t)
	service.deps.SkipBuildChecks = true
	principal := invocation.Principal()
	principal.ClientType = model.SessionClientDesktop
	principal.RegisteredDesktopKey = true
	principal.DesktopRegistrationID = model.NewDesktopRegistrationID()
	principal.DPoPKeyThumbprint = strings.Repeat("A", 43)
	principal.DesktopRelease = "1.2.3"
	principal.DesktopBuildID = "development"
	principal.DesktopPlatform = model.DesktopPlatformDarwin
	principal.DesktopArchitecture = model.DesktopArchitectureARM64
	principal.DesktopRealtimeProtocol = 1
	require.NoError(t, principal.Validate())
	_, err := service.ResolveAttemptDesktopBuild(t.Context(), principal)
	require.Error(t, err, "uncataloged dev build obtained Attempt authority")
	principal.DesktopBuildID = "darwin-current"
	_, err = service.ResolveAttemptDesktopBuild(t.Context(), principal)
	require.NoError(t, err)
	persistence.policy.RevokedDesktopBuildIDs = []string{principal.DesktopBuildID}
	_, err = service.ResolveAttemptDesktopBuild(t.Context(), principal)
	require.Error(t, err, "revoked build obtained Attempt authority in dev")
}
