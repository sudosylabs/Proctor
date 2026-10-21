// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package store defines Proctor's durable persistence contracts.
package store

import (
	"context"
)

// Catalog exposes the complete family of authoritative persistence contracts
// without lifecycle authority. Application construction borrows this view;
// the node runtime and Platform retain validation and closure ownership.
type Catalog interface {
	Institution() InstitutionStore
	AcademicUnit() AcademicUnitStore
	Programme() ProgrammeStore
	ProgrammeLevel() ProgrammeLevelStore
	AcademicPeriod() AcademicPeriodStore
	ExamAuthoring() ExamAuthoringStore
	ExamRevision() ExamRevisionStore
	ExamSitting() ExamSittingStore
	ExamAttempt() ExamAttemptStore
	ExamAttemptWorkspace() ExamAttemptWorkspaceStore
	ExamSubmission() ExamSubmissionStore
	ExamIntegrityReview() ExamIntegrityReviewStore
	ExamRecords() ExamRecordsStore
	ExamExport() ExamExportStore
	ExamResource() ExamResourceStore
	ExamCorrection() ExamCorrectionStore
	ExamStarterWorkspace() ExamStarterWorkspaceStore
	Class() ClassStore
	User() UserStore
	UserSettings() UserSettingsStore
	File() FileStore
	Job() JobStore
	Mail() MailStore
	ExternalIdentity() ExternalIdentityStore
	ExternalLoginState() ExternalLoginStateStore
	BrowserAuthentication() BrowserAuthenticationStore
	DesktopRegistration() DesktopRegistrationStore
	UserToken() UserTokenStore
	Invitation() InvitationStore
	OnboardingImport() OnboardingImportStore
	PersonalAccessToken() PersonalAccessTokenStore
	MFA() MFAStore
	Affiliation() AffiliationStore
	AcademicUnitMember() AcademicUnitMemberStore
	ClassMember() ClassMemberStore
	PasswordCredential() PasswordCredentialStore
	Session() SessionStore
	SessionCredential() SessionCredentialStore
	Role() RoleStore
	RoleBinding() RoleBindingStore
	Audit() AuditStore
	Installation() InstallationStore
	AccessPolicy() AccessPolicyStore
	DesktopCompatibilityPolicy() DesktopCompatibilityPolicyStore
	RetentionPolicy() RetentionPolicyStore
	Retention() RetentionStore
	ClusterDiscovery() ClusterDiscoveryStore
	ServingNodeLease() ServingNodeLeaseStore
	CommandOutcome() CommandOutcomeStore
}

// Store is the root persistence contract used by the application and platform.
// Concrete adapters expose each model store through this interface so callers
// do not depend on PostgreSQL implementation types. It is the Catalog plus the
// lifecycle and schema authority that only the node runtime holds.
type Store interface {
	Catalog

	Ping(context.Context) error
	GetDBSchemaVersion(context.Context) (int, error)
	GetLocalSchemaVersion() (int, error)
	ValidateSchema(context.Context) error
	Close() error
}
