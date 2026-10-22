// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import "sort"

// Event-only audit actions name durable audit outcomes that are not
// permissions. Authorization never accepts them and no Role, Role Binding, or
// Personal Access Token can grant them; they appear only as AuditEvent.Action.
// Every permission Action is also an audit action, because authorization
// decisions and permission-checked mutations record the checked action.
const (
	ActionAuthenticationLogin                     Action = "authentication.login"
	ActionAuthenticationExternalLogin             Action = "authentication.external_login"
	ActionAuthenticationExternalProvision         Action = "authentication.external_provision"
	ActionAuthenticationPublicRegistration        Action = "authentication.public_registration"
	ActionAuthenticationReauthenticate            Action = "authentication.reauthenticate"
	ActionAuthenticationDesktopAuthorization      Action = "authentication.desktop_authorization"
	ActionAuthenticationDesktopSessionIssue       Action = "authentication.desktop_session_issue"
	ActionAuthenticationEmailVerificationRequest  Action = "authentication.email_verification.request"
	ActionAuthenticationEmailVerificationComplete Action = "authentication.email_verification.complete"
	ActionAuthenticationPasswordResetRequest      Action = "authentication.password_reset.request"
	ActionAuthenticationPasswordResetComplete     Action = "authentication.password_reset.complete"
	ActionAuthenticationAdministratorRecovery     Action = "authentication.administrator_recovery"
	ActionAuthenticationAdministratorMFAReset     Action = "authentication.administrator_mfa_reset"
	ActionInstallationBootstrap                   Action = "installation.bootstrap"
	ActionRoleSystemAdminReconcile                Action = "role.system_admin.reconcile"
	ActionInvitationAccept                        Action = "invitation.accept"
	ActionUserSearch                              Action = "user.search"
	ActionUserSettingsReplace                     Action = "user.settings.replace"
	ActionPersonalAccessTokenCreate               Action = "personal_access_token.create"
	ActionPersonalAccessTokenEnable               Action = "personal_access_token.enable"
	ActionPersonalAccessTokenDisable              Action = "personal_access_token.disable"
	ActionPersonalAccessTokenRevoke               Action = "personal_access_token.revoke"
	ActionMFASetup                                Action = "mfa.setup"
	ActionMFAActivate                             Action = "mfa.activate"
	ActionMFAChallenge                            Action = "mfa.challenge"
	ActionMFARecoveryCodesRegenerate              Action = "mfa.recovery_codes.regenerate"
	ActionMFADisable                              Action = "mfa.disable"
)

// eventAuditActionDefinition declares the audit-listing traits of one
// event-only action. AcademicAudit has the same meaning as on
// ActionDefinition.
type eventAuditActionDefinition struct {
	AcademicAudit bool
}

var eventAuditActions = map[Action]eventAuditActionDefinition{
	ActionAuthenticationLogin:                     {},
	ActionAuthenticationExternalLogin:             {},
	ActionAuthenticationExternalProvision:         {},
	ActionAuthenticationPublicRegistration:        {},
	ActionAuthenticationReauthenticate:            {},
	ActionAuthenticationDesktopAuthorization:      {},
	ActionAuthenticationDesktopSessionIssue:       {},
	ActionAuthenticationEmailVerificationRequest:  {},
	ActionAuthenticationEmailVerificationComplete: {},
	ActionAuthenticationPasswordResetRequest:      {},
	ActionAuthenticationPasswordResetComplete:     {},
	ActionAuthenticationAdministratorRecovery:     {},
	ActionAuthenticationAdministratorMFAReset:     {},
	ActionInstallationBootstrap:                   {},
	ActionRoleSystemAdminReconcile:                {},
	ActionInvitationAccept:                        {},
	ActionUserSearch:                              {AcademicAudit: true},
	ActionUserSettingsReplace:                     {},
	ActionPersonalAccessTokenCreate:               {},
	ActionPersonalAccessTokenEnable:               {},
	ActionPersonalAccessTokenDisable:              {},
	ActionPersonalAccessTokenRevoke:               {},
	ActionMFASetup:                                {},
	ActionMFAActivate:                             {},
	ActionMFAChallenge:                            {},
	ActionMFARecoveryCodesRegenerate:              {},
	ActionMFADisable:                              {},
}

// AuditActions returns every declared audit action in stable order.
func AuditActions() []string {
	actions := make([]string, 0, len(actionDefinitions)+len(eventAuditActions))
	for action := range actionDefinitions {
		actions = append(actions, string(action))
	}
	for action := range eventAuditActions {
		actions = append(actions, string(action))
	}
	sort.Strings(actions)
	return actions
}

// AcademicAuditActions returns, in stable order, the closed action catalog
// that academic audit visibility may list: every audit action declared with
// AcademicAudit.
func AcademicAuditActions() []string {
	actions := make([]string, 0, 32)
	for action, definition := range actionDefinitions {
		if definition.AcademicAudit {
			actions = append(actions, string(action))
		}
	}
	for action, definition := range eventAuditActions {
		if definition.AcademicAudit {
			actions = append(actions, string(action))
		}
	}
	sort.Strings(actions)
	return actions
}
