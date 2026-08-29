// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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
) (*desktopCompatibilityService, *desktopCompatibilityPolicyStoreFake, *accessPolicyAuthorizationFake, *accessPolicyAuditFake, Invocation) {
	t.Helper()
	at := time.UnixMilli(10_000)
	institution, err := model.NewInstitution(
		model.NewInstitutionID(),
		"northbridge",
		"Northbridge",
		"",
		at,
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := model.NewInitialDesktopCompatibilityPolicy(institution.ID, at)
	persistence := &desktopCompatibilityPolicyStoreFake{policy: policy}
	authorization := &accessPolicyAuthorizationFake{}
	audit := &accessPolicyAuditFake{beginID: model.NewId()}
	service, err := newDesktopCompatibilityService(
		persistence,
		&accessPolicyInstitutionFake{institution: institution},
		authorization,
		audit,
		[]model.DesktopBuildTuple{
			testDesktopBuildTuple(
				"1.2.3",
				"darwin-current",
				model.DesktopPlatformDarwin,
				model.DesktopArchitectureARM64,
				1,
			),
		},
		time.Minute,
		func() time.Time { return at.Add(30 * time.Second) },
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := model.Principal{
		UserID:                 model.NewUserID(),
		SessionID:              model.NewSessionID(),
		CredentialID:           model.PrincipalCredentialID(model.NewId()),
		CredentialType:         model.CredentialSessionAccess,
		AuthenticationMethod:   "password",
		AuthenticationStrength: model.AuthenticationMultiFactor,
		AuthenticatedAt:        at,
		MFACompletedAt:         model.OptionalTimeFrom(at.Add(20 * time.Second)),
		ClientType:             model.SessionClientWeb,
	}
	return service, persistence, authorization, audit,
		NewInvocation(principal, model.RequestMetadata{RequestID: "request-1"})
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
			if _, err := newDesktopBuildCatalog(test.entries); err == nil {
				t.Fatal("newDesktopBuildCatalog() accepted invalid entries")
			}
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
	if err != nil {
		t.Fatal(err)
	}
	policy := model.NewInitialDesktopCompatibilityPolicy(model.NewInstitutionID(), time.UnixMilli(100))
	if err := policy.Replace(1, model.DesktopCompatibilityPolicySettings{
		MinimumDesktopRelease:  "1.2.3",
		RevokedDesktopBuildIDs: []string{"darwin-old"},
		AdministratorMessage:   "Update before the next exam.",
		Availability:           model.DesktopAvailabilityReady,
	}, time.UnixMilli(200)); err != nil {
		t.Fatal(err)
	}

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
			if err != nil {
				t.Fatal(err)
			}
			if result.Compatibility != test.wantCompatibility || result.Reason != test.wantReason {
				t.Fatalf("evaluate() = %#v", result)
			}
			if test.query.Platform == "darwin" &&
				(result.MinimumDesktopRelease != "1.2.3" || result.MaximumDesktopRelease != "1.4.0" ||
					result.MinimumRealtimeProtocol != 2 || result.MaximumRealtimeProtocol != 3) {
				t.Fatalf("Darwin bounds = %#v", result)
			}
		})
	}
}

func TestDesktopCompatibilityEvaluationRejectsMalformedSelectors(t *testing.T) {
	t.Parallel()

	catalog, err := newDesktopBuildCatalog([]model.DesktopBuildTuple{
		testDesktopBuildTuple("1.2.3", "build-123", model.DesktopPlatformDarwin, model.DesktopArchitectureARM64, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
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
			if _, err := catalog.evaluate(policy, test.query); !Is(err, "request.invalid") {
				t.Fatalf("evaluate() error = %v", err)
			}
		})
	}
}

func TestDesktopCompatibilityServiceEvaluatesAuthoritativePolicy(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, _ := desktopCompatibilityServiceFixture(t)
	if err := persistence.policy.Replace(1, model.DesktopCompatibilityPolicySettings{
		RevokedDesktopBuildIDs: []string{"darwin-current"},
		AdministratorMessage:   "Use another verified build.",
		Availability:           model.DesktopAvailabilityReady,
	}, time.UnixMilli(11_000)); err != nil {
		t.Fatal(err)
	}

	result, err := service.Evaluate(context.Background(), DesktopCompatibilityQuery{
		DesktopRelease:   "1.2.3",
		DesktopBuildID:   "darwin-current",
		Platform:         "darwin",
		Architecture:     "arm64",
		RealtimeProtocol: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Compatibility != DesktopCompatibilityUpdateRequired ||
		result.Reason != "build_revoked" ||
		result.AdministratorMessage != "Use another verified build." || result.PolicyRevision != 2 {
		t.Fatalf("evaluation = %#v", result)
	}
}

func TestDesktopCompatibilityPolicyReadUsesAdministrativeViewAuthorization(t *testing.T) {
	t.Parallel()
	service, persistence, authorization, _, invocation := desktopCompatibilityServiceFixture(t)

	policy, err := service.Read(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if authorization.action != model.ActionAccessPolicyView || policy == persistence.policy ||
		policy.InstitutionID != persistence.policy.InstitutionID {
		t.Fatalf("authorization=%q policy=%#v", authorization.action, policy)
	}
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
	if err := updated.Replace(1, settings, time.UnixMilli(11_000)); err != nil {
		t.Fatal(err)
	}
	persistence.result = &store.DesktopCompatibilityPolicyReplacementResult{
		Policy:  updated,
		Changed: true,
	}

	result, err := service.Replace(context.Background(), invocation, ReplaceDesktopCompatibilityPolicyCommand{
		ExpectedRevision: 1,
		Settings:         settings,
		IdempotencyKey:   "desktop-policy-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 2 || authorization.action != model.ActionDesktopCompatibilityPolicyManage ||
		persistence.replaceInput == nil || persistence.replaceInput.ActorID != invocation.Principal().UserID ||
		persistence.replaceInput.AuditEventID != audit.beginID || persistence.command == nil ||
		persistence.command.Operation != "desktop_compatibility_policy.replace.v1" ||
		audit.attempt.Action != model.ActionDesktopCompatibilityPolicyManage ||
		audit.attempt.ScopeType != model.RoleScopeInstitution {
		t.Fatalf("result=%#v input=%#v command=%#v audit=%#v", result, persistence.replaceInput, persistence.command, audit.attempt)
	}
	encodedAudit := ""
	for key, value := range audit.attempt.Value {
		encodedAudit += key + "=" + valueString(value)
	}
	for _, secret := range append(settings.RevokedDesktopBuildIDs, settings.AdministratorMessage) {
		if strings.Contains(encodedAudit, secret) {
			t.Fatalf("audit attempt exposed policy content %q: %s", secret, encodedAudit)
		}
	}
}

func TestDesktopCompatibilityPolicyReplacementRejectsWeakOrStaleCredentialsBeforePersistence(t *testing.T) {
	t.Parallel()
	service, persistence, _, _, invocation := desktopCompatibilityServiceFixture(t)
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
			_, err := service.Replace(
				context.Background(),
				NewInvocation(test.mutate(invocation.Principal()), model.RequestMetadata{}),
				ReplaceDesktopCompatibilityPolicyCommand{
					ExpectedRevision: 1,
					Settings:         settings,
					IdempotencyKey:   "desktop-policy-assurance",
				},
			)
			if !Is(err, test.wantCode) {
				t.Fatalf("error = %v, want %s", err, test.wantCode)
			}
		})
	}
	if persistence.replaceInput != nil {
		t.Fatal("persistence was called for a rejected assurance level")
	}
}

func TestDesktopCompatibilityPolicyReplacementFailsClosedWhenAuditBeginFails(t *testing.T) {
	t.Parallel()
	service, persistence, _, audit, invocation := desktopCompatibilityServiceFixture(t)
	auditError := NewError("audit.unavailable")
	audit.beginErr = auditError

	_, err := service.Replace(context.Background(), invocation, ReplaceDesktopCompatibilityPolicyCommand{
		ExpectedRevision: 1,
		Settings:         persistence.policy.Settings(),
		IdempotencyKey:   "desktop-policy-audit-unavailable",
	})
	if !errors.Is(err, auditError) {
		t.Fatalf("Replace() error = %v, want audit failure", err)
	}
	if persistence.replaceInput != nil {
		t.Fatal("persistence was called after the audit begin failed")
	}
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
			_, err := service.Replace(context.Background(), invocation, ReplaceDesktopCompatibilityPolicyCommand{
				ExpectedRevision: 1,
				Settings:         persistence.policy.Settings(),
				IdempotencyKey:   "failed-" + test.name,
			})
			if !Is(err, test.wantCode) || audit.failCode != test.wantCode {
				t.Fatalf("error=%v fail_code=%q", err, audit.failCode)
			}
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
		DesktopSettingsRegistryFingerprint:      "sha256:" + strings.Repeat("b", 64),
		CapabilityMatrixIdentity:                "matrix-" + buildID,
	}
}
