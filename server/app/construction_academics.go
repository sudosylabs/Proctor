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
	return accessAcademicConstruction{
		authorization: authorization,
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
			deps.Store.ProgrammeLevel(), deps.Store.Programme(),
			academicAuthorization, mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		academicPeriods: newAcademicPeriodService(
			deps.Store.AcademicPeriod(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		classes: newClassService(
			deps.Store.Class(), deps.Store.ProgrammeLevel(),
			deps.Store.Programme(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		affiliations: newAffiliationService(
			deps.Store.Affiliation(), deps.Store.ClassMember(),
			academicAuthorization, mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		academicUnitMembers: newAcademicUnitMemberService(
			deps.Store.AcademicUnitMember(), academicAuthorization,
			mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
		classMembers: newClassMemberService(
			deps.Store.ClassMember(), deps.Store.Class(),
			academicAuthorization, mutationAuditAdapter{audit: foundation.audit}, time.Now, model.NewId,
		),
	}, nil
}
