// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

// productionResources is the single, visible inventory of HTTP resource
// modules. Each resource owns its route declarations and DTO adapters; the
// catalog compiler validates and seals the combined manifest in New.
func productionResources(options Options, cookies browserCookies, webSocket WebSocketTransport) []resource {
	accessPolicy := options.AccessPolicy
	if accessPolicy == nil {
		accessPolicy = unavailableAccessPolicyApplication{}
	}
	invitations := options.Invitations
	if invitations == nil {
		invitations, _ = options.Application.(InvitationApplication)
	}
	onboardingImports := options.OnboardingImports
	if onboardingImports == nil {
		onboardingImports, _ = options.Application.(OnboardingImportApplication)
	}
	if onboardingImports == nil {
		onboardingImports = unavailableOnboardingImportApplication{}
	}
	studentProgressions := options.StudentProgressions
	if studentProgressions == nil {
		studentProgressions, _ = options.Application.(StudentProgressionApplication)
	}
	if studentProgressions == nil {
		studentProgressions = unavailableStudentProgressionApplication{}
	}
	if invitations == nil {
		invitations = unavailableInvitationApplication{}
	}
	academicAdministrationBatches := options.AcademicAdministrationBatches
	if academicAdministrationBatches == nil {
		academicAdministrationBatches, _ = options.Application.(AcademicAdministrationBatchApplication)
	}
	return []resource{
		systemResource(options.Health, options.BuildInfo),
		bootstrapResource(options.Bootstrap),
		accessPolicyResource(accessPolicy),
		authenticationResource(options.Application, cookies),
		authenticationMethodResource(options.Application, cookies),
		desktopAuthorizationResource(options.Application),
		externalAuthenticationResource(options.Application, cookies),
		userProfileResource(options.UserProfiles),
		userSettingsResource(options.UserSettings),
		userAdministrationResource(options.AccountStates, options.SessionAdministrations),
		sessionResource(options.Application, cookies),
		mfaResource(options.Application),
		personalAccessTokenResource(options.Application),
		institutionResource(options.Institutions),
		academicUnitResource(options.AcademicUnits),
		examResource(options.Application),
		examRevisionResource(options.Application),
		examSittingResource(options.Application),
		examSittingCorrectionResource(options.Application),
		examResourceHTTPResource(options.Application),
		examStarterWorkspaceHTTPResource(options.Application),
		examAttemptResource(options.Application),
		examIntegrityReviewResource(options.Application),
		programmeResource(options.Programmes),
		programmeLevelResource(options.ProgrammeLevels),
		academicPeriodResource(options.AcademicPeriods),
		classResource(options.Classes),
		affiliationResource(options.Affiliations),
		academicUnitMemberResource(options.AcademicUnitMembers),
		classMemberResource(options.ClassMembers),
		invitationResource(invitations),
		onboardingImportResource(onboardingImports),
		studentProgressionResource(studentProgressions),
		academicAdministrationBatchResource(academicAdministrationBatches),
		roleResource(options.Roles),
		roleBindingResource(options.RoleBindings),
		auditResource(options.AuditListings),
		jobResource(options.Application),
		mailResource(options.Mail),
		webSocketResource(webSocket),
	}
}
