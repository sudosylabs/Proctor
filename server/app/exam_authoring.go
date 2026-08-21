// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamView = examengine.View

type CreateExamCommand struct {
	AcademicUnitID       model.AcademicUnitID
	Title                string
	InstructionsMarkdown string
	IdempotencyKey       string
}

type GetExamQuery struct{ ExamID model.ExamID }

type EditExamDraftTextCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Title                 *string
	InstructionsMarkdown  *string
	IdempotencyKey        string
}

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

type ConfigureExamDraftExecutionProfileCommand struct {
	ExamID                model.ExamID
	ExpectedDraftRevision int64
	Enabled               bool
	Image                 string
	Network               model.ExecutionNetwork
	IdempotencyKey        string
}

type ExamExecutionImage struct {
	ID       string
	Networks []model.ExecutionNetwork
}

type examUseCases interface {
	Create(context.Context, examengine.Call, examengine.CreateCommand) (examengine.View, error)
	Get(context.Context, examengine.Call, model.ExamID) (examengine.View, error)
	EditDraftText(context.Context, examengine.Call, examengine.EditDraftTextCommand) (examengine.View, error)
	ConfigureDraftFocusLoss(context.Context, examengine.Call, examengine.ConfigureDraftFocusLossCommand) (examengine.View, error)
	ConfigureDraftExecutionProfile(context.Context, examengine.Call, examengine.ConfigureDraftExecutionProfileCommand) (examengine.View, error)
	List(context.Context, examengine.Call, examengine.ListQuery) (examengine.CatalogPage, error)
	Archive(context.Context, examengine.Call, examengine.ArchiveCommand) (model.Exam, error)
	ListManagers(context.Context, examengine.Call, examengine.ListManagersQuery) (examengine.ManagerPage, error)
	AddManager(context.Context, examengine.Call, examengine.AddManagerCommand) (examengine.ManagerChange, error)
	RemoveManager(context.Context, examengine.Call, examengine.RemoveManagerCommand) (examengine.ManagerChange, error)
	TransferOwner(context.Context, examengine.Call, examengine.TransferOwnerCommand) (examengine.ManagerChange, error)
	AuthorizeView(context.Context, examengine.Call, model.ExamID) error
}

func (a *App) CreateExam(ctx context.Context, invocation Invocation, command CreateExamCommand) (ExamView, error) {
	if command.IdempotencyKey == "" {
		return ExamView{}, NewError("idempotency.key_required")
	}
	idempotency, err := newCommandIdempotency(invocation, "exam.create.v1", command.IdempotencyKey, struct {
		AcademicUnitID       string `json:"academic_unit_id"`
		Title                string `json:"title"`
		InstructionsMarkdown string `json:"instructions_markdown"`
	}{command.AcademicUnitID.String(), command.Title, command.InstructionsMarkdown})
	if err != nil {
		return ExamView{}, err
	}
	view, err := a.exams.Create(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.CreateCommand{
		AcademicUnitID: command.AcademicUnitID, Title: command.Title,
		InstructionsMarkdown: command.InstructionsMarkdown, Idempotency: idempotency,
	})
	if err != nil {
		return ExamView{}, examError(err, false)
	}
	return view, nil
}

func (a *App) GetExam(ctx context.Context, invocation Invocation, query GetExamQuery) (ExamView, error) {
	view, err := a.exams.Get(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), query.ExamID)
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func (a *App) EditExamDraftText(ctx context.Context, invocation Invocation, command EditExamDraftTextCommand) (ExamView, error) {
	if command.IdempotencyKey == "" {
		return ExamView{}, NewError("idempotency.key_required")
	}
	var normalizedTitle *string
	if command.Title != nil {
		value := strings.TrimSpace(*command.Title)
		normalizedTitle = &value
	}
	idempotency, err := newCommandIdempotency(invocation, "exam.draft.text.edit.v1", command.IdempotencyKey, struct {
		ExamID                string  `json:"exam_id"`
		ExpectedDraftRevision int64   `json:"expected_draft_revision"`
		Title                 *string `json:"title"`
		InstructionsMarkdown  *string `json:"instructions_markdown"`
	}{command.ExamID.String(), command.ExpectedDraftRevision, normalizedTitle, command.InstructionsMarkdown})
	if err != nil {
		return ExamView{}, err
	}
	view, err := a.exams.EditDraftText(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.EditDraftTextCommand{
		ExamID: command.ExamID, ExpectedDraftRevision: command.ExpectedDraftRevision,
		Title: normalizedTitle, InstructionsMarkdown: command.InstructionsMarkdown, Idempotency: idempotency,
	})
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func (a *App) ConfigureExamDraftFocusLoss(ctx context.Context, invocation Invocation, command ConfigureExamDraftFocusLossCommand) (ExamView, error) {
	if command.IdempotencyKey == "" {
		return ExamView{}, NewError("idempotency.key_required")
	}
	minimumDuration := time.Duration(command.MinimumDuration.Milliseconds()) * time.Millisecond
	window := time.Duration(command.Window.Milliseconds()) * time.Millisecond
	idempotency, err := newCommandIdempotency(invocation, "exam.draft.focus_loss.configure.v1", command.IdempotencyKey, struct {
		ExamID                      string                          `json:"exam_id"`
		ExpectedDraftRevision       int64                           `json:"expected_draft_revision"`
		Enabled                     bool                            `json:"enabled"`
		MinimumDurationMilliseconds int64                           `json:"minimum_duration_milliseconds"`
		IncidentCount               int                             `json:"incident_count"`
		WindowMilliseconds          int64                           `json:"window_milliseconds"`
		Outcome                     model.IntegrityThresholdOutcome `json:"outcome"`
	}{
		ExamID: command.ExamID.String(), ExpectedDraftRevision: command.ExpectedDraftRevision,
		Enabled: command.Enabled, MinimumDurationMilliseconds: minimumDuration.Milliseconds(),
		IncidentCount: command.IncidentCount, WindowMilliseconds: window.Milliseconds(), Outcome: command.Outcome,
	})
	if err != nil {
		return ExamView{}, err
	}
	view, err := a.exams.ConfigureDraftFocusLoss(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.ConfigureDraftFocusLossCommand{
		ExamID: command.ExamID, ExpectedDraftRevision: command.ExpectedDraftRevision,
		FocusLoss:   model.FocusLossPolicy{Enabled: command.Enabled, MinimumDuration: minimumDuration, IncidentCount: command.IncidentCount, Window: window, Outcome: command.Outcome},
		Idempotency: idempotency,
	})
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func (a *App) ConfigureExamDraftExecutionProfile(ctx context.Context, invocation Invocation, command ConfigureExamDraftExecutionProfileCommand) (ExamView, error) {
	if command.IdempotencyKey == "" {
		return ExamView{}, NewError("idempotency.key_required")
	}
	profile := model.ExecutionProfile{Enabled: command.Enabled, Image: strings.TrimSpace(command.Image), Network: command.Network}
	if err := profile.Validate(); err != nil {
		return ExamView{}, NewError("exam.invalid").Wrap(err)
	}
	idempotency, err := newCommandIdempotency(invocation, "exam.draft.execution_profile.configure.v1", command.IdempotencyKey, struct {
		ExamID                string                 `json:"exam_id"`
		ExpectedDraftRevision int64                  `json:"expected_draft_revision"`
		Profile               model.ExecutionProfile `json:"profile"`
	}{command.ExamID.String(), command.ExpectedDraftRevision, profile})
	if err != nil {
		return ExamView{}, err
	}
	view, err := a.exams.ConfigureDraftExecutionProfile(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), examengine.ConfigureDraftExecutionProfileCommand{
		ExamID: command.ExamID, ExpectedDraftRevision: command.ExpectedDraftRevision, Profile: profile, Idempotency: idempotency,
	})
	if err != nil {
		return ExamView{}, examError(err, true)
	}
	return view, nil
}

func (a *App) ListExamExecutionImages(ctx context.Context, invocation Invocation, query GetExamQuery) ([]ExamExecutionImage, error) {
	if a.execution == nil {
		return nil, NewError("exam.unavailable")
	}
	if _, err := a.exams.Get(ctx, examengine.NewCall(invocation.Principal(), invocation.RequestMetadata()), query.ExamID); err != nil {
		return nil, examError(err, true)
	}
	images, err := a.execution.Images(ctx)
	if err != nil {
		return nil, NewError("exam.unavailable").Wrap(err)
	}
	result := make([]ExamExecutionImage, len(images))
	for index, image := range images {
		result[index].ID = image.ID
		result[index].Networks = make([]model.ExecutionNetwork, len(image.Networks))
		for networkIndex, network := range image.Networks {
			result[index].Networks[networkIndex] = model.ExecutionNetwork(network)
		}
	}
	return result, nil
}

func executionCatalogSupports(images []appexecution.ImageOption, profile model.ExecutionProfile) bool {
	for _, image := range images {
		if image.ID != profile.Image {
			continue
		}
		for _, network := range image.Networks {
			if model.ExecutionNetwork(network) == profile.Network {
				return true
			}
		}
	}
	return false
}

type examExecutionProfileCatalog struct{ execution executionUseCases }

func (catalog examExecutionProfileCatalog) Supports(ctx context.Context, profile model.ExecutionProfile) (bool, error) {
	if !profile.Enabled {
		return true, nil
	}
	if catalog.execution == nil {
		return false, errors.New("execution catalog is unavailable")
	}
	images, err := catalog.execution.Images(ctx)
	if err != nil {
		return false, err
	}
	return executionCatalogSupports(images, profile), nil
}

func examError(err error, conceal bool) error {
	if err == nil {
		return nil
	}
	if existing, ok := As(err); ok {
		if conceal && existing.Code() == "authorization.denied" {
			return NewError("resource.not_found").Wrap(err)
		}
		return err
	}
	var fault *examengine.Fault
	if !errors.As(err, &fault) {
		return NewError("exam.unavailable").Wrap(err)
	}
	if conceal && fault.Code == "exam.not_found" {
		return NewError("resource.not_found").Wrap(err)
	}
	mapped := NewError(fault.Code)
	for key, value := range fault.SafeFields {
		mapped.WithField(key, fmt.Sprint(value))
	}
	return mapped.Wrap(err)
}

type examAuthorizationAdapter struct{ authorization *accessControlService }

func (a examAuthorizationAdapter) Authorize(ctx context.Context, call examengine.Call, action model.Action, resource model.Resource) error {
	return a.authorization.authorizeCurrentState(ctx, call.Principal(), action, resource, call.RequestMetadata())
}

func (a examAuthorizationAdapter) AuthorizeList(ctx context.Context, call examengine.Call, _ model.AcademicUnitID) (store.ExamListVisibility, error) {
	principal := call.Principal()
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
	if err := a.authorization.audit.RecordAuthorizationDecision(ctx, principal, action, resource, model.RoleScopeInstitution, institution.ID.String(), call.RequestMetadata(), hasOrdinary || hasOverride); err != nil {
		return store.ExamListVisibility{}, err
	}
	if !hasOrdinary && !hasOverride {
		return store.ExamListVisibility{}, authorizationDeniedError("examAuthorizationAdapter.AuthorizeList")
	}
	return store.ExamListVisibility{ActorUserID: principal.UserID,
		OrdinaryMembershipAt:    decisionAt,
		OrdinaryInstitutionWide: ordinary.InstitutionWide, OrdinaryAcademicUnitRootIDs: append([]string(nil), ordinary.AcademicUnitRootIDs...),
		OverrideInstitutionWide: override.InstitutionWide, OverrideAcademicUnitRootIDs: append([]string(nil), override.AcademicUnitRootIDs...)}, nil
}

type examAuditAdapter struct{ audit mutationAuditAdapter }

func (a examAuditAdapter) Begin(ctx context.Context, call examengine.Call, action model.Action, resource model.Resource, scopeType model.RoleScopeType, scopeID, operation string, value, prior map[string]any) (string, error) {
	return a.audit.BeginAtScope(ctx, NewInvocation(call.Principal(), call.RequestMetadata()), action, resource, scopeType, scopeID, operation, value, prior)
}
func (a examAuditAdapter) Fail(ctx context.Context, id, code string) error {
	return a.audit.Fail(ctx, id, code)
}

type examRealtimeEffects struct{ realtime *realtimeService }

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
	return e.realtime.Publish(ctx, event)
}
func (e examRealtimeEffects) ManagerChanged(ctx context.Context, examID model.ExamID, userID model.UserID, present bool, revision int64, changedAt time.Time) error {
	event, err := apprealtime.NewExamManagerChangedEvent(examID, userID, present, revision, changedAt)
	if err != nil {
		return err
	}
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
var _ examengine.Auditor = examAuditAdapter{}
var _ examengine.Effects = examRealtimeEffects{}
var _ examengine.EffectFailures = examRealtimeEffects{}
