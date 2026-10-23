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
	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// The interfaces in this file are transport-owned capability contracts. They
// describe only what HTTP resource adapters call and keep concrete application
// and infrastructure types out of the transport composition surface.

type Authenticator interface {
	AuthenticateAccess(context.Context, string) (*model.Principal, error)
	AuthenticateBearer(context.Context, string) (*model.Principal, error)
}

type AccountStateApplication interface {
	SetUserEnabled(context.Context, invocation.Invocation, app.SetUserEnabledCommand) (*model.User, error)
}

type SessionAdministrationApplication interface {
	ListUserSessions(context.Context, invocation.Invocation, app.ListUserSessionsQuery) (app.Page[*model.Session], error)
	RevokeUserSession(context.Context, invocation.Invocation, app.RevokeUserSessionCommand) error
	RevokeUserSessions(context.Context, invocation.Invocation, app.RevokeUserSessionsCommand) error
}

type UserProfileApplication interface {
	GetCurrentUserContext(context.Context, invocation.Invocation) (*app.CurrentUserContextView, error)
	ListCandidateExamActivity(context.Context, invocation.Invocation, attempt.CandidateActivityQuery) (attempt.CandidateActivityPage, error)
	SearchUsers(context.Context, invocation.Invocation, app.SearchUsersQuery) (app.Page[*model.User], error)
	GetUserProfile(context.Context, invocation.Invocation, app.GetUserProfileQuery) (*model.User, error)
	UpdateUserProfile(context.Context, invocation.Invocation, app.UpdateUserProfileCommand) (*model.User, error)
	ChangeUserEmail(context.Context, invocation.Invocation, app.ChangeUserEmailCommand) (*app.UserEmailState, error)
	VerifyUserEmailPrivileged(context.Context, invocation.Invocation, app.VerifyUserEmailPrivilegedCommand) (*app.UserEmailState, error)
	UploadProfilePicture(context.Context, invocation.Invocation, app.UploadProfilePictureCommand) (*model.User, error)
	RemoveProfilePicture(context.Context, invocation.Invocation, app.RemoveProfilePictureCommand) (*model.User, error)
	GetProfilePicture(context.Context, invocation.Invocation, app.GetProfilePictureQuery) (*app.ProfilePictureContent, error)
}

type UserSettingsApplication interface {
	ReadOwnUserSettings(context.Context, invocation.Invocation) (app.UserSettingsView, error)
	ReplaceOwnUserSettings(context.Context, invocation.Invocation, app.ReplaceOwnUserSettingsCommand) (app.UserSettingsReplacementResult, error)
}

type AcademicUnitApplication interface {
	GetAcademicUnit(context.Context, invocation.Invocation, app.GetAcademicUnitQuery) (*model.AcademicUnit, error)
	ListAcademicUnits(context.Context, invocation.Invocation, app.ListAcademicUnitsQuery) (app.Page[*model.AcademicUnit], error)
	SearchAcademicUnits(context.Context, invocation.Invocation, app.SearchAcademicUnitsQuery) (app.Page[*model.AcademicUnit], error)
	CreateAcademicUnit(context.Context, invocation.Invocation, app.CreateAcademicUnitCommand) (*model.AcademicUnit, error)
	UpdateAcademicUnit(context.Context, invocation.Invocation, app.UpdateAcademicUnitCommand) (*model.AcademicUnit, error)
	ArchiveAcademicUnit(context.Context, invocation.Invocation, app.ArchiveAcademicUnitCommand) error
}

type InstitutionApplication interface {
	GetInstitution(context.Context, invocation.Invocation, app.GetInstitutionQuery) (*model.Institution, error)
	UpdateInstitution(context.Context, invocation.Invocation, app.UpdateInstitutionCommand) (*model.Institution, error)
}

type ProgrammeApplication interface {
	GetProgramme(context.Context, invocation.Invocation, app.GetProgrammeQuery) (*model.Programme, error)
	ListProgrammes(context.Context, invocation.Invocation, app.ListProgrammesQuery) (app.Page[*model.Programme], error)
	CreateProgramme(context.Context, invocation.Invocation, app.CreateProgrammeCommand) (*model.Programme, error)
	UpdateProgramme(context.Context, invocation.Invocation, app.UpdateProgrammeCommand) (*model.Programme, error)
	ArchiveProgramme(context.Context, invocation.Invocation, app.ArchiveProgrammeCommand) error
}

type ProgrammeLevelApplication interface {
	GetProgrammeLevel(context.Context, invocation.Invocation, app.GetProgrammeLevelQuery) (*model.ProgrammeLevel, error)
	ListProgrammeLevels(context.Context, invocation.Invocation, app.ListProgrammeLevelsQuery) (app.Page[*model.ProgrammeLevel], error)
	CreateProgrammeLevel(context.Context, invocation.Invocation, app.CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error)
	UpdateProgrammeLevel(context.Context, invocation.Invocation, app.UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error)
	ArchiveProgrammeLevel(context.Context, invocation.Invocation, app.ArchiveProgrammeLevelCommand) error
}

type AcademicPeriodApplication interface {
	GetAcademicPeriod(context.Context, invocation.Invocation, app.GetAcademicPeriodQuery) (*model.AcademicPeriod, error)
	ListAcademicPeriods(context.Context, invocation.Invocation, app.ListAcademicPeriodsQuery) (app.Page[*model.AcademicPeriod], error)
	CreateAcademicPeriod(context.Context, invocation.Invocation, app.CreateAcademicPeriodCommand) (*model.AcademicPeriod, error)
	UpdateAcademicPeriod(context.Context, invocation.Invocation, app.UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error)
	ArchiveAcademicPeriod(context.Context, invocation.Invocation, app.ArchiveAcademicPeriodCommand) error
}

type ClassApplication interface {
	GetClass(context.Context, invocation.Invocation, app.GetClassQuery) (*model.Class, error)
	ListClasses(context.Context, invocation.Invocation, app.ListClassesQuery) (app.Page[*model.Class], error)
	SearchClasses(context.Context, invocation.Invocation, app.SearchClassesQuery) (app.Page[*model.Class], error)
	CreateClass(context.Context, invocation.Invocation, app.CreateClassCommand) (*model.Class, error)
	UpdateClass(context.Context, invocation.Invocation, app.UpdateClassCommand) (*model.Class, error)
	ArchiveClass(context.Context, invocation.Invocation, app.ArchiveClassCommand) error
}

type ExamApplication interface {
	CreateExam(context.Context, invocation.Invocation, exam.CreateCommand) (exam.View, error)
	GetExam(context.Context, invocation.Invocation, exam.GetQuery) (exam.View, error)
	EditExamDraftText(context.Context, invocation.Invocation, exam.EditDraftTextCommand) (exam.View, error)
	ConfigureExamDraftNativePolicy(context.Context, invocation.Invocation, exam.ConfigureDraftNativePolicyCommand) (exam.View, error)
	ConfigureExamDraftFocusLoss(context.Context, invocation.Invocation, exam.ConfigureDraftFocusLossCommand) (exam.View, error)
	ConfigureExamDraftBrowserPolicy(context.Context, invocation.Invocation, exam.ConfigureDraftBrowserPolicyCommand) (exam.View, error)
	ListExams(context.Context, invocation.Invocation, exam.ListQuery) (exam.CatalogPage, error)
	ArchiveExam(context.Context, invocation.Invocation, exam.ArchiveCommand) (model.Exam, error)
	ListExamManagers(context.Context, invocation.Invocation, exam.ListManagersQuery) (exam.ManagerPage, error)
	AddExamManager(context.Context, invocation.Invocation, exam.AddManagerCommand) (store.ExamManagerCommandResult, error)
	RemoveExamManager(context.Context, invocation.Invocation, exam.RemoveManagerCommand) (store.ExamManagerCommandResult, error)
	TransferExamOwnership(context.Context, invocation.Invocation, exam.TransferOwnerCommand) (store.ExamManagerCommandResult, error)
}

type AffiliationApplication interface {
	ListAffiliations(context.Context, invocation.Invocation, app.ListAffiliationsQuery) ([]*model.Affiliation, error)
	CreateAffiliation(context.Context, invocation.Invocation, app.CreateAffiliationCommand) (*model.Affiliation, error)
	EndAffiliation(context.Context, invocation.Invocation, app.EndAffiliationCommand) (*model.Affiliation, error)
}

type AcademicUnitMemberApplication interface {
	ListAcademicUnitMembersPage(context.Context, invocation.Invocation, app.ListAcademicUnitMembersPageQuery) (*app.AcademicUnitMemberPage, error)
	CreateAcademicUnitMember(context.Context, invocation.Invocation, app.CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
	EndAcademicUnitMember(context.Context, invocation.Invocation, app.EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
}

type ClassMemberApplication interface {
	ListClassMembersPage(context.Context, invocation.Invocation, app.ListClassMembersPageQuery) (*app.ClassMemberPage, error)
	EnrollClassMember(context.Context, invocation.Invocation, app.EnrollClassMemberCommand) (*model.ClassEnrollment, error)
	EndClassMember(context.Context, invocation.Invocation, app.EndClassMemberCommand) (*model.ClassMember, error)
}

type Sessions interface {
	ListSessions(context.Context, invocation.Invocation, app.ListSessionsQuery) ([]*model.Session, error)
	RevokeSession(context.Context, invocation.Invocation, app.RevokeSessionCommand) error
	RevokeAllSessions(context.Context, invocation.Invocation, app.RevokeAllSessionsCommand) error
}

type PersonalAccessTokens interface {
	CreatePersonalAccessToken(context.Context, invocation.Invocation, app.CreatePersonalAccessTokenCommand) (*model.PersonalAccessTokenCreation, error)
	ListPersonalAccessTokens(context.Context, invocation.Invocation, app.ListPersonalAccessTokensQuery) ([]*model.PersonalAccessToken, error)
	RevokePersonalAccessToken(context.Context, invocation.Invocation, app.RevokePersonalAccessTokenCommand) (*model.PersonalAccessToken, error)
	SetPersonalAccessTokenDisabled(context.Context, invocation.Invocation, app.SetPersonalAccessTokenDisabledCommand) (*model.PersonalAccessToken, error)
}

type MFA interface {
	GetMFAStatus(context.Context, invocation.Invocation, app.GetMFAStatusQuery) (*app.MFAStatus, error)
	SetupMFA(context.Context, invocation.Invocation, app.SetupMFACommand) (*app.MFASetup, error)
	ActivateMFA(context.Context, invocation.Invocation, app.ActivateMFACommand) (*app.MFAActivation, error)
	ChallengeMFA(context.Context, invocation.Invocation, app.ChallengeMFACommand) (*model.Session, error)
	RegenerateMFARecoveryCodes(context.Context, invocation.Invocation, app.RegenerateMFARecoveryCodesCommand) ([]string, error)
	DisableMFA(context.Context, invocation.Invocation, app.DisableMFACommand) error
}

type AuditListingApplication interface {
	ListAuditEvents(context.Context, invocation.Invocation, app.ListAuditEventsQuery) (app.Page[*model.AuditEvent], error)
}

type BootstrapApplication interface {
	GetInstallationStatus(context.Context, invocation.Invocation, app.GetInstallationStatusQuery) (*model.InstallationStatus, error)
	BootstrapInstallation(context.Context, invocation.Invocation, app.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error)
}

type DesktopCompatibilityApplication interface {
	EvaluateDesktopCompatibility(
		context.Context,
		invocation.Invocation,
		app.DesktopCompatibilityQuery,
	) (app.DesktopCompatibilityResult, error)
	GetDesktopCompatibilityPolicy(
		context.Context,
		invocation.Invocation,
	) (*model.DesktopCompatibilityPolicy, error)
	ReplaceDesktopCompatibilityPolicy(
		context.Context,
		invocation.Invocation,
		app.ReplaceDesktopCompatibilityPolicyCommand,
	) (*model.DesktopCompatibilityPolicy, error)
}

type RoleApplication interface {
	ListRoles(context.Context, invocation.Invocation, app.ListRolesQuery) ([]*model.Role, error)
	GetRole(context.Context, invocation.Invocation, app.GetRoleQuery) (*model.Role, error)
	CreateRole(context.Context, invocation.Invocation, app.CreateRoleCommand) (*model.Role, error)
	UpdateRole(context.Context, invocation.Invocation, app.UpdateRoleCommand) (*model.Role, error)
	ArchiveRole(context.Context, invocation.Invocation, app.ArchiveRoleCommand) error
}

type RoleBindingApplication interface {
	ListRoleBindings(context.Context, invocation.Invocation, app.ListRoleBindingsQuery) ([]*model.RoleBinding, error)
	CreateRoleBinding(context.Context, invocation.Invocation, app.CreateRoleBindingCommand) (*model.RoleBinding, error)
	EndRoleBinding(context.Context, invocation.Invocation, app.EndRoleBindingCommand) (*model.RoleBinding, error)
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
	MFAKeyApplication
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
	GetMailKeyState(context.Context, invocation.Invocation) (app.MailKeyStateView, error)
	StartMailRekey(context.Context, invocation.Invocation, string) (app.MailRekeyView, error)
	GetMailRekeyStatus(context.Context, invocation.Invocation, model.JobID) (app.MailRekeyStatusView, error)
	SendTestMail(context.Context, invocation.Invocation) (app.MailDeliveryView, error)
	GetMailMetrics(context.Context, invocation.Invocation) (app.MailMetricsSnapshot, error)
	GetMailDelivery(context.Context, invocation.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error)
	ListMailDeliveries(context.Context, invocation.Invocation, app.ListMailDeliveriesQuery) (app.MailDeliveryPage, error)
	CancelMailDelivery(context.Context, invocation.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error)
	RetryMailDelivery(context.Context, invocation.Invocation, model.MailDeliveryID) (app.MailDeliveryView, error)
}
