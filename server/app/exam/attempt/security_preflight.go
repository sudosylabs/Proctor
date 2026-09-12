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

func (service *Service) securityPreflightAccess(ctx context.Context, call Call, sittingID model.ExamSittingID) (store.SecurityPreflightAccess, error) {
	principal := call.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return store.SecurityPreflightAccess{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !principal.HasRegisteredDesktopKey() {
		return store.SecurityPreflightAccess{}, &Fault{Code: "exam.attempt.registered_desktop_required"}
	}
	build, err := service.deps.DesktopBuilds.ResolveAttemptDesktopBuild(ctx, principal)
	if err != nil || build.Build.Validate() != nil || build.Build.NativeAgreement == nil || build.CompatibilityPolicyRevision < 1 {
		return store.SecurityPreflightAccess{}, &Fault{Code: "exam.attempt.desktop_incompatible", Cause: err}
	}
	return store.SecurityPreflightAccess{SittingID: sittingID, CandidateUserID: principal.UserID, SessionID: principal.SessionID, DesktopRegistrationID: principal.DesktopRegistrationID, DPoPKeyThumbprint: principal.DPoPKeyThumbprint, DesktopBuild: build.Build, DesktopCompatibilityPolicyRevision: build.CompatibilityPolicyRevision}, nil
}
func (service *Service) beginPreflightAudit(ctx context.Context, call Call, sittingID model.ExamSittingID, operation string) (string, error) {
	snapshot, err := service.deps.Sittings.Resolve(ctx, sittingID)
	if err != nil {
		return "", mapStore(err)
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.ID != sittingID || !snapshot.Sitting.ClassID.IsValid() {
		return "", unavailable(errors.New("preflight Sitting ownership is invalid"))
	}
	return service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, model.RoleScopeClass, snapshot.Sitting.ClassID.String(), operation, map[string]any{"exam_sitting_id": sittingID.String()})
}
func (service *Service) PrepareSecurityPreflight(ctx context.Context, call Call, command PrepareSecurityPreflightCommand) (*store.SecurityPreflightPrepared, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !command.SittingID.IsValid() || command.AttemptID != "" && !command.AttemptID.IsValid() || !model.IsValidSHA256Fingerprint(command.NativeRegistryDigest) || !model.IsValidSHA256Fingerprint(command.SourceManifestDigest) || !model.IsValidSHA256Fingerprint(command.ConfigurationManifestFingerprint) {
		return nil, invalid("security_preflight")
	}
	access, err := service.securityPreflightAccess(ctx, call, command.SittingID)
	if err != nil {
		return nil, err
	}
	idempotency, err := prepareIdempotency(call, store.SecurityPreflightPrepareOperation, command.IdempotencyKey, struct {
		SittingID     model.ExamSittingID `json:"sitting_id"`
		AttemptID     model.ExamAttemptID `json:"attempt_id,omitempty"`
		Registry      string              `json:"native_registry_digest"`
		Manifest      string              `json:"source_manifest_digest"`
		Configuration string              `json:"configuration_manifest_fingerprint"`
	}{command.SittingID, command.AttemptID, command.NativeRegistryDigest, command.SourceManifestDigest, command.ConfigurationManifestFingerprint})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginPreflightAudit(ctx, call, command.SittingID, store.SecurityPreflightPrepareOperation)
	if err != nil {
		return nil, err
	}
	result, err := service.deps.Persistence.PrepareSecurityPreflight(ctx, &store.SecurityPreflightPrepare{Access: access, AttemptID: command.AttemptID, PreflightID: model.NewId(), Challenge: model.NewCredentialToken(), NativeRegistryDigest: command.NativeRegistryDigest, SourceManifestDigest: command.SourceManifestDigest, ConfigurationManifestFingerprint: command.ConfigurationManifestFingerprint, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		return nil, service.failAudit(ctx, audit, mapSecurityPreflightError(err))
	}
	if result == nil || result.Challenge.Validate() != nil || result.Resolved.Policy.Validate(result.ServerTime) != nil || result.Resolved.Policy.SittingID != command.SittingID {
		return nil, unavailable(errors.New("preflight Store returned an invalid policy"))
	}
	return result, nil
}
func (service *Service) ReportSecurityPreflight(ctx context.Context, call Call, command ReportSecurityPreflightCommand) (*model.SecurityPreflightResult, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !model.IsValidAgreementID(command.PreflightID) || command.Report.Validate() != nil {
		return nil, invalid("security_preflight_report")
	}
	principal := call.Principal()
	// Authenticate before looking up a resource selector or beginning an audit.
	access, err := service.securityPreflightAccess(ctx, call, "")
	if err != nil {
		return nil, err
	}
	sittingID, err := service.deps.Persistence.ResolveSecurityPreflightSitting(ctx, command.PreflightID, principal.UserID, principal.SessionID)
	if err != nil {
		return nil, mapSecurityPreflightError(err)
	}
	access.SittingID = sittingID
	idempotency, err := prepareIdempotency(call, store.SecurityPreflightReportOperation, command.IdempotencyKey, struct {
		PreflightID string                        `json:"preflight_id"`
		Report      model.SecurityPreflightReport `json:"report"`
	}{command.PreflightID, command.Report})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginPreflightAudit(ctx, call, sittingID, store.SecurityPreflightReportOperation)
	if err != nil {
		return nil, err
	}
	result, err := service.deps.Persistence.ReportSecurityPreflight(ctx, &store.SecurityPreflightReport{Access: access, PreflightID: command.PreflightID, Report: command.Report, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		return nil, service.failAudit(ctx, audit, mapSecurityPreflightError(err))
	}
	if result == nil || result.PreflightID != command.PreflightID || !model.IsValidSHA256Fingerprint(result.ReportDigest) || (result.Admission != "eligible" && result.Admission != "blocked") {
		return nil, unavailable(errors.New("preflight Store returned an invalid report receipt"))
	}
	return result, nil
}
func mapSecurityPreflightError(err error) error {
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) {
		switch conflict.Constraint {
		case "preflight_expired", "preflight_superseded", "preflight_consumed", "preflight_rate_limited", "policy_changed", "session_changed", "configuration_unsupported", "attempt_not_ready":
			return &Fault{Code: "exam.security." + conflict.Constraint, Cause: err}
		}
	}
	if errors.Is(err, model.ErrNativeCatalogUnavailable) {
		return &Fault{Code: "exam.security.catalog_unavailable", Cause: err}
	}
	if errors.Is(err, model.ErrNativeCoverageUnsupported) {
		return &Fault{Code: "exam.security.unsupported_capability", Cause: err}
	}
	return mapStore(err)
}

func (service *Service) RecoverSecurityPolicy(ctx context.Context, call Call, attemptID model.ExamAttemptID) (*store.SecurityPolicyRecovery, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !attemptID.IsValid() {
		return nil, invalid("attempt_id")
	}
	access, err := service.securityPreflightAccess(ctx, call, "")
	if err != nil {
		return nil, err
	}
	result, err := service.deps.Persistence.RecoverSecurityPolicy(ctx, access, attemptID)
	if err != nil {
		return nil, mapSecurityPreflightError(err)
	}
	if result == nil || result.ServerTime.IsZero() || result.SecurityCoverage.Validate() != nil || result.CurrentSources == nil || result.CurrentCoverage == nil || result.SourceResetReceipts == nil || len(result.CurrentSources) > 11 || len(result.CurrentCoverage) > 64 || len(result.SourceResetReceipts) > 11 || result.Security.Validate() != nil || result.Security.Policy.Scope.AttemptID != attemptID || result.FrozenAttemptConfiguration.Validate() != nil {
		return nil, unavailable(errors.New("inconsistent security policy recovery"))
	}
	return result, nil
}

type UpdateSecurityCoverageCommand struct {
	Access          CandidateAccess
	ParticipationID model.AttemptParticipationID
	Generation      int64
	Coverage        model.SecurityCoverageRenewal
}

func (service *Service) UpdateSecurityCoverage(ctx context.Context, call Call, command UpdateSecurityCoverageCommand) (model.SecurityCoverageResult, error) {
	release, admissionErr := service.enterControl(ctx, call, true)
	if admissionErr != nil {
		return model.SecurityCoverageResult{}, admissionErr
	}
	defer release()
	selector, err := candidateSelector(call, command.Access)
	if err != nil {
		return model.SecurityCoverageResult{}, err
	}
	if !command.ParticipationID.IsValid() || command.Generation < 1 || command.Coverage.Validate() != nil {
		return model.SecurityCoverageResult{}, invalid("security_coverage")
	}
	build, err := service.deps.DesktopBuilds.ResolveAttemptDesktopBuild(ctx, call.Principal())
	if err != nil || build.Build.Validate() != nil || build.Build.NativeAgreement == nil || build.CompatibilityPolicyRevision < 1 {
		return model.SecurityCoverageResult{}, &Fault{Code: "exam.attempt.desktop_incompatible", Cause: err}
	}
	result, err := service.deps.Persistence.UpdateSecurityCoverage(ctx, &store.ExamAttemptSecurityCoverageUpdate{Access: selector, ParticipationID: command.ParticipationID, Generation: command.Generation, DesktopBuild: build.Build, DesktopCompatibilityPolicyRevision: build.CompatibilityPolicyRevision, Coverage: command.Coverage})
	if err != nil {
		return model.SecurityCoverageResult{}, mapSecurityPreflightError(err)
	}
	if result.Validate() != nil {
		return model.SecurityCoverageResult{}, unavailable(errors.New("inconsistent security coverage result"))
	}
	return result, nil
}
