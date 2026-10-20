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

	"github.com/sudosylabs/proctor/server/app/apperror"
	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type PrepareSecurityPreflightCommand struct {
	SittingID                        model.ExamSittingID
	AttemptID                        model.ExamAttemptID
	NativeRegistryDigest             string
	SourceManifestDigest             string
	ConfigurationManifestFingerprint string
	IdempotencyKey                   string
}
type ReportSecurityPreflightCommand struct {
	PreflightID    string
	Report         model.SecurityPreflightReport
	IdempotencyKey string
}

func (service *Service) securityPreflightAccess(ctx context.Context, invocation invocation.Invocation, sittingID model.ExamSittingID) (store.SecurityPreflightAccess, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return store.SecurityPreflightAccess{}, apperror.New("authentication.token_invalid")
	}
	if !principal.HasRegisteredDesktopKey() {
		return store.SecurityPreflightAccess{}, apperror.New("exam.attempt.registered_desktop_required")
	}
	build, err := service.deps.DesktopBuilds.ResolveAttemptDesktopBuild(ctx, principal)
	if err != nil || build.Build.Validate() != nil || build.Build.NativeAgreement == nil || build.CompatibilityPolicyRevision < 1 {
		return store.SecurityPreflightAccess{}, apperror.New("exam.attempt.desktop_incompatible").Wrap(err)
	}
	return store.SecurityPreflightAccess{SittingID: sittingID, CandidateUserID: principal.UserID, SessionID: principal.SessionID, DesktopRegistrationID: principal.DesktopRegistrationID, DPoPKeyThumbprint: principal.DPoPKeyThumbprint, DesktopBuild: build.Build, DesktopCompatibilityPolicyRevision: build.CompatibilityPolicyRevision}, nil
}
func (service *Service) beginPreflightAudit(ctx context.Context, invocation invocation.Invocation, sittingID model.ExamSittingID, operation string) (model.AuditEventID, error) {
	snapshot, err := service.deps.Sittings.Resolve(ctx, sittingID)
	if err != nil {
		return "", storeError(storeErrors, err)
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.ID != sittingID || !snapshot.Sitting.ClassID.IsValid() {
		return "", apperror.New("exam.attempt.unavailable").Wrap(errors.New("preflight Sitting ownership is invalid"))
	}
	return service.deps.Auditor.BeginAtScope(ctx, invocation, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, model.RoleScopeClass, snapshot.Sitting.ClassID.String(), operation, map[string]any{"exam_sitting_id": sittingID.String()}, nil)
}
func (service *Service) PrepareSecurityPreflight(ctx context.Context, invocation invocation.Invocation, command PrepareSecurityPreflightCommand) (*store.SecurityPreflightPrepared, error) {
	release, admissionErr := service.enterControl(ctx, invocation, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !command.SittingID.IsValid() || command.AttemptID != "" && !command.AttemptID.IsValid() || !model.IsValidSHA256Fingerprint(command.NativeRegistryDigest) || !model.IsValidSHA256Fingerprint(command.SourceManifestDigest) || !model.IsValidSHA256Fingerprint(command.ConfigurationManifestFingerprint) {
		return nil, apperror.Invalid("exam.attempt.invalid", "security_preflight")
	}
	access, err := service.securityPreflightAccess(ctx, invocation, command.SittingID)
	if err != nil {
		return nil, err
	}
	idempotency, err := applicationidempotency.Require(invocation, store.SecurityPreflightPrepareOperation, command.IdempotencyKey, struct {
		SittingID     model.ExamSittingID `json:"sitting_id"`
		AttemptID     model.ExamAttemptID `json:"attempt_id,omitempty"`
		Registry      string              `json:"native_registry_digest"`
		Manifest      string              `json:"source_manifest_digest"`
		Configuration string              `json:"configuration_manifest_fingerprint"`
	}{command.SittingID, command.AttemptID, command.NativeRegistryDigest, command.SourceManifestDigest, command.ConfigurationManifestFingerprint})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginPreflightAudit(ctx, invocation, command.SittingID, store.SecurityPreflightPrepareOperation)
	if err != nil {
		return nil, err
	}
	result, err := service.deps.Persistence.PrepareSecurityPreflight(ctx, &store.SecurityPreflightPrepare{Access: access, AttemptID: command.AttemptID, PreflightID: model.NewId(), Challenge: model.NewCredentialToken(), NativeRegistryDigest: command.NativeRegistryDigest, SourceManifestDigest: command.SourceManifestDigest, ConfigurationManifestFingerprint: command.ConfigurationManifestFingerprint, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		return nil, service.failAudit(ctx, audit, storeError(securityPreflightStoreErrors, err))
	}
	if result == nil || result.Challenge.Validate() != nil || result.Resolved.Policy.Validate(result.ServerTime) != nil || result.Resolved.Policy.SittingID != command.SittingID {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(errors.New("preflight Store returned an invalid policy"))
	}
	return result, nil
}
func (service *Service) ReportSecurityPreflight(ctx context.Context, invocation invocation.Invocation, command ReportSecurityPreflightCommand) (*model.SecurityPreflightResult, error) {
	release, admissionErr := service.enterControl(ctx, invocation, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !model.IsValidAgreementID(command.PreflightID) || command.Report.Validate() != nil {
		return nil, apperror.Invalid("exam.attempt.invalid", "security_preflight_report")
	}
	principal := invocation.Principal()
	// Authenticate before looking up a resource selector or beginning an audit.
	access, err := service.securityPreflightAccess(ctx, invocation, "")
	if err != nil {
		return nil, err
	}
	sittingID, err := service.deps.Persistence.ResolveSecurityPreflightSitting(ctx, command.PreflightID, principal.UserID, principal.SessionID)
	if err != nil {
		return nil, storeError(securityPreflightStoreErrors, err)
	}
	access.SittingID = sittingID
	idempotency, err := applicationidempotency.Require(invocation, store.SecurityPreflightReportOperation, command.IdempotencyKey, struct {
		PreflightID string                        `json:"preflight_id"`
		Report      model.SecurityPreflightReport `json:"report"`
	}{command.PreflightID, command.Report})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginPreflightAudit(ctx, invocation, sittingID, store.SecurityPreflightReportOperation)
	if err != nil {
		return nil, err
	}
	result, err := service.deps.Persistence.ReportSecurityPreflight(ctx, &store.SecurityPreflightReport{Access: access, PreflightID: command.PreflightID, Report: command.Report, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		return nil, service.failAudit(ctx, audit, storeError(securityPreflightStoreErrors, err))
	}
	if result == nil || result.PreflightID != command.PreflightID || !model.IsValidSHA256Fingerprint(result.ReportDigest) || (result.Admission != "eligible" && result.Admission != "blocked") {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(errors.New("preflight Store returned an invalid report receipt"))
	}
	return result, nil
}

// securityPreflightStoreErrors adds the native catalog sentinels to the
// Attempt table.
var securityPreflightStoreErrors = storeErrors.With(
	apperror.StoreRule{Kind: apperror.StoreSentinel, Sentinel: model.ErrNativeCatalogUnavailable, Code: "exam.security.catalog_missing"},
	apperror.StoreRule{Kind: apperror.StoreSentinel, Sentinel: model.ErrNativeCoverageUnsupported, Code: "exam.security.unsupported_capability"},
)

func (service *Service) RecoverSecurityPolicy(ctx context.Context, invocation invocation.Invocation, attemptID model.ExamAttemptID) (*store.SecurityPolicyRecovery, error) {
	release, admissionErr := service.enterControl(ctx, invocation, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !attemptID.IsValid() {
		return nil, apperror.Invalid("exam.attempt.invalid", "attempt_id")
	}
	access, err := service.securityPreflightAccess(ctx, invocation, "")
	if err != nil {
		return nil, err
	}
	result, err := service.deps.Persistence.RecoverSecurityPolicy(ctx, access, attemptID)
	if err != nil {
		return nil, storeError(securityPreflightStoreErrors, err)
	}
	if result == nil || result.ServerTime.IsZero() || result.SecurityCoverage.Validate() != nil || result.CurrentSources == nil || result.CurrentCoverage == nil || result.SourceResetReceipts == nil || len(result.CurrentSources) > 11 || len(result.CurrentCoverage) > 64 || len(result.SourceResetReceipts) > 11 || result.Security.Validate() != nil || result.Security.Policy.Scope.AttemptID != attemptID || result.FrozenAttemptConfiguration.Validate() != nil {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent security policy recovery"))
	}
	return result, nil
}

type UpdateSecurityCoverageCommand struct {
	Access          CandidateAccess
	ParticipationID model.AttemptParticipationID
	Generation      int64
	Coverage        model.SecurityCoverageRenewal
}

func (service *Service) UpdateSecurityCoverage(ctx context.Context, invocation invocation.Invocation, command UpdateSecurityCoverageCommand) (model.SecurityCoverageResult, error) {
	release, admissionErr := service.enterControl(ctx, invocation, true)
	if admissionErr != nil {
		return model.SecurityCoverageResult{}, admissionErr
	}
	defer release()
	selector, err := candidateSelector(invocation, command.Access)
	if err != nil {
		return model.SecurityCoverageResult{}, err
	}
	if !command.ParticipationID.IsValid() || command.Generation < 1 || command.Coverage.Validate() != nil {
		return model.SecurityCoverageResult{}, apperror.Invalid("exam.attempt.invalid", "security_coverage")
	}
	build, err := service.deps.DesktopBuilds.ResolveAttemptDesktopBuild(ctx, invocation.Principal())
	if err != nil || build.Build.Validate() != nil || build.Build.NativeAgreement == nil || build.CompatibilityPolicyRevision < 1 {
		return model.SecurityCoverageResult{}, apperror.New("exam.attempt.desktop_incompatible").Wrap(err)
	}
	result, err := service.deps.Persistence.UpdateSecurityCoverage(ctx, &store.ExamAttemptSecurityCoverageUpdate{Access: selector, ParticipationID: command.ParticipationID, Generation: command.Generation, DesktopBuild: build.Build, DesktopCompatibilityPolicyRevision: build.CompatibilityPolicyRevision, Coverage: command.Coverage})
	if err != nil {
		return model.SecurityCoverageResult{}, storeError(securityPreflightStoreErrors, err)
	}
	if result.Validate() != nil {
		return model.SecurityCoverageResult{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent security coverage result"))
	}
	return result, nil
}
