// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const onboardingImportRetention = 7 * 24 * time.Hour
const MaximumOnboardingImportBytes = store.OnboardingImportMaximumBytes

type UploadOnboardingImportCommand struct {
	Mode      model.OnboardingImportMode
	ScopeType model.RoleScopeType
	ScopeID   string
	RoleID    string
	Body      io.Reader
}

type CommitOnboardingImportCommand struct {
	ID               string
	ExpectedRevision int64
	PreviewDigest    string
	Policy           model.OnboardingImportCommitPolicy
	IdempotencyKey   string
}

type OnboardingImportView struct {
	ID             model.OnboardingImportID
	Mode           model.OnboardingImportMode
	State          model.OnboardingImportState
	ScopeType      model.RoleScopeType
	ScopeID        string
	RoleID         model.RoleID
	PreviewDigest  string
	IgnoredHeaders []string
	TotalRows      int
	ValidRows      int
	InvalidRows    int
	SucceededRows  int
	NoOpRows       int
	FailedRows     int
	SkippedRows    int
	CommitPolicy   model.OnboardingImportCommitPolicy
	ParseJobID     model.JobID
	ExecutionJobID model.JobID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      time.Time
	Revision       int64
	FailureCode    string
}

type OnboardingImportRowView struct {
	RowNumber    int
	Reference    string
	Operation    string
	Status       model.OnboardingImportRowStatus
	PreviewCode  string
	PublicCode   string
	InvitationID model.InvitationID
}

type onboardingImportService struct {
	imports        store.OnboardingImportStore
	content        OnboardingImportFiles
	invitations    *invitationService
	authorization  invitationAuthorizer
	audit          mutationAuditor
	authentication *authenticationService
	tokens         store.PersonalAccessTokenStore
	users          store.UserStore
	institutions   store.InstitutionStore
	units          store.AcademicUnitStore
	programmes     store.ProgrammeStore
	levels         store.ProgrammeLevelStore
	periods        store.AcademicPeriodStore
	classes        store.ClassStore
	roles          store.RoleStore
	now            func() time.Time
	wake           func()
}

func newOnboardingImportService(deps Dependencies, invitations *invitationService, authentication *authenticationService, authorization invitationAuthorizer, audit mutationAuditor) (*onboardingImportService, error) {
	if deps.Store == nil {
		return nil, errors.New("onboarding import store catalog is required")
	}
	if deps.Store.OnboardingImport() == nil {
		return nil, errors.New("onboarding import persistence is required")
	}
	if deps.FileContent == nil {
		return nil, errors.New("onboarding import file content is required")
	}
	if invitations == nil || authentication == nil || authorization == nil || audit == nil {
		return nil, errors.New("onboarding import application collaborators are required")
	}
	if deps.Store.PersonalAccessToken() == nil || deps.Store.User() == nil {
		return nil, errors.New("onboarding import credential stores are required")
	}
	if deps.Store.Institution() == nil || deps.Store.AcademicUnit() == nil || deps.Store.Programme() == nil || deps.Store.ProgrammeLevel() == nil ||
		deps.Store.AcademicPeriod() == nil || deps.Store.Class() == nil || deps.Store.Role() == nil {
		// Deliberately reduced lifecycle test catalogs omit whole capability
		// families. Production catalogs project every target store together.
		return nil, nil
	}
	return &onboardingImportService{imports: deps.Store.OnboardingImport(), content: deps.FileContent, invitations: invitations, authorization: authorization, audit: audit,
		authentication: authentication, tokens: deps.Store.PersonalAccessToken(), users: deps.Store.User(), institutions: deps.Store.Institution(), units: deps.Store.AcademicUnit(),
		programmes: deps.Store.Programme(), levels: deps.Store.ProgrammeLevel(), periods: deps.Store.AcademicPeriod(), classes: deps.Store.Class(), roles: deps.Store.Role(), now: time.Now}, nil
}

func (a *App) UploadOnboardingImport(ctx context.Context, invocation Invocation, command UploadOnboardingImportCommand) (OnboardingImportView, error) {
	if a == nil || a.onboardingImports == nil {
		return OnboardingImportView{}, NewError("onboarding_import.unavailable")
	}
	return a.onboardingImports.Upload(ctx, invocation, command)
}

func (a *App) GetOnboardingImport(ctx context.Context, invocation Invocation, id string) (OnboardingImportView, []OnboardingImportRowView, error) {
	if a == nil || a.onboardingImports == nil {
		return OnboardingImportView{}, nil, NewError("onboarding_import.unavailable")
	}
	return a.onboardingImports.Get(ctx, invocation, id)
}

func (a *App) CommitOnboardingImport(ctx context.Context, invocation Invocation, command CommitOnboardingImportCommand) (OnboardingImportView, error) {
	if a == nil || a.onboardingImports == nil {
		return OnboardingImportView{}, NewError("onboarding_import.unavailable")
	}
	return a.onboardingImports.Commit(ctx, invocation, command)
}

func (a *App) CancelOnboardingImport(ctx context.Context, invocation Invocation, id string) (OnboardingImportView, error) {
	if a == nil || a.onboardingImports == nil {
		return OnboardingImportView{}, NewError("onboarding_import.unavailable")
	}
	return a.onboardingImports.Cancel(ctx, invocation, id)
}

func (a *App) OnboardingImportReport(ctx context.Context, invocation Invocation, id string, output io.Writer) error {
	if a == nil || a.onboardingImports == nil {
		return NewError("onboarding_import.unavailable")
	}
	return a.onboardingImports.Report(ctx, invocation, id, output)
}

func (s *onboardingImportService) Upload(ctx context.Context, invocation Invocation, command UploadOnboardingImportCommand) (OnboardingImportView, error) {
	command.ScopeID = strings.TrimSpace(command.ScopeID)
	command.RoleID = strings.TrimSpace(command.RoleID)
	if command.Body == nil || model.ValidateOnboardingImportScope(command.Mode, command.ScopeType, command.ScopeID) != nil ||
		(command.Mode == model.OnboardingImportTeacherAcademicUnit && !model.IsValidId(command.RoleID)) ||
		(command.Mode != model.OnboardingImportTeacherAcademicUnit && command.RoleID != "") {
		return OnboardingImportView{}, NewError("request.invalid")
	}
	resource := onboardingImportResource(command.ScopeType, command.ScopeID)
	if err := s.authorization.Authorize(ctx, invocation, model.ActionOnboardingBatchManage, resource); err != nil {
		return OnboardingImportView{}, err
	}
	if err := s.validatePrincipal(ctx, invocation.Principal()); err != nil {
		return OnboardingImportView{}, err
	}
	if err := s.validateExternalTarget(ctx, invocation, command.Mode, command.ScopeID, command.RoleID, true); err != nil {
		return OnboardingImportView{}, err
	}

	at := model.TimeUTC(s.now())
	created, err := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionOnboardingBatchManage,
		Resource: resource, ScopeType: command.ScopeType, ScopeID: command.ScopeID, Operation: "onboarding_import.upload",
		Value: map[string]any{"mode": command.Mode, "scope_type": command.ScopeType, "scope_id": command.ScopeID}}, func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*store.OnboardingImport, error) {
			id := model.NewOnboardingImportID()
			if _, _, stageErr := s.content.StageOnboardingImport(ctx, id, command.Body, store.OnboardingImportMaximumBytes); stageErr != nil {
				if s.content.IsOnboardingImportTooLarge(stageErr) {
					return nil, NewError("onboarding_import.invalid_file").Wrap(stageErr)
				}
				return nil, NewError("onboarding_import.unavailable").Wrap(stageErr)
			}
			job, jobErr := newOnboardingImportJob(model.JobTypeOnboardingImportParse, id, at)
			if jobErr != nil {
				_ = s.content.RemoveOnboardingImport(ctx, id)
				return nil, jobErr
			}
			value := &store.OnboardingImport{ID: id, Mode: command.Mode, State: model.OnboardingImportParsing, ScopeType: command.ScopeType, ScopeID: command.ScopeID,
				ActorUserID: invocation.Principal().UserID, Principal: invocation.Principal(), ParseJobID: job.ID, CreatedAt: at, UpdatedAt: at, ExpiresAt: at.Add(onboardingImportRetention), Revision: 1}
			if command.RoleID != "" {
				value.RoleID = model.RoleID(command.RoleID)
			}
			created, createErr := s.imports.CreateOnboardingImport(ctx, &store.OnboardingImportCreation{Import: value, ParseJob: job, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
			if createErr != nil {
				_ = s.content.RemoveOnboardingImport(ctx, id)
			}
			return created, createErr
		}, onboardingImportError)
	if err != nil {
		return OnboardingImportView{}, err
	}
	if s.wake != nil {
		s.wake()
	}
	return onboardingImportView(created), nil
}

func newOnboardingImportJob(kind model.JobType, id model.OnboardingImportID, at time.Time) (*model.Job, error) {
	command, err := json.Marshal(onboardingImportJobCommandV1{ImportID: id.String()})
	if err != nil {
		return nil, err
	}
	return model.NewJob(model.NewJobID(), kind, 1, command, string(kind)+":"+id.String(), at, at, 8)
}

func (s *onboardingImportService) Get(ctx context.Context, invocation Invocation, rawID string) (OnboardingImportView, []OnboardingImportRowView, error) {
	id := model.OnboardingImportID(strings.TrimSpace(rawID))
	value, err := s.imports.GetOnboardingImport(ctx, id)
	if err != nil {
		return OnboardingImportView{}, nil, onboardingImportError(err)
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionOnboardingBatchView, onboardingImportResource(value.ScopeType, value.ScopeID)); err != nil {
		return OnboardingImportView{}, nil, err
	}
	rows := make([]OnboardingImportRowView, 0, value.TotalRows)
	for after := 0; ; {
		page, pageErr := s.imports.ListOnboardingImportRows(ctx, id, after, store.OnboardingImportPageSize)
		if pageErr != nil {
			return OnboardingImportView{}, nil, onboardingImportError(pageErr)
		}
		for _, row := range page.Rows {
			rows = append(rows, safeOnboardingImportRow(row))
			after = row.RowNumber
		}
		if !page.More {
			break
		}
	}
	return onboardingImportView(value), rows, nil
}

func (s *onboardingImportService) Commit(ctx context.Context, invocation Invocation, command CommitOnboardingImportCommand) (OnboardingImportView, error) {
	id := model.OnboardingImportID(strings.TrimSpace(command.ID))
	if !id.IsValid() || command.ExpectedRevision < 1 || len(command.PreviewDigest) != sha256.Size*2 || !command.Policy.IsValid() || !validInvitationBatchItemKey(command.IdempotencyKey) {
		return OnboardingImportView{}, NewError("request.invalid")
	}
	current, err := s.imports.GetOnboardingImport(ctx, id)
	if err != nil {
		return OnboardingImportView{}, onboardingImportError(err)
	}
	resource := onboardingImportResource(current.ScopeType, current.ScopeID)
	if err = s.authorization.Authorize(ctx, invocation, model.ActionOnboardingBatchManage, resource); err != nil {
		return OnboardingImportView{}, err
	}
	if err = s.validatePrincipal(ctx, invocation.Principal()); err != nil {
		return OnboardingImportView{}, err
	}
	if current.ActorUserID != invocation.Principal().UserID {
		return OnboardingImportView{}, authorizationDeniedError("onboardingImportService.Commit")
	}
	at := model.TimeUTC(s.now())
	job, err := newOnboardingImportJob(model.JobTypeOnboardingImportExecute, id, at)
	if err != nil {
		return OnboardingImportView{}, NewError("onboarding_import.unavailable").Wrap(err)
	}
	digest := sha256.Sum256([]byte(command.IdempotencyKey))
	committed, err := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionOnboardingBatchManage,
		Resource: resource, ScopeType: current.ScopeType, ScopeID: current.ScopeID, Operation: "onboarding_import.commit",
		Value: map[string]any{"onboarding_import_id": id, "policy": command.Policy,
			"total_rows": current.TotalRows, "valid_rows": current.ValidRows, "invalid_rows": current.InvalidRows}}, func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*store.OnboardingImport, error) {
			return s.imports.CommitOnboardingImport(ctx, &store.OnboardingImportCommit{ID: id, ActorUserID: invocation.Principal().UserID,
				ExpectedRevision: command.ExpectedRevision, PreviewDigest: command.PreviewDigest, Policy: command.Policy, IdempotencyKey: digest, ExecutionJob: job, At: at,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, onboardingImportError)
	if err != nil {
		return OnboardingImportView{}, err
	}
	if s.wake != nil {
		s.wake()
	}
	return onboardingImportView(committed), nil
}

func (s *onboardingImportService) Cancel(ctx context.Context, invocation Invocation, rawID string) (OnboardingImportView, error) {
	id := model.OnboardingImportID(strings.TrimSpace(rawID))
	current, err := s.imports.GetOnboardingImport(ctx, id)
	if err != nil {
		return OnboardingImportView{}, onboardingImportError(err)
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionOnboardingBatchManage, onboardingImportResource(current.ScopeType, current.ScopeID)); err != nil {
		return OnboardingImportView{}, err
	}
	at := model.TimeUTC(s.now())
	canceled, err := runAuditedMutation(ctx, s.audit, mutationAttempt{Invocation: invocation, Action: model.ActionOnboardingBatchManage,
		Resource: onboardingImportResource(current.ScopeType, current.ScopeID), ScopeType: current.ScopeType, ScopeID: current.ScopeID,
		Operation: "onboarding_import.cancel", Value: map[string]any{"onboarding_import_id": id, "state": current.State}}, func() time.Time { return at },
		func(ctx context.Context, reference mutationAttemptReference) (*store.OnboardingImport, error) {
			return s.imports.CancelOnboardingImport(ctx, &store.OnboardingImportCancellation{ID: id, ActorUserID: invocation.Principal().UserID, At: at,
				AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
		}, onboardingImportError)
	if err != nil {
		return OnboardingImportView{}, err
	}
	if current.State == model.OnboardingImportParsing || current.State == model.OnboardingImportUploading {
		_ = s.content.RemoveOnboardingImport(context.WithoutCancel(ctx), id)
	}
	return onboardingImportView(canceled), nil
}

func (s *onboardingImportService) Report(ctx context.Context, invocation Invocation, rawID string, output io.Writer) error {
	if output == nil {
		return NewError("request.invalid")
	}
	id := model.OnboardingImportID(strings.TrimSpace(rawID))
	current, err := s.imports.GetOnboardingImport(ctx, id)
	if err != nil {
		return onboardingImportError(err)
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionOnboardingBatchView, onboardingImportResource(current.ScopeType, current.ScopeID)); err != nil {
		return err
	}
	if current.State != model.OnboardingImportCompleted && current.State != model.OnboardingImportCompletedWithErrors &&
		current.State != model.OnboardingImportCanceled && current.State != model.OnboardingImportFailed {
		return NewError("onboarding_import.conflict")
	}
	writer := csv.NewWriter(output)
	if err = writer.Write([]string{"row", "reference", "operation", "status", "invitation_id", "public_code"}); err != nil {
		return NewError("onboarding_import.unavailable").Wrap(err)
	}
	for after := 0; ; {
		page, pageErr := s.imports.ListOnboardingImportRows(ctx, id, after, store.OnboardingImportPageSize)
		if pageErr != nil {
			return onboardingImportError(pageErr)
		}
		for _, row := range page.Rows {
			if err = writer.Write([]string{strconv.Itoa(row.RowNumber), escapeSpreadsheetFormula(row.Reference), row.Operation, string(row.Status), row.InvitationID.String(), row.PublicCode}); err != nil {
				return NewError("onboarding_import.unavailable").Wrap(err)
			}
			after = row.RowNumber
		}
		if !page.More {
			break
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return NewError("onboarding_import.unavailable").Wrap(err)
	}
	return nil
}

func onboardingImportView(value *store.OnboardingImport) OnboardingImportView {
	if value == nil {
		return OnboardingImportView{}
	}
	return OnboardingImportView{ID: value.ID, Mode: value.Mode, State: value.State, ScopeType: value.ScopeType, ScopeID: value.ScopeID, RoleID: value.RoleID,
		PreviewDigest: value.PreviewDigest, IgnoredHeaders: append([]string(nil), value.IgnoredHeaders...), TotalRows: value.TotalRows, ValidRows: value.ValidRows,
		InvalidRows: value.InvalidRows, SucceededRows: value.SucceededRows, NoOpRows: value.NoOpRows, FailedRows: value.FailedRows, SkippedRows: value.SkippedRows,
		CommitPolicy: value.CommitPolicy, ParseJobID: value.ParseJobID, ExecutionJobID: value.ExecutionJobID, CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt, ExpiresAt: value.ExpiresAt, Revision: value.Revision, FailureCode: value.FailureCode}
}

func safeOnboardingImportRow(row store.OnboardingImportRow) OnboardingImportRowView {
	return OnboardingImportRowView{RowNumber: row.RowNumber, Reference: row.Reference, Operation: row.Operation, Status: row.Status,
		PreviewCode: row.PreviewCode, PublicCode: row.PublicCode, InvitationID: row.InvitationID}
}

func onboardingImportResource(scope model.RoleScopeType, id string) model.Resource {
	resourceType := model.ResourceInstitution
	if scope == model.RoleScopeAcademicUnit {
		resourceType = model.ResourceAcademicUnit
	}
	if scope == model.RoleScopeClass {
		resourceType = model.ResourceClass
	}
	return model.Resource{Type: resourceType, ID: id}
}

func (s *onboardingImportService) validatePrincipal(ctx context.Context, principal model.Principal) error {
	if principal.Validate() != nil {
		return invalidTokenAppError()
	}
	if principal.CredentialType == model.CredentialSessionAccess {
		return s.authentication.ValidatePrincipal(ctx, principal)
	}
	token, err := s.tokens.Get(ctx, principal.CredentialID.String())
	if err != nil {
		if store.IsNotFound(err) {
			return invalidTokenAppError()
		}
		return NewError("administration.unavailable").Wrap(err)
	}
	if token.UserID != principal.UserID || !token.IsActiveAt(s.now()) || !slices.Equal(token.Scopes, principal.CredentialScopes) || token.AcademicUnitID != principal.AcademicUnitID {
		return invalidTokenAppError()
	}
	user, err := s.users.Get(ctx, principal.UserID.String())
	if err != nil {
		if store.IsNotFound(err) {
			return invalidTokenAppError()
		}
		return NewError("administration.unavailable").Wrap(err)
	}
	if !user.IsActive() {
		return invalidTokenAppError()
	}
	return nil
}

func (s *onboardingImportService) validateExternalTarget(ctx context.Context, invocation Invocation, mode model.OnboardingImportMode, scopeID, roleID string, preview bool) error {
	switch mode {
	case model.OnboardingImportStudentClass:
		class, err := s.classes.Get(ctx, scopeID)
		if err != nil || class.IsArchived() {
			return invitationError(err)
		}
		period, err := s.periods.Get(ctx, class.AcademicPeriodID.String())
		if err != nil || period.IsArchived() {
			return invitationError(err)
		}
		if preview {
			for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage} {
				if err = s.authorization.Authorize(ctx, invocation, action, model.Resource{Type: model.ResourceClass, ID: scopeID}); err != nil {
					return err
				}
			}
		}
	case model.OnboardingImportTeacherAcademicUnit:
		unit, err := s.units.Get(ctx, scopeID)
		if err != nil || unit.IsArchived() {
			return invitationError(err)
		}
		role, err := s.roles.Get(ctx, roleID)
		if err != nil || role.IsArchived() {
			return invitationError(err)
		}
		if err = validateInvitationDelegableRole(role, model.ResourceAcademicUnit); err != nil {
			return err
		}
		if preview {
			resource := model.Resource{Type: model.ResourceAcademicUnit, ID: scopeID}
			for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage} {
				if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
					return err
				}
			}
			return s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, scopeID)
		}
	default:
		return nil
	}
	return nil
}

func onboardingImportError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := As(err); ok {
		return err
	}
	if store.IsNotFound(err) {
		return NewError("resource.not_found")
	}
	if store.IsConflict(err) {
		return NewError("onboarding_import.conflict").Wrap(err)
	}
	var invalid *store.ErrInvalidInput
	if errors.As(err, &invalid) {
		return NewError("request.invalid").Wrap(err)
	}
	return NewError("onboarding_import.unavailable").Wrap(err)
}

func escapeSpreadsheetFormula(value string) string {
	if value == "" {
		return value
	}
	if strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

type onboardingImportJobCommandV1 struct {
	ImportID string `json:"import_id"`
}

func (s *onboardingImportService) parse(ctx context.Context, id model.OnboardingImportID) error {
	current, err := s.imports.GetOnboardingImport(ctx, id)
	if err != nil {
		return err
	}
	if current.State == model.OnboardingImportPreviewReady {
		return s.content.RemoveOnboardingImport(context.WithoutCancel(ctx), id)
	}
	if current.State == model.OnboardingImportFailed || current.State == model.OnboardingImportCanceled {
		return s.content.RemoveOnboardingImport(context.WithoutCancel(ctx), id)
	}
	if current.State != model.OnboardingImportParsing {
		return store.NewErrConflict("onboarding_import", "state", nil)
	}
	invocation := NewInvocation(current.Principal, model.RequestMetadata{})
	if err = s.validatePrincipal(ctx, current.Principal); err != nil {
		if onboardingImportRetryableError(err) {
			return err
		}
		if _, failErr := s.imports.FailOnboardingImport(ctx, id, "authentication.invalid_token", model.TimeUTC(s.now())); failErr != nil {
			return failErr
		}
		if removeErr := s.content.RemoveOnboardingImport(context.WithoutCancel(ctx), id); removeErr != nil {
			return removeErr
		}
		return err
	}
	if err = s.authorization.Authorize(ctx, invocation, model.ActionOnboardingBatchManage, onboardingImportResource(current.ScopeType, current.ScopeID)); err != nil {
		if onboardingImportRetryableError(err) {
			return err
		}
		if _, failErr := s.imports.FailOnboardingImport(ctx, id, invitationBatchPublicErrorCode(err), model.TimeUTC(s.now())); failErr != nil {
			return failErr
		}
		if removeErr := s.content.RemoveOnboardingImport(context.WithoutCancel(ctx), id); removeErr != nil {
			return removeErr
		}
		return err
	}
	if current.Mode != model.OnboardingImportInstitution {
		if err = s.validateExternalTarget(ctx, invocation, current.Mode, current.ScopeID, current.RoleID.String(), true); err != nil {
			if onboardingImportRetryableError(err) {
				return err
			}
			if _, failErr := s.imports.FailOnboardingImport(ctx, id, invitationBatchPublicErrorCode(err), model.TimeUTC(s.now())); failErr != nil {
				return failErr
			}
			if removeErr := s.content.RemoveOnboardingImport(context.WithoutCancel(ctx), id); removeErr != nil {
				return removeErr
			}
			return err
		}
	}
	reader, err := s.content.OpenOnboardingImport(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	rows, ignored, digest, err := s.parseCSV(ctx, invocation, current, reader)
	if err != nil {
		if !errors.Is(err, errOnboardingImportInvalidFile) {
			return err
		}
		if _, failErr := s.imports.FailOnboardingImport(ctx, id, "onboarding_import.invalid_file", model.TimeUTC(s.now())); failErr != nil {
			return failErr
		}
		if removeErr := s.content.RemoveOnboardingImport(context.WithoutCancel(ctx), id); removeErr != nil {
			return removeErr
		}
		return err
	}
	_, err = s.imports.CompleteOnboardingImportPreview(ctx, &store.OnboardingImportPreviewCompletion{ID: id, ExpectedRevision: current.Revision,
		Digest: digest, IgnoredHeaders: ignored, Rows: rows, At: model.TimeUTC(s.now())})
	if err != nil {
		return err
	}
	return s.content.RemoveOnboardingImport(context.WithoutCancel(ctx), id)
}

var errOnboardingImportInvalidFile = errors.New("onboarding import file is invalid")

var onboardingImportKnownHeaders = map[string]struct{}{
	"email": {}, "reference": {}, "kind": {}, "username": {}, "display_name": {}, "first_name": {}, "last_name": {}, "locale": {}, "timezone": {},
	"start_at": {}, "end_at": {}, "academic_unit": {}, "academic_period": {}, "programme": {}, "programme_level": {}, "class": {}, "role": {},
}

func (s *onboardingImportService) parseCSV(ctx context.Context, invocation Invocation, current *store.OnboardingImport, body io.Reader) ([]store.OnboardingImportRow, []string, string, error) {
	data, err := io.ReadAll(io.LimitReader(body, store.OnboardingImportMaximumBytes+1))
	if err != nil {
		return nil, nil, "", err
	}
	if len(data) > store.OnboardingImportMaximumBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, "", invalidOnboardingImportFile("invalid CSV encoding or size")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil || len(header) < 1 || len(header) > store.OnboardingImportMaximumColumns {
		return nil, nil, "", invalidOnboardingImportFile("invalid CSV header")
	}
	indexes := make(map[string]int, len(header))
	ignored := make([]string, 0)
	for index, raw := range header {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" || len(name) > 128 || model.SanitizeUnicode(name) != name {
			return nil, nil, "", invalidOnboardingImportFile("invalid CSV header")
		}
		if _, exists := indexes[name]; exists {
			return nil, nil, "", invalidOnboardingImportFile("duplicate CSV header")
		}
		indexes[name] = index
		if _, known := onboardingImportKnownHeaders[name]; !known {
			ignored = append(ignored, name)
		}
	}
	if _, exists := indexes["email"]; !exists {
		return nil, nil, "", invalidOnboardingImportFile("email header is required")
	}
	slices.Sort(ignored)
	rows := make([]store.OnboardingImportRow, 0)
	for number := 1; ; number++ {
		record, readErr := reader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil || len(record) != len(header) || number > store.OnboardingImportMaximumRows {
			return nil, nil, "", invalidOnboardingImportFile("invalid CSV row")
		}
		values := make(map[string]string, len(indexes))
		for name, index := range indexes {
			value := strings.TrimSpace(record[index])
			if len(value) > store.OnboardingImportMaximumField || model.SanitizeUnicode(value) != value {
				return nil, nil, "", invalidOnboardingImportFile("invalid CSV field")
			}
			values[name] = value
		}
		row := store.OnboardingImportRow{ImportID: current.ID, RowNumber: number, Reference: values["reference"], ScopeType: current.ScopeType, ScopeID: current.ScopeID, Email: strings.ToLower(values["email"]),
			Username: values["username"], DisplayName: values["display_name"], FirstName: values["first_name"], LastName: values["last_name"], Locale: values["locale"], Timezone: values["timezone"], UpdatedAt: model.TimeUTC(s.now())}
		if row.Reference == "" {
			row.Reference = strconv.Itoa(number)
		}
		if len(row.Reference) > 128 {
			row.Reference = strconv.Itoa(number)
		}
		if len(row.Email) > model.UserEmailMaxLength {
			row.Email = ""
		}
		row.StartsAt, row.EndsAt, err = parseOnboardingImportBounds(values["start_at"], values["end_at"])
		boundsErr := err
		resolveErr := s.resolveAndValidateImportRow(ctx, invocation, current, values, &row)
		if onboardingImportRetryableError(resolveErr) {
			return nil, nil, "", resolveErr
		}
		if boundsErr != nil {
			row.PreviewStatus, row.Status, row.PreviewCode = model.OnboardingImportRowInvalid, model.OnboardingImportRowInvalid, "request.invalid"
		} else if resolveErr != nil {
			row.PreviewStatus, row.Status, row.PreviewCode = model.OnboardingImportRowInvalid, model.OnboardingImportRowInvalid, onboardingImportPreviewErrorCode(resolveErr)
		} else {
			row.PreviewStatus, row.Status = model.OnboardingImportRowValid, model.OnboardingImportRowValid
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, nil, "", invalidOnboardingImportFile("CSV has no data rows")
	}
	duplicates := make(map[string]int, len(rows))
	for _, row := range rows {
		if key := onboardingImportDuplicateKey(row); key != "" {
			duplicates[key]++
		}
	}
	for index := range rows {
		if key := onboardingImportDuplicateKey(rows[index]); key != "" && duplicates[key] > 1 {
			rows[index].PreviewStatus, rows[index].Status, rows[index].PreviewCode = model.OnboardingImportRowDuplicate, model.OnboardingImportRowDuplicate, "onboarding_batch.duplicate"
		}
	}
	digestRows := make([]store.OnboardingImportRow, len(rows))
	copy(digestRows, rows)
	for index := range digestRows {
		digestRows[index].ImportID = ""
		digestRows[index].UpdatedAt = time.Time{}
	}
	document, err := json.Marshal(struct {
		Version int                         `json:"version"`
		Ignored []string                    `json:"ignored"`
		Rows    []store.OnboardingImportRow `json:"rows"`
	}{1, ignored, digestRows})
	if err != nil {
		return nil, nil, "", err
	}
	digest := sha256.Sum256(document)
	return rows, ignored, hex.EncodeToString(digest[:]), nil
}

func invalidOnboardingImportFile(message string) error {
	return fmt.Errorf("%w: %s", errOnboardingImportInvalidFile, message)
}

func parseOnboardingImportBounds(start, end string) (int64, int64, error) {
	parse := func(value string) (int64, error) {
		if value == "" {
			return 0, nil
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return 0, err
		}
		return parsed.UnixMilli(), nil
	}
	starts, err := parse(start)
	if err != nil {
		return 0, 0, err
	}
	ends, err := parse(end)
	if err != nil || (ends != 0 && ends <= starts) {
		return 0, 0, errors.New("invalid effective bounds")
	}
	return starts, ends, nil
}

func onboardingImportDuplicateKey(row store.OnboardingImportRow) string {
	if row.Operation == "" || !row.ScopeType.IsValid() || !model.IsValidId(row.ScopeID) || row.Email == "" {
		return ""
	}
	return row.Operation + "\x00" + string(row.ScopeType) + "\x00" + row.ScopeID + "\x00" + strings.ToLower(row.Email) + "\x00" + row.RoleID.String()
}

func onboardingImportPreviewErrorCode(err error) string {
	if _, ok := As(err); !ok {
		return "request.invalid"
	}
	return invitationBatchPublicErrorCode(err)
}

func (s *onboardingImportService) resolveAndValidateImportRow(ctx context.Context, invocation Invocation, current *store.OnboardingImport, values map[string]string, row *store.OnboardingImportRow) error {
	if row.Timezone != "" && len(row.Timezone) > model.UserTimezoneMaxLength {
		return errors.New("timezone is too long")
	}
	if current.Mode == model.OnboardingImportStudentClass {
		class, err := s.classes.Get(ctx, current.ScopeID)
		if err != nil {
			return invitationError(err)
		}
		period, err := s.periods.Get(ctx, class.AcademicPeriodID.String())
		if err != nil {
			return invitationError(err)
		}
		row.Operation, row.ScopeType, row.ScopeID, row.TargetRevision = string(InvitationBatchStudentClassCreate), model.RoleScopeClass, class.ID.String(), class.Revision
		return validateImportStudentCandidate(current.ActorUserID, class, period, row)
	}
	if current.Mode == model.OnboardingImportTeacherAcademicUnit {
		unit, err := s.units.Get(ctx, current.ScopeID)
		if err != nil {
			return invitationError(err)
		}
		role, err := s.roles.Get(ctx, current.RoleID.String())
		if err != nil {
			return invitationError(err)
		}
		row.Operation, row.ScopeType, row.ScopeID, row.TargetRevision, row.RoleID, row.RoleRevision = string(InvitationBatchTeacherAcademicUnitCreate), model.RoleScopeAcademicUnit, unit.ID.String(), unit.Revision, role.ID, role.UpdatedAt.UnixMicro()
		return validateImportTeacherCandidate(current.ActorUserID, unit, role, row)
	}
	return s.resolveInstitutionImportRow(ctx, invocation, current, values, row)
}

func (s *onboardingImportService) resolveInstitutionImportRow(ctx context.Context, invocation Invocation, current *store.OnboardingImport, values map[string]string, row *store.OnboardingImportRow) error {
	kind := strings.ToLower(values["kind"])
	if kind == "" {
		return NewError("request.invalid")
	}
	institution, err := s.institutions.Get(ctx, current.ScopeID)
	if err != nil {
		return invitationError(err)
	}
	if kind == "institution_role" {
		role, roleErr := s.roles.GetByName(ctx, values["role"])
		if roleErr != nil {
			return invitationError(roleErr)
		}
		if err = s.previewRoleAuthorization(ctx, invocation, role, model.ResourceInstitution, model.RoleScopeInstitution, institution.ID.String()); err != nil {
			return err
		}
		row.Operation, row.ScopeType, row.ScopeID, row.TargetRevision, row.RoleID, row.RoleRevision = string(InvitationBatchInstitutionRoleCreate), model.RoleScopeInstitution, institution.ID.String(), institution.Revision, role.ID, role.UpdatedAt.UnixMicro()
		return validateImportRoleCandidate(current.ActorUserID, model.InvitationPurposeInstitutionRole, model.AcademicUnitID(""), role, row)
	}
	unit, err := s.resolveExactAcademicUnit(ctx, institution.ID.String(), values["academic_unit"])
	if err != nil {
		return err
	}
	if kind == "teacher" || kind == "academic_unit_role" {
		role, roleErr := s.roles.GetByName(ctx, values["role"])
		if roleErr != nil {
			return invitationError(roleErr)
		}
		if kind == "teacher" {
			resource := model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()}
			for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionAcademicUnitMembersManage} {
				if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
					return err
				}
			}
			if err = s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, model.RoleScopeAcademicUnit, unit.ID.String()); err != nil {
				return err
			}
			row.Operation = string(InvitationBatchTeacherAcademicUnitCreate)
		} else {
			if err = s.previewRoleAuthorization(ctx, invocation, role, model.ResourceAcademicUnit, model.RoleScopeAcademicUnit, unit.ID.String()); err != nil {
				return err
			}
			row.Operation = string(InvitationBatchAcademicUnitRoleCreate)
		}
		row.ScopeType, row.ScopeID, row.TargetRevision, row.RoleID, row.RoleRevision = model.RoleScopeAcademicUnit, unit.ID.String(), unit.Revision, role.ID, role.UpdatedAt.UnixMicro()
		if kind == "teacher" {
			return validateImportTeacherCandidate(current.ActorUserID, unit, role, row)
		}
		return validateImportRoleCandidate(current.ActorUserID, model.InvitationPurposeAcademicUnitRole, unit.ID, role, row)
	}
	if kind != "student" {
		return NewError("request.invalid")
	}
	programme, err := s.programmes.GetByName(ctx, unit.ID.String(), values["programme"])
	if err != nil {
		return invitationError(err)
	}
	level, err := s.levels.GetByName(ctx, programme.ID.String(), values["programme_level"])
	if err != nil {
		return invitationError(err)
	}
	period, err := s.periods.GetByOwnerName(ctx, model.Resource{Type: model.ResourceAcademicUnit, ID: unit.ID.String()}, values["academic_period"])
	if store.IsNotFound(err) {
		period, err = s.periods.GetByOwnerName(ctx, model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}, values["academic_period"])
	}
	if err != nil {
		return invitationError(err)
	}
	class, err := s.classes.GetByName(ctx, level.ID.String(), period.ID.String(), values["class"])
	if err != nil {
		return invitationError(err)
	}
	resource := model.Resource{Type: model.ResourceClass, ID: class.ID.String()}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionClassMembersManage} {
		if err = s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return err
		}
	}
	row.Operation, row.ScopeType, row.ScopeID, row.TargetRevision = string(InvitationBatchStudentClassCreate), model.RoleScopeClass, class.ID.String(), class.Revision
	return validateImportStudentCandidate(current.ActorUserID, class, period, row)
}

func (s *onboardingImportService) resolveExactAcademicUnit(ctx context.Context, institutionID, name string) (*model.AcademicUnit, error) {
	if !model.IsValidId(institutionID) || name == "" {
		return nil, NewError("request.invalid")
	}
	exact, err := s.units.GetByName(ctx, institutionID, name)
	if err != nil {
		return nil, invitationError(err)
	}
	if exact == nil || exact.IsArchived() {
		return nil, NewError("resource.not_found")
	}
	return exact, nil
}

func (s *onboardingImportService) previewRoleAuthorization(ctx context.Context, invocation Invocation, role *model.Role, resourceType model.ResourceType, scopeType model.RoleScopeType, scopeID string) error {
	if s.invitations != nil {
		if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.invitations.recentAuthenticationTTL); err != nil {
			return err
		}
	}
	if err := validateInvitationDelegableRole(role, resourceType); err != nil {
		return err
	}
	resource := model.Resource{Type: resourceType, ID: scopeID}
	for _, action := range []model.Action{model.ActionInvitationCreate, model.ActionRoleBindingManage} {
		if err := s.authorization.Authorize(ctx, invocation, action, resource); err != nil {
			return err
		}
	}
	return s.authorization.CanDelegateActionsAtScope(ctx, invocation, role.Permissions, scopeType, scopeID)
}

func validateImportStudentCandidate(actor model.UserID, class *model.Class, period *model.AcademicPeriod, row *store.OnboardingImportRow) error {
	starts := model.TimeFromMillis(row.StartsAt)
	if starts.IsZero() {
		starts = period.StartsAt
	}
	candidate, err := model.NewStudentClassInvitation(model.StudentClassInvitationInput{ID: model.NewInvitationID(), TargetEmail: row.Email, ClassID: class.ID, AcademicPeriodID: period.ID,
		IntendedStartsAt: starts, IntendedEndsAt: model.OptionalTimeFromMillis(row.EndsAt), Suggestions: model.InvitationProfileSuggestions{Username: row.Username, DisplayName: row.DisplayName, FirstName: row.FirstName, LastName: row.LastName, Locale: row.Locale, Timezone: row.Timezone},
		InviterUserID: actor, ScopeType: model.RoleScopeClass, ScopeID: class.ID.String(), ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: row.UpdatedAt})
	if err == nil {
		row.Email = candidate.TargetEmail
		row.StartsAt = model.MillisFromTime(candidate.IntendedStartsAt)
	}
	return err
}

func validateImportTeacherCandidate(actor model.UserID, unit *model.AcademicUnit, role *model.Role, row *store.OnboardingImportRow) error {
	starts := model.TimeFromMillis(row.StartsAt)
	if starts.IsZero() {
		starts = row.UpdatedAt
	}
	candidate, err := model.NewTeacherAcademicUnitInvitation(model.TeacherAcademicUnitInvitationInput{ID: model.NewInvitationID(), TargetEmail: row.Email, AcademicUnitID: unit.ID, RoleID: role.ID, RoleActions: role.Permissions,
		IntendedStartsAt: starts, IntendedEndsAt: model.OptionalTimeFromMillis(row.EndsAt), Suggestions: model.InvitationProfileSuggestions{Username: row.Username, DisplayName: row.DisplayName, FirstName: row.FirstName, LastName: row.LastName, Locale: row.Locale, Timezone: row.Timezone},
		InviterUserID: actor, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unit.ID.String(), ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: row.UpdatedAt})
	if err == nil {
		row.Email = candidate.TargetEmail
		row.StartsAt = model.MillisFromTime(candidate.IntendedStartsAt)
	}
	return err
}

func validateImportRoleCandidate(actor model.UserID, purpose model.InvitationPurpose, unitID model.AcademicUnitID, role *model.Role, row *store.OnboardingImportRow) error {
	starts := model.TimeFromMillis(row.StartsAt)
	if starts.IsZero() {
		starts = row.UpdatedAt
	}
	candidate, err := model.NewScopedRoleInvitation(model.ScopedRoleInvitationInput{ID: model.NewInvitationID(), Purpose: purpose, TargetEmail: row.Email, AcademicUnitID: unitID, RoleID: role.ID,
		RoleActions: role.Permissions, IntendedStartsAt: starts, IntendedEndsAt: model.OptionalTimeFromMillis(row.EndsAt), InviterUserID: actor, ScopeType: row.ScopeType, ScopeID: row.ScopeID,
		ClaimHash: strings.Repeat("0", model.TokenHashLength), IssuedAt: row.UpdatedAt})
	if err == nil {
		row.Email = candidate.TargetEmail
		row.StartsAt = model.MillisFromTime(candidate.IntendedStartsAt)
	}
	return err
}

func (s *onboardingImportService) execute(ctx context.Context, id model.OnboardingImportID, startAfter int, checkpoint func(int, int) error) error {
	current, err := s.imports.GetOnboardingImport(ctx, id)
	if err != nil {
		return err
	}
	if current.State == model.OnboardingImportCompleted || current.State == model.OnboardingImportCompletedWithErrors || current.State == model.OnboardingImportCanceled {
		return nil
	}
	if current.State != model.OnboardingImportExecuting {
		return store.NewErrConflict("onboarding_import", "state", nil)
	}
	invocation := NewInvocation(current.Principal, model.RequestMetadata{})
	after, processed := startAfter, 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		latest, getErr := s.imports.GetOnboardingImport(ctx, id)
		if getErr != nil {
			return getErr
		}
		if latest.State == model.OnboardingImportCanceled {
			return nil
		}
		page, pageErr := s.imports.ListOnboardingImportRows(ctx, id, after, store.OnboardingImportPageSize)
		if pageErr != nil {
			return pageErr
		}
		for _, row := range page.Rows {
			after = row.RowNumber
			if row.Status != model.OnboardingImportRowPending {
				continue
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			latest, getErr = s.imports.GetOnboardingImport(ctx, id)
			if getErr != nil {
				return getErr
			}
			if latest.State == model.OnboardingImportCanceled {
				return nil
			}
			if latest.State != model.OnboardingImportExecuting {
				return store.NewErrConflict("onboarding_import", "state", nil)
			}
			status, invitationID, code, executeErr := s.executeRow(ctx, invocation, row)
			if executeErr != nil {
				return executeErr
			}
			if status == model.OnboardingImportRowFailed {
				if _, err = s.imports.CompleteOnboardingImportRow(ctx, &store.OnboardingImportRowCompletion{ID: id, RowNumber: row.RowNumber, Status: status, InvitationID: invitationID, PublicCode: code, At: model.TimeUTC(s.now())}); err != nil {
					return err
				}
			}
			processed++
			if checkpoint != nil {
				if err = checkpoint(after, processed); err != nil {
					return err
				}
			}
		}
		if !page.More {
			_, err = s.imports.FinishOnboardingImport(ctx, id, model.TimeUTC(s.now()))
			return err
		}
	}
}

func (s *onboardingImportService) executeRow(ctx context.Context, invocation Invocation, row store.OnboardingImportRow) (model.OnboardingImportRowStatus, model.InvitationID, string, error) {
	if err := s.validatePrincipal(ctx, invocation.Principal()); err != nil {
		if onboardingImportRetryableError(err) {
			return "", "", "", err
		}
		return model.OnboardingImportRowFailed, "", "authentication.invalid_token", nil
	}
	if err := s.revalidateFrozenTarget(ctx, row); err != nil {
		if onboardingImportRetryableError(err) {
			return "", "", "", err
		}
		return model.OnboardingImportRowFailed, "", invitationBatchPublicErrorCode(err), nil
	}
	result, err := s.invitations.RunBatch(ctx, invocation, RunInvitationBatchCommand{Operation: InvitationBatchOperation(row.Operation), ScopeType: row.ScopeType, ScopeID: row.ScopeID,
		onboardingImportID: row.ImportID, onboardingImportRowNumber: row.RowNumber,
		IdempotencyKey: row.ImportID.String(), Items: []InvitationBatchItemCommand{{IdempotencyKey: strconv.Itoa(row.RowNumber), TargetEmail: row.Email, RoleID: row.RoleID.String(),
			IntendedStartsAt: row.StartsAt, IntendedEndsAt: row.EndsAt, SuggestedUsername: row.Username, SuggestedDisplayName: row.DisplayName, SuggestedFirstName: row.FirstName, SuggestedLastName: row.LastName, SuggestedLocale: row.Locale, SuggestedTimezone: row.Timezone}}})
	if err != nil || len(result.Items) != 1 {
		if err != nil && onboardingImportRetryableError(err) {
			return "", "", "", err
		}
		return model.OnboardingImportRowFailed, "", invitationBatchPublicErrorCode(err), nil
	}
	item := result.Items[0]
	if item.Status == InvitationBatchItemSucceeded {
		return model.OnboardingImportRowSucceeded, item.InvitationID, "", nil
	}
	if item.Status == InvitationBatchItemNoOp {
		return model.OnboardingImportRowNoOp, item.InvitationID, "", nil
	}
	if onboardingImportRetryablePublicCode(item.ErrorCode) {
		return "", "", "", NewError(item.ErrorCode)
	}
	return model.OnboardingImportRowFailed, "", item.ErrorCode, nil
}

func onboardingImportRetryableError(err error) bool {
	appErr, ok := As(err)
	return ok && onboardingImportRetryablePublicCode(appErr.Code())
}

func onboardingImportRetryablePublicCode(code string) bool {
	switch code {
	case "administration.unavailable", "authorization.unavailable", "audit.unavailable", "dependency.unavailable",
		"authentication.internal", "idempotency.in_progress", "invitation.mail_unavailable", "invitation.unavailable", "onboarding_import.unavailable":
		return true
	default:
		return false
	}
}

func (s *onboardingImportService) revalidateFrozenTarget(ctx context.Context, row store.OnboardingImportRow) error {
	switch InvitationBatchOperation(row.Operation) {
	case InvitationBatchStudentClassCreate:
		value, err := s.classes.Get(ctx, row.ScopeID)
		if err != nil {
			return invitationError(err)
		}
		if value.Revision != row.TargetRevision || value.IsArchived() {
			return NewError("invitation.conflict")
		}
	case InvitationBatchTeacherAcademicUnitCreate, InvitationBatchAcademicUnitRoleCreate:
		value, err := s.units.Get(ctx, row.ScopeID)
		if err != nil {
			return invitationError(err)
		}
		if value.Revision != row.TargetRevision || value.IsArchived() {
			return NewError("invitation.conflict")
		}
		role, err := s.roles.Get(ctx, row.RoleID.String())
		if err != nil {
			return invitationError(err)
		}
		if role.UpdatedAt.UnixMicro() != row.RoleRevision || role.IsArchived() {
			return NewError("invitation.conflict")
		}
	case InvitationBatchInstitutionRoleCreate:
		value, err := s.institutions.Get(ctx, row.ScopeID)
		if err != nil {
			return invitationError(err)
		}
		if value.Revision != row.TargetRevision || value.IsArchived() {
			return NewError("invitation.conflict")
		}
		role, err := s.roles.Get(ctx, row.RoleID.String())
		if err != nil {
			return invitationError(err)
		}
		if role.UpdatedAt.UnixMicro() != row.RoleRevision || role.IsArchived() {
			return NewError("invitation.conflict")
		}
	default:
		return NewError("request.invalid")
	}
	return nil
}
