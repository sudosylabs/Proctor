//go:build integration

// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import "testing"

// TestAccessAndOnboardingServerPhaseCertification is the single real-graph
// entry point for the accepted server phase. Each subtest owns a pristine
// PostgreSQL installation and constructs the production graph through
// testlib; the suite deliberately does not claim the deferred hosted pages or
// Desktop LaunchWindow UI.
func TestAccessAndOnboardingServerPhaseCertification(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{"bootstrap-policy-role-and-assurance", TestBootstrapAndRoleAdministrationIntegration},
		{"two-node-access-policy", TestAccessPolicyTwoNodePostgreSQLFenceAndSafeRevisionFanout},
		{"desktop-browser-continuation", TestDesktopAuthorizationContinuesAcrossNodesAndCreatesAnOrdinaryRotatingSession},
		{"local-credentials-sessions-and-pats", TestAuthenticationIntegration},
		{"public-local-registration", TestPublicLocalRegistrationRealGraph},
		{"password-recovery-policy", TestCurrentAccessPolicyGatesLocalLoginAndPasswordRecoveryRealGraph},
		{"offline-administrator-recovery", TestOfflineAdministratorRecoveryReconcilesBeforeNormalAuthentication},
		{"cas-provider-admission", TestCASExternalAuthenticationIntegration},
		{"oidc-provider-admission", TestOIDCExternalAuthenticationIntegration},
		{"invitation-json-batch-recovery", TestInvitationBatchCommitsMixedRowsAndRecoversAcrossRequests},
		{"student-invitation-cross-node", TestStudentClassInvitationIssuesMailAndAcceptsAcrossNodes},
		{"teacher-invitation-cross-node", TestTeacherAcademicUnitInvitationIssuesMailAndAcceptsAcrossNodes},
		{"scoped-role-invitation-cross-node", TestScopedRoleInvitationAcceptsAsExistingUserAcrossNodesWithoutProfileOrWelcomeMutation},
		{"academic-csv-json-progression-mail-report", TestAcademicMembershipAndUserAdministrationIntegration},
		{"secret-free-fanout-failure", TestAuthenticationFanoutFailureIntegration},
	}
	for _, test := range tests {
		t.Run(test.name, test.run)
	}
}
