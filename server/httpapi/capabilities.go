// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

// The interfaces in this file are transport-owned capability contracts. They
// describe only what HTTP resource adapters call and keep concrete application
// and infrastructure types out of the transport composition surface.

type Authenticator interface {
	AuthenticateAccess(context.Context, string) (*model.Principal, error)
	AuthenticateBearer(context.Context, string) (*model.Principal, error)
}

type Authentication interface {
	Authenticator
	RegisterLocalUser(context.Context, application.Invocation, application.RegisterLocalUserCommand) error
	Login(context.Context, application.Invocation, application.LoginCommand) (*application.LoginResult, error)
	AuthenticateAccess(context.Context, string) (*model.Principal, error)
	RefreshSession(context.Context, application.Invocation, application.RefreshSessionCommand) (*model.Session, *model.AuthenticationTokens, error)
	Logout(context.Context, application.Invocation, application.LogoutCommand) error
	RequestEmailVerification(context.Context, application.Invocation, application.RequestEmailVerificationCommand) error
	CompleteEmailVerification(context.Context, application.Invocation, application.CompleteEmailVerificationCommand) (*model.User, error)
	RequestPasswordReset(context.Context, application.Invocation, application.RequestPasswordResetCommand) error
	CompletePasswordReset(context.Context, application.Invocation, application.CompletePasswordResetCommand) (*model.User, error)
}

type ExternalAuthentication interface {
	ExternalAuthenticationProviders(context.Context) ([]model.ExternalAuthenticationProvider, error)
	BeginExternalAuthentication(context.Context, application.Invocation, application.BeginExternalAuthenticationCommand) (*model.ExternalAuthenticationStart, error)
	CompleteExternalAuthentication(context.Context, application.Invocation, application.CompleteExternalAuthenticationCommand) (*model.ExternalAuthenticationCompletion, error)
}

type AccountStateApplication interface {
	SetUserEnabled(context.Context, application.Invocation, application.SetUserEnabledCommand) (*model.User, error)
}

type SessionAdministrationApplication interface {
	ListUserSessions(context.Context, application.Invocation, application.ListUserSessionsQuery) ([]*model.Session, error)
	RevokeUserSession(context.Context, application.Invocation, application.RevokeUserSessionCommand) error
	RevokeUserSessions(context.Context, application.Invocation, application.RevokeUserSessionsCommand) error
}

type UserProfileApplication interface {
	SearchUsers(context.Context, application.Invocation, application.SearchUsersQuery) ([]*model.User, error)
	GetUserProfile(context.Context, application.Invocation, application.GetUserProfileQuery) (*model.User, error)
	UpdateUserProfile(context.Context, application.Invocation, application.UpdateUserProfileCommand) (*model.User, error)
	ChangeUserEmail(context.Context, application.Invocation, application.ChangeUserEmailCommand) (*application.UserEmailState, error)
	VerifyUserEmailPrivileged(context.Context, application.Invocation, application.VerifyUserEmailPrivilegedCommand) (*application.UserEmailState, error)
	UploadProfilePicture(context.Context, application.Invocation, application.UploadProfilePictureCommand) (*model.User, error)
	RemoveProfilePicture(context.Context, application.Invocation, application.RemoveProfilePictureCommand) (*model.User, error)
	GetProfilePicture(context.Context, application.Invocation, application.GetProfilePictureQuery) (*application.ProfilePictureContent, error)
}

type UserSettingsApplication interface {
	ReadOwnUserSettings(context.Context, application.Invocation) (application.UserSettingsView, error)
	ReplaceOwnUserSettings(context.Context, application.Invocation, application.ReplaceOwnUserSettingsCommand) (application.UserSettingsReplacementResult, error)
}

type AcademicUnitApplication interface {
	GetAcademicUnit(context.Context, application.Invocation, application.GetAcademicUnitQuery) (*model.AcademicUnit, error)
	ListAcademicUnits(context.Context, application.Invocation, application.ListAcademicUnitsQuery) ([]*model.AcademicUnit, error)
	SearchAcademicUnits(context.Context, application.Invocation, application.SearchAcademicUnitsQuery) ([]*model.AcademicUnit, error)
	CreateAcademicUnit(context.Context, application.Invocation, application.CreateAcademicUnitCommand) (*model.AcademicUnit, error)
	UpdateAcademicUnit(context.Context, application.Invocation, application.UpdateAcademicUnitCommand) (*model.AcademicUnit, error)
	ArchiveAcademicUnit(context.Context, application.Invocation, application.ArchiveAcademicUnitCommand) error
}

type InstitutionApplication interface {
	GetInstitution(context.Context, application.Invocation, application.GetInstitutionQuery) (*model.Institution, error)
	UpdateInstitution(context.Context, application.Invocation, application.UpdateInstitutionCommand) (*model.Institution, error)
}

type ProgrammeApplication interface {
	GetProgramme(context.Context, application.Invocation, application.GetProgrammeQuery) (*model.Programme, error)
	ListProgrammes(context.Context, application.Invocation, application.ListProgrammesQuery) ([]*model.Programme, error)
	CreateProgramme(context.Context, application.Invocation, application.CreateProgrammeCommand) (*model.Programme, error)
	UpdateProgramme(context.Context, application.Invocation, application.UpdateProgrammeCommand) (*model.Programme, error)
	ArchiveProgramme(context.Context, application.Invocation, application.ArchiveProgrammeCommand) error
}

type ProgrammeLevelApplication interface {
	GetProgrammeLevel(context.Context, application.Invocation, application.GetProgrammeLevelQuery) (*model.ProgrammeLevel, error)
	ListProgrammeLevels(context.Context, application.Invocation, application.ListProgrammeLevelsQuery) ([]*model.ProgrammeLevel, error)
	CreateProgrammeLevel(context.Context, application.Invocation, application.CreateProgrammeLevelCommand) (*model.ProgrammeLevel, error)
	UpdateProgrammeLevel(context.Context, application.Invocation, application.UpdateProgrammeLevelCommand) (*model.ProgrammeLevel, error)
	ArchiveProgrammeLevel(context.Context, application.Invocation, application.ArchiveProgrammeLevelCommand) error
}

type AcademicPeriodApplication interface {
	GetAcademicPeriod(context.Context, application.Invocation, application.GetAcademicPeriodQuery) (*model.AcademicPeriod, error)
	ListAcademicPeriods(context.Context, application.Invocation, application.ListAcademicPeriodsQuery) ([]*model.AcademicPeriod, error)
	CreateAcademicPeriod(context.Context, application.Invocation, application.CreateAcademicPeriodCommand) (*model.AcademicPeriod, error)
	UpdateAcademicPeriod(context.Context, application.Invocation, application.UpdateAcademicPeriodCommand) (*model.AcademicPeriod, error)
	ArchiveAcademicPeriod(context.Context, application.Invocation, application.ArchiveAcademicPeriodCommand) error
}

type ClassApplication interface {
	GetClass(context.Context, application.Invocation, application.GetClassQuery) (*model.Class, error)
	ListClasses(context.Context, application.Invocation, application.ListClassesQuery) ([]*model.Class, error)
	SearchClasses(context.Context, application.Invocation, application.SearchClassesQuery) ([]*model.Class, error)
	CreateClass(context.Context, application.Invocation, application.CreateClassCommand) (*model.Class, error)
	UpdateClass(context.Context, application.Invocation, application.UpdateClassCommand) (*model.Class, error)
	ArchiveClass(context.Context, application.Invocation, application.ArchiveClassCommand) error
}

type ExamApplication interface {
	CreateExam(context.Context, application.Invocation, application.CreateExamCommand) (application.ExamView, error)
	GetExam(context.Context, application.Invocation, application.GetExamQuery) (application.ExamView, error)
	EditExamDraftText(context.Context, application.Invocation, application.EditExamDraftTextCommand) (application.ExamView, error)
	ConfigureExamDraftFocusLoss(context.Context, application.Invocation, application.ConfigureExamDraftFocusLossCommand) (application.ExamView, error)
	ConfigureExamDraftExecutionProfile(context.Context, application.Invocation, application.ConfigureExamDraftExecutionProfileCommand) (application.ExamView, error)
	ListExamExecutionImages(context.Context, application.Invocation, application.GetExamQuery) ([]application.ExamExecutionImage, error)
	ListExams(context.Context, application.Invocation, application.ListExamsQuery) (application.ExamCatalogPage, error)
	ArchiveExam(context.Context, application.Invocation, application.ArchiveExamCommand) (model.Exam, error)
	ListExamManagers(context.Context, application.Invocation, application.ListExamManagersQuery) (application.ExamManagerPage, error)
	AddExamManager(context.Context, application.Invocation, application.AddExamManagerCommand) (application.ExamManagerChange, error)
	RemoveExamManager(context.Context, application.Invocation, application.RemoveExamManagerCommand) (application.ExamManagerChange, error)
	TransferExamOwnership(context.Context, application.Invocation, application.TransferExamOwnershipCommand) (application.ExamManagerChange, error)
}

type AffiliationApplication interface {
	ListAffiliations(context.Context, application.Invocation, application.ListAffiliationsQuery) ([]*model.Affiliation, error)
	CreateAffiliation(context.Context, application.Invocation, application.CreateAffiliationCommand) (*model.Affiliation, error)
	EndAffiliation(context.Context, application.Invocation, application.EndAffiliationCommand) (*model.Affiliation, error)
}

type AcademicUnitMemberApplication interface {
	ListAcademicUnitMembers(context.Context, application.Invocation, application.ListAcademicUnitMembersQuery) ([]*model.AcademicUnitMember, error)
	CreateAcademicUnitMember(context.Context, application.Invocation, application.CreateAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
	EndAcademicUnitMember(context.Context, application.Invocation, application.EndAcademicUnitMemberCommand) (*model.AcademicUnitMember, error)
}

type ClassMemberApplication interface {
	ListClassMembers(context.Context, application.Invocation, application.ListClassMembersQuery) ([]*model.ClassMember, error)
	EnrollClassMember(context.Context, application.Invocation, application.EnrollClassMemberCommand) (*model.ClassEnrollment, error)
	EndClassMember(context.Context, application.Invocation, application.EndClassMemberCommand) (*model.ClassMember, error)
}

type Sessions interface {
	ListSessions(context.Context, application.Invocation, application.ListSessionsQuery) ([]*model.Session, error)
	RevokeSession(context.Context, application.Invocation, application.RevokeSessionCommand) error
	RevokeAllSessions(context.Context, application.Invocation, application.RevokeAllSessionsCommand) error
}

type PersonalAccessTokens interface {
	CreatePersonalAccessToken(context.Context, application.Invocation, application.CreatePersonalAccessTokenCommand) (*model.PersonalAccessTokenCreation, error)
	ListPersonalAccessTokens(context.Context, application.Invocation, application.ListPersonalAccessTokensQuery) ([]*model.PersonalAccessToken, error)
	RevokePersonalAccessToken(context.Context, application.Invocation, application.RevokePersonalAccessTokenCommand) (*model.PersonalAccessToken, error)
	SetPersonalAccessTokenDisabled(context.Context, application.Invocation, application.SetPersonalAccessTokenDisabledCommand) (*model.PersonalAccessToken, error)
}

type MFA interface {
	GetMFAStatus(context.Context, application.Invocation, application.GetMFAStatusQuery) (*application.MFAStatus, error)
	SetupMFA(context.Context, application.Invocation, application.SetupMFACommand) (*application.MFASetup, error)
	ActivateMFA(context.Context, application.Invocation, application.ActivateMFACommand) (*application.MFAActivation, error)
	ChallengeMFA(context.Context, application.Invocation, application.ChallengeMFACommand) (*model.Session, error)
	RegenerateMFARecoveryCodes(context.Context, application.Invocation, application.RegenerateMFARecoveryCodesCommand) ([]string, error)
	DisableMFA(context.Context, application.Invocation, application.DisableMFACommand) error
}

type AuditListingApplication interface {
	ListAuditEvents(context.Context, application.Invocation, application.ListAuditEventsQuery) ([]*model.AuditEvent, error)
}

type BootstrapApplication interface {
	GetInstallationStatus(context.Context, application.GetInstallationStatusQuery) (*model.InstallationStatus, error)
	BootstrapInstallation(context.Context, application.Invocation, application.BootstrapInstallationCommand) (*model.InstallationBootstrapResult, error)
}

type RoleApplication interface {
	ListRoles(context.Context, application.Invocation, application.ListRolesQuery) ([]*model.Role, error)
	GetRole(context.Context, application.Invocation, application.GetRoleQuery) (*model.Role, error)
	CreateRole(context.Context, application.Invocation, application.CreateRoleCommand) (*model.Role, error)
	UpdateRole(context.Context, application.Invocation, application.UpdateRoleCommand) (*model.Role, error)
	ArchiveRole(context.Context, application.Invocation, application.ArchiveRoleCommand) error
}

type RoleBindingApplication interface {
	ListRoleBindings(context.Context, application.Invocation, application.ListRoleBindingsQuery) ([]*model.RoleBinding, error)
	CreateRoleBinding(context.Context, application.Invocation, application.CreateRoleBindingCommand) (*model.RoleBinding, error)
	EndRoleBinding(context.Context, application.Invocation, application.EndRoleBindingCommand) (*model.RoleBinding, error)
}

type Realtime interface {
	AuthorizeWebSocketSubscription(context.Context, model.Principal, model.RequestMetadata, model.Action, model.Resource) error
	ValidateWebSocketPrincipal(context.Context, model.Principal) error
}

// Application is the cohesive application-facing API contract. Its component
// interfaces keep domain ownership visible without turning authentication into
// an unrelated service locator.
type Application interface {
	Authentication
	DesktopAuthorization
	ExternalAuthentication
	authenticationMethodApplication
	Sessions
	PersonalAccessTokens
	MFA
	InstitutionApplication
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
	Realtime
}

type MailApplication interface {
	GetMailKeyState(context.Context, application.Invocation) (application.MailKeyStateView, error)
	StartMailRekey(context.Context, application.Invocation, string) (application.MailRekeyView, error)
	GetMailRekeyStatus(context.Context, application.Invocation, model.JobID) (application.MailRekeyStatusView, error)
	SendTestMail(context.Context, application.Invocation) (application.MailDeliveryView, error)
	GetMailMetrics(context.Context, application.Invocation) (application.MailMetricsSnapshot, error)
	GetMailDelivery(context.Context, application.Invocation, model.MailDeliveryID) (application.MailDeliveryView, error)
	ListMailDeliveries(context.Context, application.Invocation, application.ListMailDeliveriesQuery) (application.MailDeliveryPage, error)
	CancelMailDelivery(context.Context, application.Invocation, model.MailDeliveryID) (application.MailDeliveryView, error)
	RetryMailDelivery(context.Context, application.Invocation, model.MailDeliveryID) (application.MailDeliveryView, error)
}
