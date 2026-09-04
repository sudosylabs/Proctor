// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import "errors"

// resourceApplications is the fully resolved application graph consumed by
// the HTTP resource inventory. Construction validates it once; resource
// declaration performs no dependency discovery or fallback selection.
type resourceApplications struct {
	authenticator Authenticator

	authentication         authenticationEntryApplication
	authenticationMethods  authenticationMethodApplication
	desktopAuthorization   DesktopAuthorization
	desktopRegistrations   DesktopRegistrations
	externalAuthentication externalAuthenticationEntryApplication
	browserInvitations     BrowserInvitationApplication

	userProfiles          UserProfileApplication
	userSettings          UserSettingsApplication
	accountStates         AccountStateApplication
	sessionAdministration SessionAdministrationApplication
	sessions              Sessions
	mfa                   MFA
	personalAccessTokens  PersonalAccessTokens

	institutions        InstitutionApplication
	academicUnits       AcademicUnitApplication
	programmes          ProgrammeApplication
	programmeLevels     ProgrammeLevelApplication
	academicPeriods     AcademicPeriodApplication
	classes             ClassApplication
	affiliations        AffiliationApplication
	academicUnitMembers AcademicUnitMemberApplication
	classMembers        ClassMemberApplication

	exams                ExamApplication
	examRevisions        ExamRevisionApplication
	examSittings         ExamSittingApplication
	examCorrections      ExamSittingCorrectionApplication
	examResources        ExamResourceApplication
	examStarterWorkspace ExamStarterWorkspaceApplication
	examAttempts         ExamAttemptApplication
	examIntegrityReviews ExamIntegrityReviewApplication

	invitations                   InvitationApplication
	onboardingImports             OnboardingImportApplication
	studentProgressions           StudentProgressionApplication
	academicAdministrationBatches AcademicAdministrationBatchApplication
	roles                         RoleApplication
	roleBindings                  RoleBindingApplication
	accessPolicy                  AccessPolicyApplication
	desktopCompatibility          DesktopCompatibilityApplication

	audit     AuditListingApplication
	jobs      JobOperationsApplication
	mail      MailApplication
	bootstrap BootstrapApplication
}

func resolveResourceApplications(options Options) (resourceApplications, error) {
	if options.Application == nil {
		return resourceApplications{}, errors.New("application is required")
	}
	application := options.Application
	// These assignments are deliberately compile-time projections. Adding a
	// broad-Application resource requires changing its declared contract, not a
	// runtime assertion hidden in catalog construction.
	var authenticator Authenticator = application
	var authentication authenticationEntryApplication = application
	var authenticationMethods authenticationMethodApplication = application
	var desktopAuthorization DesktopAuthorization = application
	var desktopRegistrations DesktopRegistrations = application
	var externalAuthentication externalAuthenticationEntryApplication = application
	var sessions Sessions = application
	var mfa MFA = application
	var personalAccessTokens PersonalAccessTokens = application
	var exams ExamApplication = application
	var examRevisions ExamRevisionApplication = application
	var examSittings ExamSittingApplication = application
	var examCorrections ExamSittingCorrectionApplication = application
	var examResources ExamResourceApplication = application
	var examStarterWorkspace ExamStarterWorkspaceApplication = application
	var examAttempts ExamAttemptApplication = application
	var examIntegrityReviews ExamIntegrityReviewApplication = application
	var jobs JobOperationsApplication = application
	var desktopCompatibility DesktopCompatibilityApplication = application

	required := []struct {
		missing bool
		message string
	}{
		{options.AcademicUnits == nil, "academic unit reads are required"},
		{options.Institutions == nil, "institution application is required"},
		{options.Programmes == nil, "programme application is required"},
		{options.ProgrammeLevels == nil, "programme level application is required"},
		{options.AcademicPeriods == nil, "academic period application is required"},
		{options.Classes == nil, "class application is required"},
		{options.Affiliations == nil, "affiliation application is required"},
		{options.AcademicUnitMembers == nil, "academic unit member application is required"},
		{options.ClassMembers == nil, "class member application is required"},
		{options.Invitations == nil, "invitation application is required"},
		{options.BrowserInvitations == nil, "browser invitation application is required"},
		{options.OnboardingImports == nil, "onboarding import application is required"},
		{options.StudentProgressions == nil, "student progression application is required"},
		{options.AcademicAdministrationBatches == nil, "academic administration batch application is required"},
		{options.UserProfiles == nil, "user profile application is required"},
		{options.UserSettings == nil, "user settings application is required"},
		{options.AccountStates == nil, "account state application is required"},
		{options.SessionAdministrations == nil, "session administration application is required"},
		{options.Roles == nil, "role application is required"},
		{options.RoleBindings == nil, "role binding application is required"},
		{options.AuditListings == nil, "audit listing application is required"},
		{options.Bootstrap == nil, "bootstrap application is required"},
		{options.AccessPolicy == nil, "access policy application is required"},
		{options.Mail == nil, "mail application is required"},
	}
	for _, dependency := range required {
		if dependency.missing {
			return resourceApplications{}, errors.New(dependency.message)
		}
	}

	return resourceApplications{
		authenticator:  authenticator,
		authentication: authentication, authenticationMethods: authenticationMethods,
		desktopAuthorization: desktopAuthorization, externalAuthentication: externalAuthentication,
		desktopRegistrations: desktopRegistrations,
		browserInvitations:   options.BrowserInvitations,
		userProfiles:         options.UserProfiles, userSettings: options.UserSettings,
		accountStates: options.AccountStates, sessionAdministration: options.SessionAdministrations,
		sessions: sessions, mfa: mfa, personalAccessTokens: personalAccessTokens,
		institutions: options.Institutions, academicUnits: options.AcademicUnits,
		programmes: options.Programmes, programmeLevels: options.ProgrammeLevels,
		academicPeriods: options.AcademicPeriods, classes: options.Classes,
		affiliations: options.Affiliations, academicUnitMembers: options.AcademicUnitMembers,
		classMembers: options.ClassMembers,
		exams:        exams, examRevisions: examRevisions, examSittings: examSittings,
		examCorrections: examCorrections, examResources: examResources,
		examStarterWorkspace: examStarterWorkspace, examAttempts: examAttempts,
		examIntegrityReviews: examIntegrityReviews,
		invitations:          options.Invitations, onboardingImports: options.OnboardingImports,
		studentProgressions:           options.StudentProgressions,
		academicAdministrationBatches: options.AcademicAdministrationBatches,
		roles:                         options.Roles, roleBindings: options.RoleBindings, accessPolicy: options.AccessPolicy,
		desktopCompatibility: desktopCompatibility,
		audit:                options.AuditListings, jobs: jobs, mail: options.Mail, bootstrap: options.Bootstrap,
	}, nil
}
