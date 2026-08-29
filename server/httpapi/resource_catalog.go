// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import "time"

// productionResources is the single, visible and policy-free inventory of HTTP
// resource modules. Preserve this order: it is part of route-catalog review.
func productionResources(
	applications resourceApplications,
	health Health,
	buildInfo BuildInfo,
	cookies browserCookies,
	webSocket WebSocketTransport,
) []resource {
	return []resource{
		systemResourceWithApplication(health, buildInfo, applications.desktopCompatibility, time.Now),
		bootstrapResource(applications.bootstrap),
		accessPolicyResource(applications.accessPolicy),
		desktopCompatibilityPolicyResource(applications.desktopCompatibility),
		authenticationResource(applications.authentication, cookies),
		authenticationMethodResource(applications.authenticationMethods, cookies),
		desktopAuthorizationResource(applications.desktopAuthorization, cookies),
		desktopRegistrationResource(applications.desktopRegistrations),
		externalAuthenticationResource(applications.externalAuthentication, cookies),
		browserInvitationResource(applications.browserInvitations, cookies),
		userProfileResource(applications.userProfiles),
		userSettingsResource(applications.userSettings),
		userAdministrationResource(applications.accountStates, applications.sessionAdministration),
		sessionResource(applications.sessions, cookies),
		mfaResource(applications.mfa),
		personalAccessTokenResource(applications.personalAccessTokens),
		institutionResource(applications.institutions),
		academicUnitResource(applications.academicUnits),
		examResource(applications.exams),
		examRevisionResource(applications.examRevisions),
		examSittingResource(applications.examSittings),
		examSittingCorrectionResource(applications.examCorrections),
		examResourceHTTPResource(applications.examResources),
		examStarterWorkspaceHTTPResource(applications.examStarterWorkspace),
		examAttemptResource(applications.examAttempts),
		examIntegrityReviewResource(applications.examIntegrityReviews),
		programmeResource(applications.programmes),
		programmeLevelResource(applications.programmeLevels),
		academicPeriodResource(applications.academicPeriods),
		classResource(applications.classes),
		affiliationResource(applications.affiliations),
		academicUnitMemberResource(applications.academicUnitMembers),
		classMemberResource(applications.classMembers),
		invitationResource(applications.invitations),
		onboardingImportResource(applications.onboardingImports),
		studentProgressionResource(applications.studentProgressions),
		academicAdministrationBatchResource(applications.academicAdministrationBatches),
		roleResource(applications.roles),
		roleBindingResource(applications.roleBindings),
		auditResource(applications.audit),
		jobResource(applications.jobs),
		mailResource(applications.mail),
		webSocketResource(webSocket),
	}
}
