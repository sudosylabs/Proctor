// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import "time"

func constructAdministration(
	deps Dependencies,
	foundation applicationFoundation,
	access accessAcademicConstruction,
	identity identityConstruction,
) administrationConstruction {
	profileAuthorization := userProfileAuthorization{
		authorization: access.authorization,
		institutions:  deps.Store.Institution(),
	}
	roleAuthorization := roleAuthorization{
		authorization: access.authorization,
		institutions:  deps.Store.Institution(),
	}
	accountStates := newAccountStateService(accountStateServiceDependencies{Users: deps.Store.User(), Authorization: profileAuthorization, Capabilities: access.capabilities, Audit: mutationAuditAdapter{audit: foundation.audit}, Mail: identity.mail, Effects: accountStateRealtimeEffects{effects: foundation.realtime}, Now: time.Now})
	sessionAdministrations := newSessionAdministrationService(sessionAdministrationServiceDependencies{Sessions: deps.Store.Session(), Users: deps.Store.User(), Authorization: access.authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Mail: identity.mail, Effects: foundation.realtime, Now: time.Now})
	roleBindings := newRoleBindingService(roleBindingServiceDependencies{Bindings: deps.Store.RoleBinding(), Roles: deps.Store.Role(), Users: deps.Store.User(), Authorization: roleAuthorization, Capabilities: access.capabilities, Audit: mutationAuditAdapter{audit: foundation.audit}, Mail: foundation.mail, Effects: roleBindingRealtimeEffects{effects: foundation.realtime}, Now: time.Now})
	return administrationConstruction{
		accountStates:          accountStates,
		sessionAdministrations: sessionAdministrations,
		roles:                  newRoleService(roleServiceDependencies{Roles: deps.Store.Role(), Authorization: roleAuthorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Effects: roleRealtimeEffects{effects: foundation.realtime}, Now: time.Now}),
		roleBindings:           roleBindings,
		academicAdministrationBatches: newAcademicAdministrationBatchService(academicAdministrationBatchServiceDependencies{Commands: academicAdministrationCommandServices{affiliations: access.affiliations, academicUnitMembers: access.academicUnitMembers,
			classMembers: access.classMembers, roleBindings: roleBindings, accountStates: accountStates, sessions: sessionAdministrations}, Authorization: invitationAuthorizationAdapter{authorization: access.authorization}, Outcomes: deps.Store.CommandOutcome(), Now: time.Now, RecentTTL: deps.RecentAuthenticationTTL}),
		auditListings: newAuditListingService(auditListingServiceDependencies{Audits: deps.Store.Audit(), Authorization: auditListingAuthorization{authorization: access.authorization, institutions: deps.Store.Institution()}}),
		bootstrap:     newBootstrapService(bootstrapServiceDependencies{Installations: deps.Store.Installation(), Hasher: foundation.hasher, Attempts: foundation.attempts, RateLimit: deps.LoginRateLimit, Protection: deps.BootstrapProtection, NodeID: deps.NodeID, Now: time.Now, RecoveryPolicy: administratorRecoveryPolicy{capabilities: access.capabilities, mfaEnabled: deps.MFA.Enabled}}),
	}
}
