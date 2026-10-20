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
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	examengine "github.com/sudosylabs/proctor/server/app/exam"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamView = examengine.View

type CreateExamCommand = examengine.CreateCommand

type GetExamQuery struct{ ExamID model.ExamID }

type EditExamDraftTextCommand = examengine.EditDraftTextCommand

type ConfigureExamDraftNativePolicyCommand = examengine.ConfigureDraftNativePolicyCommand

type ConfigureExamDraftFocusLossCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Enabled               bool
	MinimumDuration       time.Duration
	IncidentCount         int
	Window                time.Duration
	Outcome               model.IntegrityThresholdOutcome
	IdempotencyKey        string
}

type ConfigureExamDraftBrowserPolicyCommand = examengine.ConfigureDraftBrowserPolicyCommand

type examUseCases interface {
	Create(context.Context, Invocation, examengine.CreateCommand) (examengine.View, error)
	Get(context.Context, Invocation, model.ExamID) (examengine.View, error)
	EditDraftText(context.Context, Invocation, examengine.EditDraftTextCommand) (examengine.View, error)
	ConfigureDraftFocusLoss(context.Context, Invocation, examengine.ConfigureDraftFocusLossCommand) (examengine.View, error)
	ConfigureDraftNativePolicy(context.Context, Invocation, examengine.ConfigureDraftNativePolicyCommand) (examengine.View, error)
	ConfigureDraftBrowserPolicy(context.Context, Invocation, examengine.ConfigureDraftBrowserPolicyCommand) (examengine.View, error)
	List(context.Context, Invocation, examengine.ListQuery) (examengine.CatalogPage, error)
	Archive(context.Context, Invocation, examengine.ArchiveCommand) (model.Exam, error)
	ListManagers(context.Context, Invocation, examengine.ListManagersQuery) (examengine.ManagerPage, error)
	AddManager(context.Context, Invocation, examengine.AddManagerCommand) (examengine.ManagerChange, error)
	RemoveManager(context.Context, Invocation, examengine.RemoveManagerCommand) (examengine.ManagerChange, error)
	TransferOwner(context.Context, Invocation, examengine.TransferOwnerCommand) (examengine.ManagerChange, error)
	AuthorizeView(context.Context, Invocation, model.ExamID) error
}

func (a *App) CreateExam(ctx context.Context, invocation Invocation, command CreateExamCommand) (result ExamView, resultErr error) {
	defer func() { a.recordOperational("exam", "create", resultErr) }()
	view, err := a.exams.Create(ctx, invocation, command)
	if err != nil {
		return ExamView{}, examError(err, false)
	}
	return view, nil
}

func (a *App) GetExam(ctx context.Context, invocation Invocation, query GetExamQuery) (ExamView, error) {
	view, err := a.exams.Get(ctx, invocation, query.ExamID)
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func (a *App) EditExamDraftText(ctx context.Context, invocation Invocation, command EditExamDraftTextCommand) (ExamView, error) {
	view, err := a.exams.EditDraftText(ctx, invocation, command)
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func (a *App) ConfigureExamDraftFocusLoss(ctx context.Context, invocation Invocation, command ConfigureExamDraftFocusLossCommand) (ExamView, error) {
	view, err := a.exams.ConfigureDraftFocusLoss(ctx, invocation, examengine.ConfigureDraftFocusLossCommand{
		ExamID: command.ExamID, ExpectedDraftRevision: command.ExpectedDraftRevision,
		FocusLoss:      model.FocusLossPolicy{Enabled: command.Enabled, MinimumDuration: command.MinimumDuration, IncidentCount: command.IncidentCount, Window: command.Window, Outcome: command.Outcome},
		IdempotencyKey: command.IdempotencyKey,
	})
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func (a *App) ConfigureExamDraftBrowserPolicy(ctx context.Context, invocation Invocation, command ConfigureExamDraftBrowserPolicyCommand) (ExamView, error) {
	command.Policy = command.Policy.Clone()
	view, err := a.exams.ConfigureDraftBrowserPolicy(ctx, invocation, command)
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func examError(err error, conceal bool) error {
	return presentExamChildError(err, conceal, "exam.unavailable", "exam.not_found")
}

// presentExamChildError presents a failure returned by an Exam child module.
// Application errors pass through unchanged; any other failure becomes the
// family's unavailable code. Concealing callers turn authorization denials and
// the family's listed absence codes into the generic resource.not_found so a
// caller without access cannot distinguish a hidden resource from a missing one.
func presentExamChildError(err error, conceal bool, unavailable apperror.Code, concealed ...apperror.Code) error {
	if err == nil {
		return nil
	}
	failure, ok := apperror.As(err)
	if !ok {
		return apperror.New(unavailable).Wrap(err)
	}
	if conceal && (failure.Code() == "authorization.denied" || slices.ContainsFunc(concealed, func(code apperror.Code) bool { return string(code) == failure.Code() })) {
		return apperror.New("resource.not_found").Wrap(err)
	}
	return err
}

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
	return e.realtime.Publish(ctx, event)
}
func (e examRealtimeEffects) DraftUpdated(ctx context.Context, examID model.ExamID, revision int64) error {
	event, err := apprealtime.NewExamDraftUpdatedEvent(examID, revision)
	if err != nil {
		return err
	}
	return e.realtime.Publish(ctx, event)
}
func (e examRealtimeEffects) Archived(ctx context.Context, examID model.ExamID, revision int64, archivedAt time.Time) error {
	event, err := apprealtime.NewExamArchivedEvent(examID, revision, archivedAt)
	if err != nil {
		return err
	}
	return errors.Join(e.realtime.Publish(ctx, event), e.collections.ExamArchived(ctx, examID))
}
func (e examRealtimeEffects) ManagerChanged(ctx context.Context, examID model.ExamID, userID model.UserID, present bool, revision int64, changedAt time.Time) error {
	event, err := apprealtime.NewExamManagerChangedEvent(examID, userID, present, revision, changedAt)
	if err != nil {
		return err
	}
	e.realtime.InvalidateAuthorization(ctx, userID.String())
	return e.realtime.Publish(ctx, event)
}
func (e examRealtimeEffects) OwnerTransferred(ctx context.Context, examID model.ExamID, ownerID model.UserID, revision int64, changedAt time.Time) error {
	event, err := apprealtime.NewExamOwnerTransferredEvent(examID, ownerID, revision, changedAt)
	if err != nil {
		return err
	}
	return e.realtime.Publish(ctx, event)
}
func (e examRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examengine.Authorizer = examAuthorizationAdapter{}
var _ examengine.Auditor = mutationAuditAdapter{}
var _ examengine.Effects = examRealtimeEffects{}
var _ examengine.EffectFailures = examRealtimeEffects{}

func (a *App) ConfigureExamDraftNativePolicy(ctx context.Context, invocation Invocation, command ConfigureExamDraftNativePolicyCommand) (ExamView, error) {
	command.NativePolicy = command.NativePolicy.Clone()
	view, err := a.exams.ConfigureDraftNativePolicy(ctx, invocation, command)
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}
