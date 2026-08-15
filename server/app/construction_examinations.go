// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	examworkspace "github.com/sudosylabs/proctor/server/app/exam/workspace"
	"github.com/sudosylabs/proctor/server/model"
)

func constructExaminations(deps Dependencies, foundation applicationFoundation, access accessAcademicConstruction) (examinationConstruction, error) {
	effects := examRealtimeEffects{realtime: foundation.realtime}
	authoring, err := examengine.NewAuthoring(
		deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(), deps.Store.User(),
		examAuthorizationAdapter{authorization: access.authorization},
		examAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		effects, effects, time.Now, model.NewExamID,
	)
	if err != nil {
		return examinationConstruction{}, err
	}
	revisions, err := examengine.NewPublication(
		deps.Store.ExamRevision(), deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(),
		examAuthorizationAdapter{authorization: access.authorization},
		examAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		effects, effects, time.Now, model.NewExamRevisionID,
	)
	if err != nil {
		return examinationConstruction{}, err
	}
	sittings, err := examsitting.New(
		deps.Store.ExamSitting(), deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(),
		examSittingAuthorizationAdapter{authorization: access.authorization},
		examSittingAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		examSittingRealtimeEffects{realtime: foundation.realtime},
		examSittingRealtimeEffects{realtime: foundation.realtime},
		time.Now, model.NewExamSittingID,
	)
	if err != nil {
		return examinationConstruction{}, err
	}
	resources, err := examresource.New(
		deps.Store.ExamResource(), deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(),
		examResourceAuthorizationAdapter{authorization: access.authorization},
		examResourceAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		examResourceRealtimeEffects{realtime: foundation.realtime},
		examResourceRealtimeEffects{realtime: foundation.realtime},
		deps.FileContent, time.Now, model.NewExamResourceID, model.NewFileEntryID,
		model.NewFileRevisionID, model.NewUploadLeaseID,
	)
	if err != nil {
		return examinationConstruction{}, err
	}
	starterWorkspace, err := examworkspace.NewService(
		deps.Store.ExamStarterWorkspace(), deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(),
		examStarterWorkspaceAuthorizationAdapter{authorization: access.authorization},
		examStarterWorkspaceAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		deps.FileContent,
		examStarterWorkspaceRealtimeEffects{realtime: foundation.realtime},
		examStarterWorkspaceRealtimeEffects{realtime: foundation.realtime},
		time.Now, model.NewStarterWorkspaceEntryID, model.NewStarterWorkspaceObjectID,
		model.NewWorkspaceContentVersion,
	)
	if err != nil {
		return examinationConstruction{}, err
	}
	return examinationConstruction{authoring: authoring, revisions: revisions, sittings: sittings, resources: resources, starterWorkspace: starterWorkspace}, nil
}
