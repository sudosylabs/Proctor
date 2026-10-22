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
	scopeResolver, err := newAccessScopeResolver(accessScopeResolverDependencies{Institutions: deps.Store.Institution(), AcademicUnits: deps.Store.AcademicUnit(), Classes: deps.Store.Class(), Users: deps.Store.User(), ClassMembers: deps.Store.ClassMember(), Exams: deps.Store.ExamAuthoring(), Sittings: deps.Store.ExamSitting(), Submissions: deps.Store.ExamSubmission(), AcademicPeriods: deps.Store.AcademicPeriod()})
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	scopeResolver.programmes = deps.Store.Programme()
	scopeResolver.programmeLevels = deps.Store.ProgrammeLevel()
	authorization, err := newAccessControlService(accessControlServiceDependencies{Roles: deps.Store.Role(), Bindings: deps.Store.RoleBinding(), Resolver: scopeResolver, Audit: foundation.audit})
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	capabilities := deploymentAccessPolicyCapabilities{
		providers: deps.Registry, mail: deps.MailDeliverySender, health: foundation.mailHealth,
	}
	accessPolicies, err := newAccessPolicyService(accessPolicyServiceDependencies{Policies: deps.Store.AccessPolicy(), Institutions: deps.Store.Institution(), Authorization: authorization, Capabilities: capabilities, Audit: mutationAuditAdapter{audit: foundation.audit}, Effects: accessPolicyRealtimeEffects{realtime: foundation.realtime}, EffectFailures: accessPolicyEffectReporter{realtime: foundation.realtime}, CanonicalOrigin: deps.PublicURL, RecentAuthenticationTTL: deps.RecentAuthenticationTTL, Now: time.Now})
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	retentionPolicy, err := newRetentionPolicyService(retentionPolicyServiceDependencies{Policies: deps.Store.RetentionPolicy(), Lifecycle: deps.Store.Retention(), Institutions: deps.Store.Institution(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, RecentAuthenticationTTL: deps.RecentAuthenticationTTL, Now: time.Now})
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	desktopCompatibility, err := newDesktopCompatibilityService(desktopCompatibilityServiceDependencies{Policies: deps.Store.DesktopCompatibilityPolicy(), Institutions: deps.Store.Institution(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Builds: deps.DesktopBuildCatalog, SkipBuildChecks: deps.SkipDesktopCompatibility, RecentAuthenticationTTL: deps.RecentAuthenticationTTL, Now: time.Now})
	if err != nil {
		return accessAcademicConstruction{}, err
	}
	return accessAcademicConstruction{
		authorization: authorization, capabilities: capabilities, accessPolicies: accessPolicies,
		desktopCompatibility: desktopCompatibility,
		retentionPolicy:      retentionPolicy,
		academicUnits:        newAcademicUnitQueryService(academicUnitQueryServiceDependencies{AcademicUnits: deps.Store.AcademicUnit(), Authorization: authorization}),
		academicUnitCommands: newAcademicUnitCommandService(academicUnitCommandServiceDependencies{Store: deps.Store.AcademicUnit(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Effects: academicUnitRealtimeEffects{realtime: foundation.realtime}, EffectFailures: academicUnitEffectReporter{realtime: foundation.realtime}, Now: time.Now, NewID: model.NewID}),
		institutions:         newInstitutionService(institutionServiceDependencies{Store: deps.Store.Institution(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Now: time.Now}),
		programmes:           newProgrammeService(programmeServiceDependencies{Store: deps.Store.Programme(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Now: time.Now, NewID: model.NewID}),
		programmeLevels:      newProgrammeLevelService(programmeLevelServiceDependencies{Store: deps.Store.ProgrammeLevel(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Now: time.Now, NewID: model.NewID}),
		academicPeriods:      newAcademicPeriodService(academicPeriodServiceDependencies{Store: deps.Store.AcademicPeriod(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Now: time.Now, NewID: model.NewID}),
		classes:              newClassService(classServiceDependencies{Store: deps.Store.Class(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Now: time.Now, NewID: model.NewID}),
		affiliations:         newAffiliationService(affiliationServiceDependencies{Store: deps.Store.Affiliation(), Enrollments: deps.Store.ClassMember(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Effects: affiliationRealtimeEffects{realtime: foundation.realtime}, Now: time.Now, NewID: model.NewID}),
		academicUnitMembers:  newAcademicUnitMemberService(academicUnitMemberServiceDependencies{Store: deps.Store.AcademicUnitMember(), Users: deps.Store.User(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Mail: foundation.mail, Effects: foundation.realtime, Now: time.Now, NewID: model.NewID}),
		classMembers:         newClassMemberService(classMemberServiceDependencies{Store: deps.Store.ClassMember(), Classes: deps.Store.Class(), Users: deps.Store.User(), Authorization: authorization, Audit: mutationAuditAdapter{audit: foundation.audit}, Mail: foundation.mail, Effects: classMemberRealtimeEffects{sittings: deps.Store.ExamSitting(), realtime: foundation.realtime}, Now: time.Now, NewID: model.NewID}),
	}, nil
}
