// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func constructAccessAndAcademics(
	deps Dependencies,
	foundation applicationFoundation,
) (accessAcademicConstruction, error) {
	scopeResolver, err := newAccessScopeResolver(
		deps.Store.Institution(), deps.Store.AcademicUnit(), deps.Store.Class(), deps.Store.User(), deps.Store.ClassMember(),
		deps.Store.ExamAuthoring(),
		deps.Store.ExamSitting(),
		deps.Store.ExamSubmission(),
		deps.Store.AcademicPeriod(),
	)
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	scopeResolver.programmes = deps.Store.Programme()
	scopeResolver.programmeLevels = deps.Store.ProgrammeLevel()
	authorization, err := newAccessControlService(
		deps.Store.Role(), deps.Store.RoleBinding(), scopeResolver, foundation.audit,
	)
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	capabilities := deploymentAccessPolicyCapabilities{
		providers: deps.Registry, mail: deps.MailDeliverySender, health: foundation.mailHealth,
	}
	accessPolicies, err := newAccessPolicyService(
		deps.Store.AccessPolicy(), deps.Store.Institution(), authorization,
		capabilities,
		mutationAuditAdapter{audit: foundation.audit},
		accessPolicyRealtimeEffects{realtime: foundation.realtime},
		accessPolicyEffectReporter{realtime: foundation.realtime},
		deps.PublicURL, deps.RecentAuthenticationTTL, time.Now,
	)
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	retentionPolicy, err := newRetentionPolicyService(
		deps.Store.RetentionPolicy(), deps.Store.Retention(), deps.Store.Institution(), authorization,
		mutationAuditAdapter{audit: foundation.audit}, deps.RecentAuthenticationTTL, time.Now,
	)
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	desktopCompatibility, err := newDesktopCompatibilityService(
		deps.Store.DesktopCompatibilityPolicy(),
		deps.Store.Institution(),
		authorization,
		mutationAuditAdapter{audit: foundation.audit},
		deps.DesktopBuildCatalog,
		deps.SkipDesktopCompatibility,
		deps.RecentAuthenticationTTL,
		time.Now,
	)
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	return accessAcademicConstruction{
		authorization: authorization, capabilities: capabilities, accessPolicies: accessPolicies,
		desktopCompatibility: desktopCompatibility,
		retentionPolicy:      retentionPolicy,
		academicUnits: newAcademicUnitQueryService(
			deps.Store.AcademicUnit(), authorization,
		),
		academicUnitCommands: newAcademicUnitCommandService(
			deps.Store.AcademicUnit(), authorization,
			mutationAuditAdapter{audit: foundation.audit},
			academicUnitRealtimeEffects{realtime: foundation.realtime},
			academicUnitEffectReporter{realtime: foundation.realtime},
			time.Now, model.NewID,
		),
		institutions: newInstitutionService(
			deps.Store.Institution(), authorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now,
		),
		programmes: newProgrammeService(
			deps.Store.Programme(), authorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewID,
		),
		programmeLevels: newProgrammeLevelService(
			deps.Store.ProgrammeLevel(), authorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewID,
		),
		academicPeriods: newAcademicPeriodService(
			deps.Store.AcademicPeriod(), authorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewID,
		),
		classes: newClassService(
			deps.Store.Class(), authorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewID,
		),
		affiliations: newAffiliationService(
			deps.Store.Affiliation(), deps.Store.ClassMember(),
			authorization, mutationAuditAdapter{audit: foundation.audit},
			affiliationRealtimeEffects{realtime: foundation.realtime}, time.Now, model.NewID,
		),
		academicUnitMembers: newAcademicUnitMemberService(
			deps.Store.AcademicUnitMember(), deps.Store.User(), authorization,
			mutationAuditAdapter{audit: foundation.audit}, foundation.mail, foundation.realtime, time.Now, model.NewID,
		),
		classMembers: newClassMemberService(
			deps.Store.ClassMember(), deps.Store.Class(), deps.Store.User(),
			authorization, mutationAuditAdapter{audit: foundation.audit}, foundation.mail,
			classMemberRealtimeEffects{sittings: deps.Store.ExamSitting(), realtime: foundation.realtime},
			time.Now, model.NewID,
		),
	}, nil
}
