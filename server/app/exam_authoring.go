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

	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type examAuthorizationAdapter struct{ authorization *accessControlService }

func (a examAuthorizationAdapter) Authorize(ctx context.Context, invocation invocation.Invocation, action model.Action, resource model.Resource) error {
	return a.authorization.Authorize(ctx, invocation, action, resource)
}

func (a examAuthorizationAdapter) AuthorizeList(ctx context.Context, invocation invocation.Invocation, _ model.AcademicUnitID) (store.ExamListVisibility, error) {
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
	institution, err := a.authorization.deps.Resolver.deps.Institutions.GetSingleton(ctx)
	if err != nil {
		return store.ExamListVisibility{}, authorizationResourceError("institution", err)
	}
	action := model.ActionExamView
	if hasOverride {
		action = model.ActionExamViewOverride
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err := a.authorization.deps.Audit.RecordAuthorizationDecision(ctx, principal, action, resource, model.RoleScopeInstitution, institution.ID.String(), invocation.RequestMetadata(), hasOrdinary || hasOverride); err != nil {
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
	event, err := realtime.NewExamCreatedEvent(examID)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examRealtimeEffects) DraftUpdated(ctx context.Context, examID model.ExamID, revision int64) error {
	event, err := realtime.NewExamDraftUpdatedEvent(examID, revision)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examRealtimeEffects) Archived(ctx context.Context, examID model.ExamID, revision int64, archivedAt time.Time) error {
	event, err := realtime.NewExamArchivedEvent(examID, revision, archivedAt)
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.PublishRealtimeEvent(ctx, event), e.collections.ExamArchived(ctx, examID))
}
func (e examRealtimeEffects) ManagerChanged(ctx context.Context, examID model.ExamID, userID model.UserID, present bool, revision int64, changedAt time.Time) error {
	event, err := realtime.NewExamManagerChangedEvent(examID, userID, present, revision, changedAt)
	if err != nil {
		return err
	}
	e.realtime.InvalidateAuthorization(ctx, userID)
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examRealtimeEffects) OwnerTransferred(ctx context.Context, examID model.ExamID, ownerID model.UserID, revision int64, changedAt time.Time) error {
	event, err := realtime.NewExamOwnerTransferredEvent(examID, ownerID, revision, changedAt)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ exam.Authorizer = examAuthorizationAdapter{}
var _ exam.Auditor = mutationAuditAdapter{}
var _ exam.Effects = examRealtimeEffects{}
var _ exam.EffectFailures = examRealtimeEffects{}
