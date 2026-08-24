// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	examattempt "github.com/sudosylabs/proctor/server/app/exam/attempt"
	examcorrection "github.com/sudosylabs/proctor/server/app/exam/correction"
	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	examreview "github.com/sudosylabs/proctor/server/app/exam/review"
	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	examworkspace "github.com/sudosylabs/proctor/server/app/exam/workspace"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
)

func constructExaminations(deps Dependencies, foundation applicationFoundation, access accessAcademicConstruction) (examinationConstruction, error) {
	execution, err := appexecution.New(deps.Store.ExecutionGrant(), deps.ExecutionHosts, deps.FileContent, time.Now, model.NewExecutionGrantID)
	if err != nil {
		return examinationConstruction{}, err
	}
	effects := examRealtimeEffects{realtime: foundation.realtime}
	authoring, err := examengine.NewAuthoring(
		deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(), deps.Store.User(),
		foundation.mail,
		examAuthorizationAdapter{authorization: access.authorization},
		examAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		deps.Store.CommandOutcome(), examExecutionProfileCatalog{execution: execution},
		effects, effects, time.Now, model.NewExamID,
	)
	if err != nil {
		return examinationConstruction{}, err
	}
	sittingRenderer, ok := deps.MailTemplateRenderer.(appmail.SittingRenderer)
	if !ok {
		return examinationConstruction{}, errors.New("Sitting mail template renderer is unavailable")
	}
	sittingMail, err := newSittingMailPreparer(sittingRenderer, deps.MailDeliverySender, deps.MailSecretSealer, time.Now)
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
	sittingMailPreparation := sittingScheduleMailPreparationAdapter{preparer: sittingMail,
		revisions: deps.Store.ExamRevision(), classes: deps.Store.Class()}
	sittings, err := examsitting.New(
		deps.Store.ExamSitting(), deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(),
		examSittingAuthorizationAdapter{authorization: access.authorization},
		examSittingAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		examSittingSystemAuditAdapter{audit: foundation.audit},
		examSittingRealtimeEffects{realtime: foundation.realtime, execution: execution},
		examSittingRealtimeEffects{realtime: foundation.realtime},
		appjobs.NewExamSittingLifecycleJobFactory(time.Now, model.NewJobID),
		sittingMailPreparation,
		time.Now, model.NewExamSittingID,
	)
	if err != nil {
		return examinationConstruction{}, err
	}
	attemptEffects := examAttemptRealtimeEffects{realtime: foundation.realtime, execution: execution}
	submissionMail := examSubmissionMailPreparationAdapter{preparer: foundation.mail, users: deps.Store.User(),
		sittings: deps.Store.ExamSitting(), revisions: deps.Store.ExamRevision()}
	attempts, err := examattempt.New(examattempt.Dependencies{
		Persistence: deps.Store.ExamAttempt(), Workspace: deps.Store.ExamAttemptWorkspace(),
		Submissions: deps.Store.ExamSubmission(), Sittings: deps.Store.ExamSitting(),
		Managers:      examAttemptManagerAuthorizationAdapter{sittings: sittings, submissions: deps.Store.ExamSubmission()},
		Auditor:       examAttemptAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		SystemAuditor: examAttemptSystemAuditAdapter{audit: foundation.audit},
		Effects:       attemptEffects, EffectFailures: attemptEffects, Content: deps.FileContent, Mail: submissionMail,
		Now: time.Now, NewAttemptID: model.NewExamAttemptID, NewWorkspaceID: model.NewExamAttemptWorkspaceID,
		NewParticipation: model.NewAttemptParticipationID, NewConnection: model.NewAttemptConnectionID,
		NewEvidence: model.NewIntegrityEvidenceID, NewFlag: model.NewIntegrityFlagID,
		NewSuspension: model.NewAttemptSuspensionID, NewFocusLossSignal: model.NewFocusLossSignalID,
		NewDiscrepancy:    model.NewIntegrityDiscrepancyID,
		NewWorkspaceEntry: model.NewAttemptWorkspaceEntryID, NewWorkspaceObject: model.NewAttemptWorkspaceObjectID,
		NewWorkspaceVersion: model.NewWorkspaceContentVersion,
		NewSubmission:       model.NewSubmissionID,
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	attemptTerminals, err := newExamAttemptTerminalService(attempts, execution,
		examAttemptTerminalAuditAdapter{audit: foundation.audit})
	if err != nil {
		return examinationConstruction{}, err
	}
	reviewEffects := examIntegrityReviewRealtimeEffects{realtime: foundation.realtime}
	resultReleaseMail := examResultReleaseMailPreparationAdapter{preparer: foundation.mail, users: deps.Store.User(),
		sittings: deps.Store.ExamSitting(), revisions: deps.Store.ExamRevision()}
	reviews, err := examreview.New(examreview.Dependencies{Persistence: deps.Store.ExamIntegrityReview(),
		Authorizer: examIntegrityReviewAuthorizationAdapter{reviews: deps.Store.ExamIntegrityReview(), sittings: sittings},
		Auditor:    examIntegrityReviewAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		Effects:    reviewEffects, EffectFailures: reviewEffects, Mail: resultReleaseMail, Now: time.Now,
		NewReviewID: model.NewSubmissionReviewID, NewDecisionID: model.NewIntegrityReviewDecisionID})
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
	corrections, err := examcorrection.New(
		deps.Store.ExamCorrection(), deps.Store.ExamRevision(), deps.Store.ExamAuthoring(), deps.Store.AcademicUnitMember(),
		examCorrectionAuthorizationAdapter{authorization: access.authorization},
		examCorrectionAuditAdapter{audit: mutationAuditAdapter{audit: foundation.audit}},
		examCorrectionRealtimeEffects{realtime: foundation.realtime},
		examCorrectionRealtimeEffects{realtime: foundation.realtime},
		deps.FileContent, time.Now, model.NewExamCorrectionResourceStageID, model.NewExamResourceID,
		model.NewFileEntryID, model.NewFileRevisionID, model.NewUploadLeaseID, model.NewFileRenditionID,
		model.NewExamRevisionID,
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
	return examinationConstruction{execution: execution, authoring: authoring, revisions: revisions, sittings: sittings, sittingMail: sittingMail,
		sittingMailPreparation: sittingMailPreparation, attempts: attempts, attemptTerminals: attemptTerminals, reviews: reviews,
		resources: resources, corrections: corrections, starterWorkspace: starterWorkspace}, nil
}

type examAttemptSystemAuditAdapter struct{ audit *auditService }

func (adapter examAttemptSystemAuditAdapter) Begin(ctx context.Context, action model.Action, resource model.Resource,
	scopeType model.RoleScopeType, scopeID, operation string, value map[string]any,
) (string, error) {
	auditValue := make(map[string]any, len(value))
	for key, item := range value {
		auditValue[key] = item
	}
	event, err := adapter.audit.BeginSystemCriticalActionAtScope(ctx, action, resource, scopeType, scopeID,
		map[string]any{"operation": operation, "value": auditValue})
	if err != nil {
		return "", err
	}
	return event.ID.String(), nil
}

func (adapter examAttemptSystemAuditAdapter) Fail(ctx context.Context, id, code string) error {
	_, err := adapter.audit.CompleteCriticalAction(ctx, id, model.AuditStatusFail, code, nil)
	return err
}
