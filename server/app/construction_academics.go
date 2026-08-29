// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

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
	academicAuthorization := academicUnitAuthorization{
		authorization: authorization,
		institutions:  deps.Store.Institution(),
	}
	capabilities := deploymentAccessPolicyCapabilities{
		providers: deps.Registry, mail: deps.MailDeliverySender, health: foundation.mailHealth,
	}
	accessPolicies, err := newAccessPolicyService(
		deps.Store.AccessPolicy(), deps.Store.Institution(), academicAuthorization,
		capabilities,
		mutationAuditAdapter{audit: foundation.audit},
		accessPolicyRealtimeEffects{realtime: foundation.realtime},
		accessPolicyEffectReporter{realtime: foundation.realtime},
		deps.PublicURL, deps.RecentAuthenticationTTL, time.Now,
	)
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	desktopCompatibility, err := newDesktopCompatibilityService(
		deps.Store.DesktopCompatibilityPolicy(),
		deps.Store.Institution(),
		academicAuthorization,
		mutationAuditAdapter{audit: foundation.audit},
		deps.DesktopBuildCatalog,
		deps.RecentAuthenticationTTL,
		time.Now,
	)
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	return accessAcademicConstruction{
		authorization: authorization, capabilities: capabilities, accessPolicies: accessPolicies,
		desktopCompatibility: desktopCompatibility,
		academicUnits: newAcademicUnitQueryService(
			deps.Store.AcademicUnit(), academicAuthorization,
		),
		academicUnitCommands: newAcademicUnitCommandService(
			deps.Store.AcademicUnit(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit},
			academicUnitRealtimeEffects{realtime: foundation.realtime},
			academicUnitEffectReporter{realtime: foundation.realtime},
			time.Now, model.NewId,
		),
		institutions: newInstitutionService(
			deps.Store.Institution(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now,
		),
		programmes: newProgrammeService(
			deps.Store.Programme(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		programmeLevels: newProgrammeLevelService(
			deps.Store.ProgrammeLevel(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		academicPeriods: newAcademicPeriodService(
			deps.Store.AcademicPeriod(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		classes: newClassService(
			deps.Store.Class(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		affiliations: newAffiliationService(
			deps.Store.Affiliation(), deps.Store.ClassMember(),
			academicAuthorization, mutationAuditAdapter{audit: foundation.audit},
			affiliationRealtimeEffects{realtime: foundation.realtime}, time.Now, model.NewId,
		),
		academicUnitMembers: newAcademicUnitMemberService(
			deps.Store.AcademicUnitMember(), deps.Store.User(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, foundation.mail, foundation.realtime, time.Now, model.NewId,
		),
		classMembers: newClassMemberService(
			deps.Store.ClassMember(), deps.Store.Class(), deps.Store.User(),
			academicAuthorization, mutationAuditAdapter{audit: foundation.audit}, foundation.mail,
			classMemberRealtimeEffects{sittings: deps.Store.ExamSitting(), realtime: foundation.realtime},
			time.Now, model.NewId,
		),
	}, nil
}
