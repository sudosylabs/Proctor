// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	accountStates := newAccountStateService(
		deps.Store.User(), profileAuthorization, access.capabilities,
		mutationAuditAdapter{audit: foundation.audit}, identity.mail,
		accountStateRealtimeEffects{effects: foundation.realtime}, time.Now,
	)
	sessionAdministrations := newSessionAdministrationService(
		deps.Store.Session(), deps.Store.User(),
		sessionAdministrationAuthorization{authorization: access.authorization},
		mutationAuditAdapter{audit: foundation.audit}, identity.mail,
		sessionAdministrationRealtimeEffects{effects: foundation.realtime}, time.Now,
	)
	roleBindings := newRoleBindingService(
		deps.Store.RoleBinding(), deps.Store.Role(), roleAuthorization, access.capabilities,
		mutationAuditAdapter{audit: foundation.audit}, roleBindingRealtimeEffects{effects: foundation.realtime}, time.Now,
	)
	return administrationConstruction{
		accountStates:          accountStates,
		sessionAdministrations: sessionAdministrations,
		roles: newRoleService(
			deps.Store.Role(),
			roleAuthorization,
			mutationAuditAdapter{audit: foundation.audit},
			roleRealtimeEffects{effects: foundation.realtime},
			time.Now,
		),
		roleBindings: roleBindings,
		academicAdministrationBatches: newAcademicAdministrationBatchService(
			academicAdministrationCommandServices{affiliations: access.affiliations, academicUnitMembers: access.academicUnitMembers,
				classMembers: access.classMembers, roleBindings: roleBindings, accountStates: accountStates, sessions: sessionAdministrations},
			invitationAuthorizationAdapter{authorization: access.authorization}, deps.Store.CommandOutcome(), time.Now, deps.RecentAuthenticationTTL,
		),
		auditListings: newAuditListingService(
			deps.Store.Audit(),
			auditListingAuthorization{authorization: access.authorization, institutions: deps.Store.Institution()},
		),
		bootstrap: newBootstrapService(
			deps.Store.Installation(),
			foundation.hasher,
			foundation.attempts,
			deps.LoginRateLimit,
			deps.BootstrapProtection,
			deps.NodeID,
			time.Now,
		),
	}
}
