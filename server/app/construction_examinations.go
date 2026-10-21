// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
)

func constructExaminations(deps Dependencies, foundation applicationFoundation, access accessAcademicConstruction) (examinationConstruction, error) {
	collectionEffects := examCollectionInvalidationEffects{
		sittings: deps.Store.ExamSitting(),
		realtime: foundation.realtime,
	}
	effects := examRealtimeEffects{realtime: foundation.realtime, collections: collectionEffects}
	authoring, err := examengine.NewAuthoring(examengine.AuthoringDependencies{
		Persistence:       deps.Store.ExamAuthoring(),
		Memberships:       deps.Store.AcademicUnitMember(),
		Users:             deps.Store.User(),
		Mail:              foundation.mail,
		Authorizer:        examAuthorizationAdapter{authorization: access.authorization},
		Auditor:           mutationAuditAdapter{audit: foundation.audit},
		Outcomes:          deps.Store.CommandOutcome(),
		Effects:           effects,
		EffectFailures:    effects,
		Now:               time.Now,
		NewID:             model.NewExamID,
		InstitutionOrigin: deps.PublicURL,
		Operational:       operationalRecorderAdapter{recorder: deps.OperationalRecorder},
	})
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
	revisions, err := examengine.NewPublication(examengine.PublicationDependencies{
		Persistence:       deps.Store.ExamRevision(),
		Access:            deps.Store.ExamAuthoring(),
		Memberships:       deps.Store.AcademicUnitMember(),
		Authorizer:        examAuthorizationAdapter{authorization: access.authorization},
		Auditor:           mutationAuditAdapter{audit: foundation.audit},
		Effects:           effects,
		EffectFailures:    effects,
		Now:               time.Now,
		NewID:             model.NewExamRevisionID,
		InstitutionOrigin: deps.PublicURL,
		Operational:       operationalRecorderAdapter{recorder: deps.OperationalRecorder},
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	sittingMailPreparation := sittingScheduleMailPreparationAdapter{preparer: sittingMail,
		revisions: deps.Store.ExamRevision(), classes: deps.Store.Class()}
	sittings, err := examsitting.New(examsitting.Dependencies{
		Persistence:    deps.Store.ExamSitting(),
		Access:         deps.Store.ExamAuthoring(),
		Memberships:    deps.Store.AcademicUnitMember(),
		Authorizer:     access.authorization,
		Auditor:        mutationAuditAdapter{audit: foundation.audit},
		SystemAuditor:  examSittingSystemAuditAdapter{audit: foundation.audit},
		Effects:        examSittingRealtimeEffects{realtime: foundation.realtime, collections: collectionEffects},
		EffectFailures: examSittingRealtimeEffects{realtime: foundation.realtime, collections: collectionEffects},
		Jobs:           appjobs.NewExamSittingLifecycleJobFactory(time.Now, model.NewJobID),
		Mail:           sittingMailPreparation,
		Now:            time.Now,
		NewID:          model.NewExamSittingID,
		Operational:    operationalRecorderAdapter{recorder: deps.OperationalRecorder},
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	attemptEffects := examAttemptRealtimeEffects{realtime: foundation.realtime}
	submissionMail := examSubmissionMailPreparationAdapter{preparer: foundation.mail, users: deps.Store.User(),
		sittings: deps.Store.ExamSitting(), revisions: deps.Store.ExamRevision()}
	attempts, err := examattempt.New(examattempt.Dependencies{
		Persistence: deps.Store.ExamAttempt(), Workspace: deps.Store.ExamAttemptWorkspace(),
		Submissions: deps.Store.ExamSubmission(), Sittings: deps.Store.ExamSitting(),
		Managers:      examAttemptManagerAuthorizationAdapter{sittings: sittings, submissions: deps.Store.ExamSubmission(), audit: foundation.audit},
		Auditor:       mutationAuditAdapter{audit: foundation.audit},
		SystemAuditor: examAttemptSystemAuditAdapter{audit: foundation.audit},
		Effects:       attemptEffects, EffectFailures: attemptEffects, Content: deps.FileContent, Mail: submissionMail,
		DesktopBuilds: access.desktopCompatibility,
		Now:           time.Now, NewAttemptID: model.NewExamAttemptID, NewWorkspaceID: model.NewExamAttemptWorkspaceID,
		NewParticipation: model.NewAttemptParticipationID, NewConnection: model.NewAttemptConnectionID,
		NewEvidence: model.NewIntegrityEvidenceID, NewFlag: model.NewIntegrityFlagID,
		NewSuspension: model.NewAttemptSuspensionID, NewFocusLossSignal: model.NewFocusLossSignalID,
		NewDiscrepancy:    model.NewIntegrityDiscrepancyID,
		NewWorkspaceEntry: model.NewAttemptWorkspaceEntryID, NewWorkspaceObject: model.NewAttemptWorkspaceObjectID,
		NewWorkspaceVersion: model.NewWorkspaceContentVersion,
		NewSubmission:       model.NewSubmissionID,
		Operational:         operationalRecorderAdapter{recorder: deps.OperationalRecorder},
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	reviewEffects := examIntegrityReviewRealtimeEffects{realtime: foundation.realtime}
	resultReleaseMail := examResultReleaseMailPreparationAdapter{preparer: foundation.mail, users: deps.Store.User(),
		sittings: deps.Store.ExamSitting(), revisions: deps.Store.ExamRevision()}
	reviews, err := examreview.New(examreview.Dependencies{Persistence: deps.Store.ExamIntegrityReview(),
		Authorizer: examIntegrityReviewAuthorizationAdapter{reviews: deps.Store.ExamIntegrityReview(), sittings: sittings,
			audit: foundation.audit},
		Auditor: mutationAuditAdapter{audit: foundation.audit},
		Effects: reviewEffects, EffectFailures: reviewEffects, Mail: resultReleaseMail, Now: time.Now,
		NewReviewID: model.NewSubmissionReviewID, NewDecisionID: model.NewIntegrityReviewDecisionID})
	if err != nil {
		return examinationConstruction{}, err
	}
	resources, err := examresource.New(examresource.Dependencies{
		Persistence:    deps.Store.ExamResource(),
		Access:         deps.Store.ExamAuthoring(),
		Memberships:    deps.Store.AcademicUnitMember(),
		Authorizer:     access.authorization,
		Auditor:        mutationAuditAdapter{audit: foundation.audit},
		Effects:        examResourceRealtimeEffects{realtime: foundation.realtime},
		EffectFailures: examResourceRealtimeEffects{realtime: foundation.realtime},
		Content:        deps.FileContent,
		Now:            time.Now,
		NewResourceID:  model.NewExamResourceID,
		NewEntryID:     model.NewFileEntryID,
		NewRevisionID:  model.NewFileRevisionID,
		NewLeaseID:     model.NewUploadLeaseID,
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	corrections, err := examcorrection.New(examcorrection.Dependencies{
		Persistence:       deps.Store.ExamCorrection(),
		Revisions:         deps.Store.ExamRevision(),
		Access:            deps.Store.ExamAuthoring(),
		Memberships:       deps.Store.AcademicUnitMember(),
		Authorizer:        access.authorization,
		Auditor:           mutationAuditAdapter{audit: foundation.audit},
		Effects:           examCorrectionRealtimeEffects{realtime: foundation.realtime, collections: collectionEffects},
		EffectFailures:    examCorrectionRealtimeEffects{realtime: foundation.realtime, collections: collectionEffects},
		Content:           deps.FileContent,
		Now:               time.Now,
		NewStageID:        model.NewExamCorrectionResourceStageID,
		NewResourceID:     model.NewExamResourceID,
		NewEntryID:        model.NewFileEntryID,
		NewFileRevisionID: model.NewFileRevisionID,
		NewLeaseID:        model.NewUploadLeaseID,
		NewRenditionID:    model.NewFileRenditionID,
		NewExamRevisionID: model.NewExamRevisionID,
		InstitutionOrigin: deps.PublicURL,
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	starterWorkspace, err := examworkspace.New(examworkspace.Dependencies{
		Persistence:    deps.Store.ExamStarterWorkspace(),
		Access:         deps.Store.ExamAuthoring(),
		Memberships:    deps.Store.AcademicUnitMember(),
		Authorizer:     access.authorization,
		Auditor:        mutationAuditAdapter{audit: foundation.audit},
		Content:        deps.FileContent,
		Effects:        examStarterWorkspaceRealtimeEffects{realtime: foundation.realtime},
		EffectFailures: examStarterWorkspaceRealtimeEffects{realtime: foundation.realtime},
		Now:            time.Now,
		NewEntryID:     model.NewStarterWorkspaceEntryID,
		NewObjectID:    model.NewStarterWorkspaceObjectID,
		NewVersion:     model.NewWorkspaceContentVersion,
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	records, err := examengine.NewRecords(examengine.RecordsDependencies{
		Persistence: deps.Store.ExamRecords(),
		Access:      deps.Store.ExamAuthoring(),
		Memberships: deps.Store.AcademicUnitMember(),
		Authorizer:  access.authorization,
		Auditor:     mutationAuditAdapter{audit: foundation.audit},
		RecentTTL:   deps.RecentAuthenticationTTL,
		Now:         time.Now,
		NewHoldID:   model.NewRetentionHoldID,
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	exports, err := examengine.NewExports(examengine.ExportsDependencies{
		Persistence: deps.Store.ExamExport(),
		Access:      deps.Store.ExamAuthoring(),
		Memberships: deps.Store.AcademicUnitMember(),
		Authorizer:  access.authorization,
		Auditor:     mutationAuditAdapter{audit: foundation.audit},
		Content:     deps.FileContent,
		Now:         time.Now,
		NewID:       model.NewExamExportID,
		NewJobID:    model.NewJobID,
	})
	if err != nil {
		return examinationConstruction{}, err
	}
	return examinationConstruction{authoring: authoring, revisions: revisions, sittings: sittings, sittingMail: sittingMail,
		sittingMailPreparation: sittingMailPreparation, attempts: attempts, reviews: reviews,
		records: records, exports: exports, resources: resources, corrections: corrections, starterWorkspace: starterWorkspace}, nil
}

type examAttemptSystemAuditAdapter struct{ audit *auditService }

func (a examAttemptSystemAuditAdapter) Begin(ctx context.Context, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value map[string]any) (model.AuditEventID, error) {
	auditValue := make(map[string]any, len(value))
	for key, item := range value {
		auditValue[key] = item
	}
	event, err := a.audit.BeginSystemCriticalActionAtScope(ctx, action, resource, scopeType, scopeID,
		map[string]any{"operation": operation, "value": auditValue})
	if err != nil {
		return "", err
	}
	return event.ID, nil
}

func (a examAttemptSystemAuditAdapter) Fail(ctx context.Context, id model.AuditEventID, code string) error {
	_, err := a.audit.CompleteCriticalAction(ctx, id, model.AuditStatusFail, code, nil)
	return err
}
