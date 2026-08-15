// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type Call struct {
	principal model.Principal
	metadata  model.RequestMetadata
}

func NewCall(principal model.Principal, metadata model.RequestMetadata) Call {
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return Call{principal: principal, metadata: metadata}
}

func (call Call) Principal() model.Principal {
	principal := call.principal
	principal.CredentialScopes = append([]string(nil), principal.CredentialScopes...)
	return principal
}

func (call Call) RequestMetadata() model.RequestMetadata { return call.metadata }

type Fault struct {
	Code       string
	SafeFields map[string]any
	Cause      error
}

type Sittings interface {
	// Resolve returns stable Sitting ownership metadata. Store remains the
	// authority for connect-time lifecycle, deadline, and membership checks.
	Resolve(context.Context, model.ExamSittingID) (*store.ExamSittingSnapshot, error)
}

func (fault *Fault) Error() string {
	if fault == nil {
		return "Exam Attempt fault"
	}
	return fault.Code
}

func (fault *Fault) Unwrap() error {
	if fault == nil {
		return nil
	}
	return fault.Cause
}

type ManagerAuthorizer interface {
	AuthorizeSittingView(context.Context, Call, model.ExamSittingID) error
}

type Auditor interface {
	Begin(context.Context, Call, model.Action, model.Resource, model.RoleScopeType, string, string, map[string]any) (string, error)
	Fail(context.Context, string, string) error
}

type Effects interface {
	ConnectionOpened(context.Context, ConnectionResult) error
	ConnectionClosed(context.Context, ConnectionClosedResult) error
}

type EffectFailures interface {
	Report(context.Context, string, error)
}

type Content interface {
	OpenExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error)
	OpenStarterWorkspaceObject(context.Context, model.StarterWorkspaceObjectID) (io.ReadCloser, error)
}

type Dependencies struct {
	Persistence      store.ExamAttemptStore
	Sittings         Sittings
	Managers         ManagerAuthorizer
	Auditor          Auditor
	Effects          Effects
	EffectFailures   EffectFailures
	Content          Content
	Now              func() time.Time
	NewAttemptID     func() model.ExamAttemptID
	NewWorkspaceID   func() model.ExamAttemptWorkspaceID
	NewParticipation func() model.AttemptParticipationID
	NewConnection    func() model.AttemptConnectionID
}

type Service struct{ deps Dependencies }

func New(deps Dependencies) (*Service, error) {
	if deps.Persistence == nil || deps.Sittings == nil || deps.Managers == nil || deps.Auditor == nil || deps.Effects == nil ||
		deps.EffectFailures == nil || deps.Content == nil || deps.Now == nil || deps.NewAttemptID == nil ||
		deps.NewWorkspaceID == nil || deps.NewParticipation == nil || deps.NewConnection == nil {
		return nil, errors.New("Exam Attempt dependencies are required")
	}
	return &Service{deps: deps}, nil
}

type GetManagedAttemptQuery struct {
	ExamID    model.ExamID
	SittingID model.ExamSittingID
	AttemptID model.ExamAttemptID
}

func (service *Service) GetManaged(ctx context.Context, call Call, query GetManagedAttemptQuery) (*store.ExamAttemptManagerSnapshot, error) {
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || !query.AttemptID.IsValid() {
		return nil, invalid("attempt")
	}
	if err := service.deps.Managers.AuthorizeSittingView(ctx, call, query.SittingID); err != nil {
		return nil, err
	}
	snapshot, err := service.deps.Persistence.Get(ctx, query.ExamID, query.AttemptID)
	if err != nil {
		return nil, mapStore(err)
	}
	if snapshot == nil || snapshot.Attempt == nil || snapshot.Attempt.Validate() != nil ||
		snapshot.Attempt.ExamID != query.ExamID || snapshot.Attempt.SittingID != query.SittingID {
		return nil, unavailable(errors.New("inconsistent managed Attempt projection"))
	}
	return cloneManagerSnapshot(snapshot), nil
}

type ListManagedAttemptsQuery struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	States          []model.ExamAttemptState
	BeforeCreatedAt time.Time
	BeforeAttemptID model.ExamAttemptID
	Limit           int
}

type ManagedAttemptPage struct {
	Items   []store.ExamAttemptManagerSnapshot
	HasMore bool
}

func (service *Service) ListManaged(ctx context.Context, call Call, query ListManagedAttemptsQuery) (ManagedAttemptPage, error) {
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeCreatedAt.IsZero() != query.BeforeAttemptID.IsZero()) {
		return ManagedAttemptPage{}, invalid("attempt_list")
	}
	if err := service.deps.Managers.AuthorizeSittingView(ctx, call, query.SittingID); err != nil {
		return ManagedAttemptPage{}, err
	}
	rows, err := service.deps.Persistence.List(ctx, store.ExamAttemptManagerListOptions{
		ExamID: query.ExamID, SittingID: query.SittingID, States: append([]model.ExamAttemptState(nil), query.States...),
		BeforeCreatedAt: model.TimeUTC(query.BeforeCreatedAt), BeforeAttemptID: query.BeforeAttemptID, Limit: query.Limit + 1,
	})
	if err != nil {
		return ManagedAttemptPage{}, mapStore(err)
	}
	page := ManagedAttemptPage{Items: make([]store.ExamAttemptManagerSnapshot, 0, min(len(rows), query.Limit)), HasMore: len(rows) > query.Limit}
	for index, row := range rows {
		if index == query.Limit {
			break
		}
		if row.Attempt == nil || row.Attempt.Validate() != nil || row.Attempt.ExamID != query.ExamID || row.Attempt.SittingID != query.SittingID {
			return ManagedAttemptPage{}, unavailable(errors.New("inconsistent managed Attempt page"))
		}
		page.Items = append(page.Items, *cloneManagerSnapshot(&row))
	}
	return page, nil
}

func cloneManagerSnapshot(snapshot *store.ExamAttemptManagerSnapshot) *store.ExamAttemptManagerSnapshot {
	if snapshot == nil {
		return nil
	}
	clone := *snapshot
	if snapshot.Attempt != nil {
		value := *snapshot.Attempt
		clone.Attempt = &value
	}
	if snapshot.Workspace != nil {
		value := *snapshot.Workspace
		clone.Workspace = &value
	}
	if snapshot.LatestParticipation != nil {
		value := *snapshot.LatestParticipation
		clone.LatestParticipation = &value
	}
	if snapshot.CurrentConnection != nil {
		value := *snapshot.CurrentConnection
		clone.CurrentConnection = &value
	}
	return &clone
}

type ConnectCommand struct {
	SittingID            model.ExamSittingID
	ContinuityCredential string
	Idempotency          *store.CommandIdempotency
}

type ConnectionResult struct {
	Attempt          model.ExamAttempt
	Workspace        model.ExamAttemptWorkspace
	Participation    store.ExamAttemptParticipationView
	Connection       model.AttemptConnection
	ClassID          model.ClassID
	FirstAdmission   bool
	ConnectionOpened bool
	Replayed         bool
}

func (service *Service) Connect(ctx context.Context, call Call, command ConnectCommand) (ConnectionResult, error) {
	principal := call.Principal()
	if command.Idempotency == nil {
		return ConnectionResult{}, &Fault{Code: "idempotency.key_required"}
	}
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return ConnectionResult{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !command.SittingID.IsValid() {
		return ConnectionResult{}, invalid("exam_sitting_id")
	}
	if !model.IsValidCredentialToken(command.ContinuityCredential) {
		return ConnectionResult{}, invalid("continuity_credential")
	}
	snapshot, err := service.deps.Sittings.Resolve(ctx, command.SittingID)
	if err != nil {
		return ConnectionResult{}, mapStore(err)
	}
	if snapshot == nil || snapshot.Sitting == nil || snapshot.Sitting.ID != command.SittingID ||
		!snapshot.Sitting.ExamID.IsValid() || !snapshot.Sitting.ClassID.IsValid() {
		return ConnectionResult{}, unavailable(errors.New("incomplete Sitting ownership projection"))
	}
	sitting := snapshot.Sitting

	attemptID := service.deps.NewAttemptID()
	at := model.TimeUTC(service.deps.Now())
	workspaceID := service.deps.NewWorkspaceID()
	credentialHash := model.HashToken(command.ContinuityCredential)
	participationID, connectionID := service.deps.NewParticipation(), service.deps.NewConnection()

	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, sitting.ClassID.String(),
		"exam_attempt_connect", map[string]any{"exam_sitting_id": command.SittingID.String()})
	if err != nil {
		return ConnectionResult{}, err
	}
	stored, err := service.deps.Persistence.Connect(ctx, &store.ExamAttemptConnect{
		SittingID: command.SittingID, CandidateUserID: principal.UserID, SessionID: principal.SessionID,
		AttemptID: attemptID, WorkspaceID: workspaceID, ParticipationID: participationID, ConnectionID: connectionID,
		ContinuityCredentialHash: credentialHash,
		AuditEventID:             auditID, AuditAt: model.MillisFromTime(at),
	}, command.Idempotency)
	if err != nil {
		return ConnectionResult{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectConnection(stored, sitting.ClassID, principal.UserID, principal.SessionID, command.SittingID)
	if err != nil {
		return ConnectionResult{}, err
	}
	if result.ConnectionOpened && !result.Replayed {
		if effectErr := service.deps.Effects.ConnectionOpened(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_connection_opened", effectErr)
		}
	}
	return result, nil
}

type CloseConnectionCommand struct {
	AttemptID    model.ExamAttemptID
	SittingID    model.ExamSittingID
	ClassID      model.ClassID
	ConnectionID model.AttemptConnectionID
	Reason       model.AttemptConnectionCloseReason
}

type ConnectionClosedResult struct {
	AttemptID       model.ExamAttemptID
	SittingID       model.ExamSittingID
	CandidateUserID model.UserID
	Connection      model.AttemptConnection
	Changed         bool
}

func (service *Service) CloseConnection(ctx context.Context, call Call, command CloseConnectionCommand) (ConnectionClosedResult, error) {
	if call.Principal().Validate() != nil || !command.AttemptID.IsValid() || !command.SittingID.IsValid() ||
		!command.ClassID.IsValid() || !command.ConnectionID.IsValid() || !command.Reason.IsValid() {
		return ConnectionClosedResult{}, invalid("connection")
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: command.SittingID.String()}, model.RoleScopeClass, command.ClassID.String(),
		"exam_attempt_connection_close", map[string]any{"exam_attempt_id": command.AttemptID.String(), "attempt_connection_id": command.ConnectionID.String()})
	if err != nil {
		return ConnectionClosedResult{}, err
	}
	stored, err := service.deps.Persistence.CloseConnection(ctx, &store.ExamAttemptConnectionClose{
		ConnectionID: command.ConnectionID, CandidateUserID: call.Principal().UserID, SessionID: call.Principal().SessionID,
		Reason: command.Reason, AuditEventID: auditID,
		AuditAt: model.MillisFromTime(model.TimeUTC(service.deps.Now())),
	})
	if err != nil {
		return ConnectionClosedResult{}, service.failAudit(ctx, auditID, err)
	}
	if stored == nil || stored.Connection == nil || stored.AttemptID != command.AttemptID || stored.SittingID != command.SittingID ||
		stored.Connection.ID != command.ConnectionID || stored.Connection.AttemptID != command.AttemptID || stored.Connection.Validate() != nil {
		return ConnectionClosedResult{}, unavailable(errors.New("inconsistent Connection close outcome"))
	}
	result := ConnectionClosedResult{AttemptID: stored.AttemptID, SittingID: stored.SittingID,
		CandidateUserID: stored.CandidateUserID, Connection: *stored.Connection, Changed: stored.Changed}
	if result.Changed {
		if effectErr := service.deps.Effects.ConnectionClosed(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_connection_closed", effectErr)
		}
	}
	return result, nil
}

type CandidateAccess struct {
	AttemptID            model.ExamAttemptID
	ConnectionID         model.AttemptConnectionID
	ContinuityCredential string
}

type Presentation struct {
	AttemptID            model.ExamAttemptID
	SittingID            model.ExamSittingID
	AdmissionRevisionID  model.ExamRevisionID
	CurrentRevisionID    model.ExamRevisionID
	Title                string
	InstructionsMarkdown string
	Resources            []Resource
}

type Resource struct {
	ResourceID          model.ExamResourceID
	DisplayName         string
	DescriptionMarkdown string
	Position            int
	MediaType           model.ExamResourceMediaType
	SizeBytes           int64
	SHA256              string
}

func (service *Service) GetPresentation(ctx context.Context, call Call, access CandidateAccess) (Presentation, error) {
	selector, err := candidateSelector(call, access)
	if err != nil {
		return Presentation{}, err
	}
	stored, err := service.deps.Persistence.GetCandidatePresentation(ctx, selector)
	if err != nil {
		return Presentation{}, mapStore(err)
	}
	if stored == nil {
		return Presentation{}, unavailable(errors.New("missing candidate presentation"))
	}
	result := Presentation{AttemptID: stored.AttemptID, SittingID: stored.SittingID,
		AdmissionRevisionID: stored.AdmissionRevisionID, CurrentRevisionID: stored.CurrentRevisionID,
		Title: stored.Title, InstructionsMarkdown: sanitizeCandidateMarkdown(stored.InstructionsMarkdown), Resources: make([]Resource, len(stored.Resources))}
	for index, item := range stored.Resources {
		result.Resources[index] = Resource{ResourceID: item.ResourceID, DisplayName: item.DisplayName,
			DescriptionMarkdown: sanitizeCandidateMarkdown(item.DescriptionMarkdown), Position: item.Position, MediaType: item.MediaType,
			SizeBytes: item.SizeBytes, SHA256: item.SHA256}
	}
	return result, nil
}

type WorkspaceQuery struct {
	Access       CandidateAccess
	AfterPath    string
	AfterEntryID model.AttemptWorkspaceEntryID
	Limit        int
}

type WorkspacePage struct {
	Items   []store.CandidateAttemptWorkspaceItem
	HasMore bool
}

func (service *Service) ListWorkspace(ctx context.Context, call Call, query WorkspaceQuery) (WorkspacePage, error) {
	selector, err := candidateSelector(call, query.Access)
	if err != nil {
		return WorkspacePage{}, err
	}
	if query.Limit < 1 || query.Limit > 200 || (query.AfterPath == "") != query.AfterEntryID.IsZero() {
		return WorkspacePage{}, invalid("workspace_list")
	}
	if query.AfterPath != "" {
		normalized, normalizeErr := model.NormalizeStarterWorkspacePath(query.AfterPath)
		if normalizeErr != nil || normalized != query.AfterPath {
			return WorkspacePage{}, invalidCause("after_path", normalizeErr)
		}
	}
	page, err := service.deps.Persistence.ListCandidateWorkspace(ctx, store.CandidateWorkspaceListOptions{
		Access: selector, AfterPath: query.AfterPath, AfterEntryID: query.AfterEntryID, Limit: query.Limit,
	})
	if err != nil {
		return WorkspacePage{}, mapStore(err)
	}
	if page == nil {
		return WorkspacePage{}, unavailable(errors.New("missing candidate Workspace page"))
	}
	return WorkspacePage{Items: append([]store.CandidateAttemptWorkspaceItem(nil), page.Items...), HasMore: page.HasMore}, nil
}

type OpenedContent struct {
	Body           io.ReadCloser
	MediaType      string
	SizeBytes      int64
	SHA256         string
	ContentVersion model.WorkspaceContentVersion
}

func (service *Service) OpenResource(ctx context.Context, call Call, access CandidateAccess, resourceID model.ExamResourceID) (*OpenedContent, error) {
	selector, err := candidateSelector(call, access)
	if err != nil {
		return nil, err
	}
	if !resourceID.IsValid() {
		return nil, invalid("exam_resource_id")
	}
	resolved, err := service.deps.Persistence.ResolveCandidateResource(ctx, selector, resourceID)
	if err != nil {
		return nil, mapStore(err)
	}
	if resolved == nil || resolved.Resource.ResourceID != resourceID || !resolved.FileRevisionID.IsValid() || !resolved.RenditionID.IsValid() {
		return nil, unavailable(errors.New("incomplete candidate Resource selector"))
	}
	body, err := service.deps.Content.OpenExamResource(ctx, resolved.FileRevisionID, resolved.RenditionID)
	if err != nil {
		return nil, unavailable(err)
	}
	return &OpenedContent{Body: body, MediaType: string(resolved.Resource.MediaType), SizeBytes: resolved.Resource.SizeBytes, SHA256: resolved.Resource.SHA256}, nil
}

func (service *Service) OpenWorkspaceFile(ctx context.Context, call Call, access CandidateAccess, entryID model.AttemptWorkspaceEntryID) (*OpenedContent, error) {
	selector, err := candidateSelector(call, access)
	if err != nil {
		return nil, err
	}
	if !entryID.IsValid() {
		return nil, invalid("attempt_workspace_entry_id")
	}
	resolved, err := service.deps.Persistence.ResolveCandidateWorkspaceFile(ctx, selector, entryID)
	if err != nil {
		return nil, mapStore(err)
	}
	if resolved == nil || resolved.Entry.EntryID != entryID || resolved.ContentVersion.IsZero() {
		return nil, unavailable(errors.New("incomplete candidate Workspace selector"))
	}
	var body io.ReadCloser
	switch resolved.StorageOrigin {
	case model.AttemptWorkspaceStorageStarter:
		if !resolved.StarterObjectID.IsValid() || !resolved.AttemptObjectID.IsValid() {
			return nil, unavailable(errors.New("inconsistent starter Workspace selector"))
		}
		body, err = service.deps.Content.OpenStarterWorkspaceObject(ctx, resolved.StarterObjectID)
	case model.AttemptWorkspaceStorageAttempt:
		return nil, unavailable(errors.New("Attempt-origin Workspace content is not implemented"))
	default:
		return nil, unavailable(errors.New("invalid candidate Workspace storage origin"))
	}
	if err != nil {
		return nil, unavailable(err)
	}
	return &OpenedContent{Body: body, MediaType: resolved.Entry.MediaType, SizeBytes: resolved.Entry.SizeBytes,
		SHA256: resolved.Entry.SHA256, ContentVersion: resolved.ContentVersion}, nil
}

func candidateSelector(call Call, access CandidateAccess) (store.CandidateAttemptAccess, error) {
	principal := call.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return store.CandidateAttemptAccess{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !access.AttemptID.IsValid() || !access.ConnectionID.IsValid() || !model.IsValidCredentialToken(access.ContinuityCredential) {
		return store.CandidateAttemptAccess{}, invalid("candidate_access")
	}
	return store.CandidateAttemptAccess{AttemptID: access.AttemptID, CandidateUserID: principal.UserID,
		SessionID: principal.SessionID, ConnectionID: access.ConnectionID, ContinuityCredentialHash: model.HashToken(access.ContinuityCredential)}, nil
}

func projectConnection(stored *store.ExamAttemptConnectResult, classID model.ClassID, candidateID model.UserID,
	sessionID model.SessionID, sittingID model.ExamSittingID,
) (ConnectionResult, error) {
	if stored == nil || stored.Attempt == nil || stored.Workspace == nil || stored.Participation == nil || stored.Connection == nil ||
		stored.Attempt.Validate() != nil || stored.Workspace.Validate() != nil || !validParticipationView(stored.Participation) || stored.Connection.Validate() != nil ||
		stored.Attempt.CandidateUserID != candidateID || stored.Attempt.SittingID != sittingID || stored.Workspace.AttemptID != stored.Attempt.ID ||
		stored.ClassID != classID || stored.Participation.AttemptID != stored.Attempt.ID ||
		stored.Connection.AttemptID != stored.Attempt.ID || stored.Connection.ParticipationID != stored.Participation.ID ||
		stored.Connection.SessionID != sessionID {
		return ConnectionResult{}, unavailable(errors.New("inconsistent Exam Attempt connection outcome"))
	}
	if stored.Attempt.State != model.ExamAttemptActive || stored.Participation.State != model.AttemptParticipationActive ||
		stored.Connection.State != model.AttemptConnectionOpen {
		return ConnectionResult{}, &Fault{Code: "exam.attempt.connection_closed"}
	}
	return ConnectionResult{Attempt: *stored.Attempt, Workspace: *stored.Workspace, Participation: *stored.Participation,
		Connection: *stored.Connection, ClassID: classID, FirstAdmission: stored.FirstAdmission,
		ConnectionOpened: stored.ConnectionOpened, Replayed: stored.Replayed}, nil
}

func validParticipationView(view *store.ExamAttemptParticipationView) bool {
	if view == nil || !view.ID.IsValid() || !view.AttemptID.IsValid() || view.Generation < 1 || view.RenewalSequence < 0 ||
		view.StartedAt.IsZero() || view.UpdatedAt.IsZero() || view.UpdatedAt.Before(view.StartedAt) ||
		view.LeaseExpiresAt.IsZero() || view.LeaseExpiresAt.Before(view.StartedAt) {
		return false
	}
	switch view.State {
	case model.AttemptParticipationActive:
		return !view.EndedAt.Valid && view.EndReason == ""
	case model.AttemptParticipationEnded:
		return view.EndedAt.Valid && !view.EndedAt.Time.IsZero() && !view.EndedAt.Time.Before(view.StartedAt) &&
			!view.EndedAt.Time.After(view.UpdatedAt) && view.EndReason != ""
	default:
		return false
	}
}

func (service *Service) failAudit(ctx context.Context, auditID string, err error) error {
	mapped := mapStore(err)
	code := "exam.attempt.unavailable"
	var fault *Fault
	if errors.As(mapped, &fault) {
		code = fault.Code
	}
	if auditErr := service.deps.Auditor.Fail(ctx, auditID, code); auditErr != nil {
		return auditErr
	}
	return mapped
}

func invalid(field string) error {
	return &Fault{Code: "exam.attempt.invalid", SafeFields: map[string]any{"field": field}}
}

func invalidCause(field string, cause error) error {
	return &Fault{Code: "exam.attempt.invalid", SafeFields: map[string]any{"field": field}, Cause: cause}
}

func unavailable(cause error) error { return &Fault{Code: "exam.attempt.unavailable", Cause: cause} }

func mapStore(err error) error {
	var idempotencyConflict *store.ErrIdempotencyConflict
	var idempotencyInProgress *store.ErrIdempotencyInProgress
	var invalidInput *store.ErrInvalidInput
	var conflict *store.ErrConflict
	switch {
	case errors.As(err, &idempotencyConflict):
		return &Fault{Code: "idempotency.conflict", Cause: err}
	case errors.As(err, &idempotencyInProgress):
		return &Fault{Code: "idempotency.in_progress", Cause: err}
	case store.IsNotFound(err):
		return &Fault{Code: "exam.attempt.not_found", Cause: err}
	case errors.As(err, &invalidInput):
		return invalidCause("value", err)
	case errors.As(err, &conflict):
		code := mapConflict(conflict.Constraint)
		return &Fault{Code: code, Cause: err}
	default:
		return unavailable(err)
	}
}

func mapConflict(constraint string) string {
	switch constraint {
	case "exam_attempt_membership":
		return "exam.attempt.not_found"
	case "exam_sitting_state", "exam_sitting_deadline_reached":
		return "exam.attempt.sitting_unavailable"
	case "exam_attempt_state":
		return "exam.attempt.state_conflict"
	case "attempt_participation_credential":
		return "exam.attempt.continuity_invalid"
	case "attempt_connection_open":
		return "exam.attempt.already_connected"
	case "attempt_participation_expired", "attempt_connection_closed":
		return "exam.attempt.connection_closed"
	default:
		return "exam.attempt.conflict"
	}
}
