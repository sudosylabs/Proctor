// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

// The interfaces in this file are transport-owned capability contracts. They
// describe only what HTTP resource adapters call and keep concrete application
// and infrastructure types out of the transport composition surface.

type Authenticator interface {
	AuthenticateAccess(context.Context, string) (*model.Principal, error)
	AuthenticateBearer(context.Context, string) (*model.Principal, error)
}

type AccountStateApplication interface {
	SetUserEnabled(context.Context, app.Invocation, app.SetUserEnabledCommand) (*model.User, error)
}

type SessionAdministrationApplication interface {
	ListUserSessions(context.Context, app.Invocation, app.ListUserSessionsQuery) ([]*model.Session, error)
	RevokeUserSession(context.Context, app.Invocation, app.RevokeUserSessionCommand) error
	RevokeUserSessions(context.Context, app.Invocation, app.RevokeUserSessionsCommand) error
}

type UserProfileApplication interface {
	GetCurrentUserContext(context.Context, app.Invocation) (*app.CurrentUserContextView, error)
	ListCandidateExamActivity(context.Context, app.Invocation, app.ListCandidateExamActivityQuery) (app.CandidateExamActivityPage, error)
	SearchUsers(context.Context, app.Invocation, app.SearchUsersQuery) ([]*model.User, error)
	GetUserProfile(context.Context, app.Invocation, app.GetUserProfileQuery) (*model.User, error)
	UpdateUserProfile(context.Context, app.Invocation, app.UpdateUserProfileCommand) (*model.User, error)
	ChangeUserEmail(context.Context, app.Invocation, app.ChangeUserEmailCommand) (*app.UserEmailState, error)
	VerifyUserEmailPrivileged(context.Context, app.Invocation, app.VerifyUserEmailPrivilegedCommand) (*app.UserEmailState, error)
	UploadProfilePicture(context.Context, app.Invocation, app.UploadProfilePictureCommand) (*model.User, error)
	RemoveProfilePicture(context.Context, app.Invocation, app.RemoveProfilePictureCommand) (*model.User, error)
	GetProfilePicture(context.Context, app.Invocation, app.GetProfilePictureQuery) (*app.ProfilePictureContent, error)
}

type UserSettingsApplication interface {
	ReadOwnUserSettings(context.Context, app.Invocation) (app.UserSettingsView, error)
	ReplaceOwnUserSettings(context.Context, app.Invocation, app.ReplaceOwnUserSettingsCommand) (app.UserSettingsReplacementResult, error)
}

type AcademicUnitApplication interface {
	GetAcademicUnit(context.Context, app.Invocation, app.GetAcademicUnitQuery) (*model.AcademicUnit, error)
	ListAcademicUnits(context.Context, app.Invocation, app.ListAcademicUnitsQuery) ([]*model.AcademicUnit, error)
	SearchAcademicUnits(context.Context, app.Invocation, app.SearchAcademicUnitsQuery) ([]*model.AcademicUnit, error)
	CreateAcademicUnit(context.Context, app.Invocation, app.CreateAcademicUnitCommand) (*model.AcademicUnit, error)
	UpdateAcademicUnit(context.Context, app.Invocation, app.UpdateAcademicUnitCommand) (*model.AcademicUnit, error)
	ArchiveAcademicUnit(context.Context, app.Invocation, app.ArchiveAcademicUnitCommand) error
}

type InstitutionApplication interface {
	GetInstitution(context.Context, app.Invocation, app.GetInstitutionQuery) (*model.Institution, error)
	UpdateInstitution(context.Context, app.Invocation, app.UpdateInstitutionCommand) (*model.Institution, error)
}

type ProgrammeApplication interface {
	GetProgramme(context.Context, app.Invocation, app.GetProgrammeQuery) (*model.Programme, error)
	ListProgrammes(context.Context, app.Invocation, app.ListProgrammesQuery) ([]*model.Programme, error)
	CreateProgramme(context.Context, app.Invocation, app.CreateProgrammeCommand) (*model.Programme, error)
	UpdateProgramme(context.Context, app.Invocation, app.UpdateProgrammeCommand) (*model.Programme, error)
	ArchiveProgramme(context.Context, app.Invocation, app.ArchiveProgrammeCommand) error
}

type ProgrammeLevelApplication interface {
	GetProgrammeLevel(context.Context, app.Invocation, app.GetProgrammeLevelQuery) (*model.ProgrammeLevel, error)
	ListProgrammeLevels(context.Context, app.Invocation, app.ListProgrammeLevelsQuery) ([]*model.ProgrammeLevel, error)
	CreateProgrammeLevel(context.Context, app.Invocation, app.CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error)
	UpdateProgrammeLevel(context.Context, app.Invocation, app.UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error)
	ArchiveProgrammeLevel(context.Context, app.Invocation, app.ArchiveProgrammeLevelCommand) error
}

type AcademicPeriodApplication interface {
	GetAcademicPeriod(context.Context, app.Invocation, app.GetAcademicPeriodQuery) (*model.AcademicPeriod, error)
	ListAcademicPeriods(context.Context, app.Invocation, app.ListAcademicPeriodsQuery) ([]*model.AcademicPeriod, error)
	CreateAcademicPeriod(context.Context, app.Invocation, app.CreateAcademicPeriodCommand) (*model.AcademicPeriod, error)
	UpdateAcademicPeriod(context.Context, app.Invocation, app.UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error)
	ArchiveAcademicPeriod(context.Context, app.Invocation, app.ArchiveAcademicPeriodCommand) error
}

type ClassApplication interface {
	GetClass(context.Context, app.Invocation, app.GetClassQuery) (*model.Class, error)
	ListClasses(context.Context, app.Invocation, app.ListClassesQuery) ([]*model.Class, error)
	SearchClasses(context.Context, app.Invocation, app.SearchClassesQuery) ([]*model.Class, error)
	CreateClass(context.Context, app.Invocation, app.CreateClassCommand) (*model.Class, error)
	UpdateClass(context.Context, app.Invocation, app.UpdateClassCommand) (*model.Class, error)
	ArchiveClass(context.Context, app.Invocation, app.ArchiveClassCommand) error
}

type ExamApplication interface {
	CreateExam(context.Context, app.Invocation, app.CreateExamCommand) (app.ExamView, error)
	GetExam(context.Context, app.Invocation, app.GetExamQuery) (app.ExamView, error)
	EditExamDraftText(context.Context, app.Invocation, app.EditExamDraftTextCommand) (app.ExamView, error)
	ConfigureExamDraftNativePolicy(context.Context, app.Invocation, app.ConfigureExamDraftNativePolicyCommand) (app.ExamView, error)
	ConfigureExamDraftFocusLoss(context.Context, app.Invocation, app.ConfigureExamDraftFocusLossCommand) (app.ExamView, error)
	ConfigureExamDraftBrowserPolicy(context.Context, app.Invocation, app.ConfigureExamDraftBrowserPolicyCommand) (app.ExamView, error)
	ListExams(context.Context, app.Invocation, app.ListExamsQuery) (app.ExamCatalogPage, error)
	ArchiveExam(context.Context, app.Invocation, app.ArchiveExamCommand) (model.Exam, error)
	ListExamManagers(context.Context, app.Invocation, app.ListExamManagersQuery) (app.ExamManagerPage, error)
	AddExamManager(context.Context, app.Invocation, app.AddExamManagerCommand) (app.ExamManagerChange, error)
	RemoveExamManager(context.Context, app.Invocation, app.RemoveExamManagerCommand) (app.ExamManagerChange, error)
	TransferExamOwnership(context.Context, app.Invocation, app.TransferExamOwnershipCommand) (app.ExamManagerChange, error)
}

type AffiliationApplication interface {
	ListAffiliations(context.Context, app.Invocation, app.ListAffiliationsQuery) ([]*model.Affiliation, error)
	CreateAffiliation(context.Context, app.Invocation, app.CreateAffiliationCommand) (*model.Affiliation, error)
	EndAffiliation(context.Context, app.Invocation, app.EndAffiliationCommand) (*model.Affiliation, error)
}

type AcademicUnitMemberApplication interface {
	ListAcademicUnitMembers(context.Context, app.Invocation, app.ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error)
	ListAcademicUnitMembersPage(context.Context, app.Invocation, app.ListAcademicUnitMembersPageQuery) (*app.AcademicUnitMemberPage, error)
	CreateAcademicUnitMember(context.Context, app.Invocation, app.CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
	EndAcademicUnitMember(context.Context, app.Invocation, app.EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
}

type ClassMemberApplication interface {
	ListClassMembers(context.Context, app.Invocation, app.ListClassMembersQuery) ([]*model.ClassMember, error)
	ListClassMembersPage(context.Context, app.Invocation, app.ListClassMembersPageQuery) (*app.ClassMemberPage, error)
	EnrollClassMember(context.Context, app.Invocation, app.EnrollClassMemberCommand) (*model.ClassEnrollment, error)
	EndClassMember(context.Context, app.Invocation, app.EndClassMemberCommand) (*model.ClassMember, error)
}

type Sessions interface {
	ListSessions(context.Context, app.Invocation, app.ListSessionsQuery) ([]*model.Session, error)
	RevokeSession(context.Context, app.Invocation, app.RevokeSessionCommand) error
	RevokeAllSessions(context.Context, app.Invocation, app.RevokeAllSessionsCommand) error
}

type PersonalAccessTokens interface {
	CreatePersonalAccessToken(context.Context, app.Invocation, app.CreatePersonalAccessTokenCommand) (*model.PersonalAccessTokenCreation, error)
	ListPersonalAccessTokens(context.Context, app.Invocation, app.ListPersonalAccessTokensQuery) ([]*model.PersonalAccessToken, error)
	RevokePersonalAccessToken(context.Context, app.Invocation, app.RevokePersonalAccessTokenCommand) (*model.PersonalAccessToken, error)
	SetPersonalAccessTokenDisabled(context.Context, app.Invocation, app.SetPersonalAccessTokenDisabledCommand) (*model.PersonalAccessToken, error)
}

type MFA interface {
	GetMFAStatus(context.Context, app.Invocation, app.GetMFAStatusQuery) (*app.MFAStatus, error)
	SetupMFA(context.Context, app.Invocation, app.SetupMFACommand) (*app.MFASetup, error)
	ActivateMFA(context.Context, app.Invocation, app.ActivateMFACommand) (*app.MFAActivation, error)
	ChallengeMFA(context.Context, app.Invocation, app.ChallengeMFACommand) (*model.Session, error)
	RegenerateMFARecoveryCodes(context.Context, app.Invocation, app.RegenerateMFARecoveryCodesCommand) ([]string, error)
	DisableMFA(context.Context, app.Invocation, app.DisableMFACommand) error
}

type AuditListingApplication interface {
	ListAuditEvents(context.Context, app.Invocation, app.ListAuditEventsQuery) ([]*model.AuditEvent, error)
}

type BootstrapApplication interface {
	GetInstallationStatus(context.Context, app.GetInstallationStatusQuery) (*model.InstallationStatus, error)
	BootstrapInstallation(context.Context, app.Invocation, app.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error)
}

type DesktopCompatibilityApplication interface {
	EvaluateDesktopCompatibility(
		context.Context,
		app.DesktopCompatibilityQuery,
	) (app.DesktopCompatibilityResult, error)
	GetDesktopCompatibilityPolicy(
		context.Context,
		app.Invocation,
	) (*model.DesktopCompatibilityPolicy, error)
	ReplaceDesktopCompatibilityPolicy(
		context.Context,
		app.Invocation,
		app.ReplaceDesktopCompatibilityPolicyCommand,
	) (*model.DesktopCompatibilityPolicy, error)
}

type RoleApplication interface {
	ListRoles(context.Context, app.Invocation, app.ListRolesQuery) ([]*model.Role, error)
	GetRole(context.Context, app.Invocation, app.GetRoleQuery) (*model.Role, error)
	CreateRole(context.Context, app.Invocation, app.CreateRoleCommand) (*model.Role, error)
	UpdateRole(context.Context, app.Invocation, app.UpdateRoleCommand) (*model.Role, error)
	ArchiveRole(context.Context, app.Invocation, app.ArchiveRoleCommand) error
}

type RoleBindingApplication interface {
	ListRoleBindings(context.Context, app.Invocation, app.ListRoleBindingsQuery) ([]*model.RoleBinding, error)
	CreateRoleBinding(context.Context, app.Invocation, app.CreateRoleBindingCommand) (*model.RoleBinding, error)
	EndRoleBinding(context.Context, app.Invocation, app.EndRoleBindingCommand) (*model.RoleBinding, error)
}

// Application is the cohesive application-facing API contract. Its component
// interfaces keep domain ownership visible without turning authentication into
// an unrelated service locator.
type Application interface {
	Authenticator
	authenticationEntryApplication
	DesktopAuthorization
	DesktopRegistrations
	externalAuthenticationEntryApplication
	authenticationMethodApplication
	Sessions
	SessionReauthentication
	PersonalAccessTokens
	MFA
	MFAAdministration
	InstitutionApplication
	DesktopCompatibilityApplication
	RetentionPolicyApplication
	RetentionApplication
	ExamRecordsApplication
	ExamExportsApplication
	JobOperationsApplication
	MailApplication
	ExamApplication
	ExamRevisionApplication
	ExamSittingApplication
	ExamSittingCorrectionApplication
	ExamResourceApplication
	ExamStarterWorkspaceApplication
	ExamAttemptApplication
	ExamIntegrityReviewApplication
}

type MailApplication interface {
	GetMailKeyState(context.Context, app.Invocation) (app.MailKeyStateView, error)
	StartMailRekey(context.Context, app.Invocation, string) (app.MailRekeyView, error)
	GetMailRekeyStatus(context.Context, app.Invocation, model.JobID) (app.MailRekeyStatusView, error)
	SendTestMail(context.Context, app.Invocation) (app.MailDeliveryView, error)
	GetMailMetrics(context.Context, app.Invocation) (app.MailMetricsSnapshot, error)
	GetMailDelivery(context.Context, app.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error)
	ListMailDeliveries(context.Context, app.Invocation, app.ListMailDeliveriesQuery) (app.MailDeliveryPage, error)
	CancelMailDelivery(context.Context, app.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error)
	RetryMailDelivery(context.Context, app.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error)
}
