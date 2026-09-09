// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package store

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

const (
	SecurityPreflightPrepareOperation = "exam.security_preflight.prepare.v1"
	SecurityPreflightReportOperation  = "exam.security_preflight.report.v1"
)

// SecurityPreflightAccess is an authenticated selector, never a wire DTO.
// Build meaning comes from the verified server release catalog.
type SecurityPreflightAccess struct {
	SittingID                          model.ExamSittingID
	CandidateUserID                    model.UserID
	SessionID                          model.SessionID
	DesktopRegistrationID              model.DesktopRegistrationID
	DPoPKeyThumbprint                  string
	DesktopBuild                       model.DesktopBuildTuple
	DesktopCompatibilityPolicyRevision int64
}
type SecurityPreflightPrepare struct {
	Access                           SecurityPreflightAccess
	AttemptID                        model.ExamAttemptID
	PreflightID                      string
	Challenge                        string
	NativeRegistryDigest             string
	SourceManifestDigest             string
	ConfigurationManifestFingerprint string
	AuditEventID                     string
	AuditAt                          int64
}
type SecurityPreflightPrepared struct {
	ServerTime                 time.Time
	BrowserActivityDisclosure  model.BrowserActivityDisclosure
	Challenge                  model.SecurityPreflightChallenge
	Resolved                   model.ResolvedNativePolicy
	FrozenAttemptConfiguration *model.AttemptConfiguration
	Replayed                   bool
}
type SecurityPreflightReport struct {
	Access       SecurityPreflightAccess
	PreflightID  string
	Report       model.SecurityPreflightReport
	AuditEventID string
	AuditAt      int64
}

// SecurityPolicyRecovery returns privileged admission provenance only to the
// active owner's registered Session. It does not grant or renew a lease.
type SecurityPolicyRecovery struct {
	CurrentSources             []model.NativeSourceCoverage
	CurrentCoverage            []model.NativeCoverageClaim
	SourceResetReceipts        []model.SourceResetReceipt
	SecurityCoverage           model.SecurityCoverageResult
	ServerTime                 time.Time
	Security                   model.AdmittedSecurity
	FrozenAttemptConfiguration model.AttemptConfiguration
}

type ExamAttemptSecurityCoverageUpdate struct {
	Access                             CandidateAttemptAccess
	ParticipationID                    model.AttemptParticipationID
	Generation                         int64
	DesktopBuild                       model.DesktopBuildTuple
	DesktopCompatibilityPolicyRevision int64
	Coverage                           model.SecurityCoverageRenewal
}
