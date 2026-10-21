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
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamView = examengine.View

// CreateExamCommand creates an Exam with its initial Exam Draft in an Academic
// Unit; IdempotencyKey makes it replayable.
type CreateExamCommand = examengine.CreateCommand

// GetExamQuery names one Exam.
type GetExamQuery = examengine.GetQuery

// EditExamDraftTextCommand edits the title and/or Exam Instructions of an Exam
// Draft; nil fields are left unchanged. It is fenced by ExpectedDraftRevision
// and replayed by IdempotencyKey.
type EditExamDraftTextCommand = examengine.EditDraftTextCommand

// ConfigureExamDraftNativePolicyCommand replaces the native security policy of
// an Exam Draft, fenced by ExpectedDraftRevision and replayed by
// IdempotencyKey.
type ConfigureExamDraftNativePolicyCommand = examengine.ConfigureDraftNativePolicyCommand

// ConfigureExamDraftFocusLossCommand replaces the Focus Loss policy of an Exam
// Draft: the minimum qualifying duration, incident count and window, and the
// outcome when crossed. It is fenced by ExpectedDraftRevision and replayed by
// IdempotencyKey.
type ConfigureExamDraftFocusLossCommand = examengine.ConfigureDraftFocusLossCommand

// ConfigureExamDraftBrowserPolicyCommand replaces the Browser Policy of an
// Exam Draft, fenced by ExpectedDraftRevision and replayed by IdempotencyKey.
type ConfigureExamDraftBrowserPolicyCommand = examengine.ConfigureDraftBrowserPolicyCommand

type examAuthorizationAdapter struct{ authorization *accessControlService }

func (a examAuthorizationAdapter) Authorize(ctx context.Context, invocation Invocation, action model.Action, resource model.Resource) error {
	return a.authorization.Authorize(ctx, invocation, action, resource)
}

func (a examAuthorizationAdapter) AuthorizeList(ctx context.Context, invocation Invocation, _ model.AcademicUnitID) (store.ExamListVisibility, error) {
	principal := invocation.Principal()
	decisionAt := model.TimeUTC(a.authorization.now())
	ordinary, err := a.authorization.authorizedScopesAt(ctx, principal, model.ActionExamView, model.ResourceExam, decisionAt)
	if err != nil {
		return store.ExamListVisibility{}, err
	}
	override, err := a.authorization.authorizedScopesAt(ctx, principal, model.ActionExamViewOverride, model.ResourceExam, decisionAt)
	if err != nil {
		return store.ExamListVisibility{}, err
	}
	hasOrdinary := ordinary.InstitutionWide || len(ordinary.AcademicUnitRootIDs) > 0
	hasOverride := override.InstitutionWide || len(override.AcademicUnitRootIDs) > 0
	institution, err := a.authorization.resolver.institutions.GetSingleton(ctx)
	if err != nil {
		return store.ExamListVisibility{}, authorizationResourceError("institution", err)
	}
	action := model.ActionExamView
	if hasOverride {
		action = model.ActionExamViewOverride
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := a.authorization.audit.RecordAuthorizationDecision(ctx, principal, action, resource, model.RoleScopeInstitution, institution.ID.String(), invocation.RequestMetadata(), hasOrdinary || hasOverride); err != nil {
		return store.ExamListVisibility{}, err
	}
	if !hasOrdinary && !hasOverride {
		return store.ExamListVisibility{}, authorizationDeniedError()
	}
	return store.ExamListVisibility{ActorUserID: principal.UserID,
		OrdinaryMembershipAt:    decisionAt,
		OrdinaryInstitutionWide: ordinary.InstitutionWide, OrdinaryAcademicUnitRootIDs: append([]model.AcademicUnitID(nil), ordinary.AcademicUnitRootIDs...),
		OverrideInstitutionWide: override.InstitutionWide, OverrideAcademicUnitRootIDs: append([]model.AcademicUnitID(nil), override.AcademicUnitRootIDs...)}, nil
}

type examRealtimeEffects struct {
	realtime    *realtimeService
	collections examCollectionInvalidationEffects
}

func (e examRealtimeEffects) Created(ctx context.Context, examID model.ExamID) error {
	event, err := apprealtime.NewExamCreatedEvent(examID)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examRealtimeEffects) DraftUpdated(ctx context.Context, examID model.ExamID, revision int64) error {
	event, err := apprealtime.NewExamDraftUpdatedEvent(examID, revision)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examRealtimeEffects) Archived(ctx context.Context, examID model.ExamID, revision int64, archivedAt time.Time) error {
	event, err := apprealtime.NewExamArchivedEvent(examID, revision, archivedAt)
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, event), e.collections.ExamArchived(ctx, examID))
}
func (e examRealtimeEffects) ManagerChanged(ctx context.Context, examID model.ExamID, userID model.UserID, present bool, revision int64, changedAt time.Time) error {
	event, err := apprealtime.NewExamManagerChangedEvent(examID, userID, present, revision, changedAt)
	if err != nil {
		return err
	}
	e.realtime.InvalidateAuthorization(ctx, userID.String())
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examRealtimeEffects) OwnerTransferred(ctx context.Context, examID model.ExamID, ownerID model.UserID, revision int64, changedAt time.Time) error {
	event, err := apprealtime.NewExamOwnerTransferredEvent(examID, ownerID, revision, changedAt)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examengine.Authorizer = examAuthorizationAdapter{}
var _ examengine.Auditor = mutationAuditAdapter{}
var _ examengine.Effects = examRealtimeEffects{}
var _ examengine.EffectFailures = examRealtimeEffects{}
