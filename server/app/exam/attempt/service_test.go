// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package attempt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestConnectDelegatesAtomicAdmissionAndPassesOnlyCredentialHash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	result, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: credential, Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempt.AdmissionRevisionID != f.revision.ID || result.ClassID != f.sitting.ClassID ||
		!result.FirstAdmission || !result.ConnectionOpened || f.effects.opened != 1 {
		t.Fatalf("connection result = %#v", result)
	}
	input := f.persistence.connect
	if input == nil || input.ContinuityCredentialHash != model.HashToken(credential) ||
		strings.Contains(input.ContinuityCredentialHash, credential) {
		t.Fatalf("persistence credential = %#v", input)
	}
	if got := strings.Join(f.order, ","); got != "sitting,audit,connect,effect.open" {
		t.Fatalf("order = %s", got)
	}
	if strings.Contains(strings.ToLower(fmt.Sprintf("%#v", result.Participation)), "credential") ||
		strings.Contains(fmt.Sprintf("%#v", result.Participation), credential) ||
		strings.Contains(fmt.Sprintf("%#v", result.Participation), model.HashToken(credential)) {
		t.Fatalf("credential leaked through connection result: %#v", result.Participation)
	}
	if f.audit.values["continuity_credential"] != nil || f.audit.values["credential_hash"] != nil {
		t.Fatalf("credential entered audit = %#v", f.audit.values)
	}
}

func TestConnectRejectsMalformedCredentialAndPATBeforeReads(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*fixture){
		func(f *fixture) { f.call = NewCall(model.Principal{UserID: f.userID}, model.RequestMetadata{}) },
		func(f *fixture) {
			principal := f.call.Principal()
			principal.CredentialType = model.CredentialPersonalAccessToken
			principal.SessionID = ""
			principal.AuthenticationStrength = ""
			principal.AuthenticatedAt = time.Time{}
			principal.ClientType = model.SessionClientCLI
			principal.CredentialScopes = []string{string(model.ActionExamSittingView)}
			f.call = NewCall(principal, model.RequestMetadata{})
		},
	} {
		f := newFixture(t)
		mutate(f)
		_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
			SittingID: f.sitting.ID, ContinuityCredential: "not-canonical", Idempotency: &store.CommandIdempotency{},
		})
		if err == nil || len(f.order) != 0 {
			t.Fatalf("error=%v order=%v", err, f.order)
		}
	}
}

func TestConnectReplayAfterCorrectionAndPauseSuppressesTransientOpenEvent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.connectionOpened = false
	f.sitting.State = model.ExamSittingPaused
	f.sitting.ExamRevisionID = model.NewExamRevisionID()
	result, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	if err != nil || !result.Replayed || f.effects.opened != 0 || strings.Join(f.order, ",") != "sitting,audit,connect" {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestReconnectThatOpensANewConnectionPublishesOneOpenEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.firstAdmission = false
	result, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	if err != nil || result.FirstAdmission || !result.ConnectionOpened || result.Replayed || f.effects.opened != 1 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestDifferentKeyConvergenceOnExistingOpenConnectionPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.firstAdmission = false
	f.persistence.connectionOpened = false
	result, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	if err != nil || result.FirstAdmission || result.ConnectionOpened || result.Replayed || f.effects.opened != 0 {
		t.Fatalf("result=%#v error=%v effects=%#v", result, err, f.effects)
	}
}

func TestConnectReplayOfClosedConnectionRequiresNewKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.connectClosed = true
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_closed" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectReplayOfEndedParticipationRequiresNewKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.replayed = true
	f.persistence.participationEnded = true
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_closed" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectRejectsAConnectionOutcomeBoundToAnotherSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectionSession = model.NewSessionID()
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.unavailable" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestConnectFailureCompletesAuditAndPublishesNoEffect(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectErr = store.NewErrConflict("exam_sitting", "exam_sitting_state", nil)
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.sitting_unavailable" ||
		f.audit.failedCode != fault.Code || f.effects.opened != 0 || strings.Join(f.order, ",") != "sitting,audit,connect,audit.fail" {
		t.Fatalf("error=%v audit=%q effects=%#v order=%v", err, f.audit.failedCode, f.effects, f.order)
	}
}

func TestExpiredReplayRequiresANewConnectionKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.connectErr = store.NewErrConflict("attempt_participation", "attempt_participation_expired", nil)
	_, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.connection_closed" || f.effects.opened != 0 {
		t.Fatalf("error=%v effects=%#v", err, f.effects)
	}
}

func TestProtectedPresentationUsesCurrentRevisionAndSanitizesCandidateMarkdown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	currentID := model.NewExamRevisionID()
	f.persistence.presentation = &store.CandidateExamPresentation{
		AttemptID: f.attemptID, SittingID: f.sitting.ID, AdmissionRevisionID: f.revision.ID, CurrentRevisionID: currentID,
		Title: "Algorithms", InstructionsMarkdown: "# Rules\nUse **Go**.\n<script>alert('x')</script>\n[bad](javascript:alert(1))\n![tracker](https://example.test/pixel.png)\n[handbook](https://example.test/handbook)",
		Resources: []store.CandidateExamResource{{ResourceID: model.NewExamResourceID(), DisplayName: "Reference",
			DescriptionMarkdown: "Read _carefully_. <iframe src=https://evil.test></iframe> [data](data:text/html,bad)", Position: 0, MediaType: model.ExamResourceMediaText, SizeBytes: 4, SHA256: strings.Repeat("a", 64)}},
	}
	result, err := f.service.GetPresentation(context.Background(), f.call, CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: credential,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AdmissionRevisionID != f.revision.ID || result.CurrentRevisionID != currentID ||
		!strings.Contains(result.InstructionsMarkdown, "# Rules") || !strings.Contains(result.InstructionsMarkdown, "**Go**") ||
		!strings.Contains(result.InstructionsMarkdown, "[handbook](https://example.test/handbook)") ||
		!strings.Contains(result.Resources[0].DescriptionMarkdown, "_carefully_") {
		t.Fatalf("presentation = %#v", result)
	}
	for _, forbidden := range []string{"<script", "alert('x')", "javascript:", "![tracker]", "pixel.png", "<iframe", "evil.test", "data:text/html"} {
		if strings.Contains(result.InstructionsMarkdown+result.Resources[0].DescriptionMarkdown, forbidden) {
			t.Fatalf("unsafe Markdown survived %q: %#v", forbidden, result)
		}
	}
	if f.persistence.candidateAccess.ContinuityCredentialHash != model.HashToken(credential) ||
		f.persistence.candidateAccess.CandidateUserID != f.userID || f.persistence.candidateAccess.SessionID != f.call.Principal().SessionID {
		t.Fatalf("candidate selector = %#v", f.persistence.candidateAccess)
	}
}

func TestStarterOriginWorkspaceFileOpensPinnedStarterBytesWithAttemptObjectIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	entryID := model.NewAttemptWorkspaceEntryID()
	starterID := model.NewStarterWorkspaceObjectID()
	attemptObjectID := model.NewAttemptWorkspaceObjectID()
	version := model.NewWorkspaceContentVersion()
	f.persistence.workspaceContent = &store.CandidateWorkspaceContent{
		Entry: store.CandidateAttemptWorkspaceItem{EntryID: entryID, Kind: model.StarterWorkspaceEntryFile,
			Path: "cmd/main.go", MediaType: "text/x-go", SizeBytes: 4, SHA256: strings.Repeat("a", 64)},
		StorageOrigin: model.AttemptWorkspaceStorageStarter, StarterObjectID: starterID,
		AttemptObjectID: attemptObjectID, ContentVersion: version,
	}
	opened, err := f.service.OpenWorkspaceFile(context.Background(), f.call, CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken(),
	}, entryID)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Body.Close()
	if f.content.starterID != starterID || opened.ContentVersion != version || opened.MediaType != "text/x-go" {
		t.Fatalf("opened=%#v starter=%s", opened, f.content.starterID)
	}
}

func TestProtectedPresentationRejectsAConnectionFromAnotherSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.persistence.authorizedSession = f.call.Principal().SessionID
	principal := f.call.Principal()
	principal.SessionID = model.NewSessionID()
	otherSession := NewCall(principal, f.call.RequestMetadata())
	_, err := f.service.GetPresentation(context.Background(), otherSession, CandidateAccess{
		AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken(),
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.not_found" ||
		f.persistence.candidateAccess.SessionID != principal.SessionID {
		t.Fatalf("error=%v access=%#v", err, f.persistence.candidateAccess)
	}
}

func TestContentSelectorsPreserveCandidateAccessErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	invalidCall := NewCall(model.Principal{}, model.RequestMetadata{})
	access := CandidateAccess{AttemptID: f.attemptID, ConnectionID: f.connectionID, ContinuityCredential: model.NewCredentialToken()}
	for _, open := range []func() error{
		func() error {
			_, err := f.service.OpenResource(context.Background(), invalidCall, access, model.NewExamResourceID())
			return err
		},
		func() error {
			_, err := f.service.OpenWorkspaceFile(context.Background(), invalidCall, access, model.NewAttemptWorkspaceEntryID())
			return err
		},
	} {
		var fault *Fault
		if err := open(); !errors.As(err, &fault) || fault.Code != "authentication.invalid_token" {
			t.Fatalf("error=%v", err)
		}
	}
}

func TestWorkspaceListRejectsUnboundedOrIncompleteCursorsBeforeStore(t *testing.T) {
	t.Parallel()
	access := CandidateAccess{ContinuityCredential: model.NewCredentialToken()}
	for _, query := range []WorkspaceQuery{
		{Access: access, Limit: 0},
		{Access: access, Limit: 201},
		{Access: access, AfterPath: "cmd/main.go", Limit: 20},
		{Access: access, AfterEntryID: model.NewAttemptWorkspaceEntryID(), Limit: 20},
		{Access: access, AfterPath: "cmd/../main.go", AfterEntryID: model.NewAttemptWorkspaceEntryID(), Limit: 20},
	} {
		f := newFixture(t)
		query.Access.AttemptID, query.Access.ConnectionID = f.attemptID, f.connectionID
		_, err := f.service.ListWorkspace(context.Background(), f.call, query)
		var fault *Fault
		if !errors.As(err, &fault) || fault.Code != "exam.attempt.invalid" || f.persistence.workspaceLists != 0 {
			t.Fatalf("query=%#v error=%v calls=%d", query, err, f.persistence.workspaceLists)
		}
	}
}

func TestManagerListAuthorizesBeforeBoundedStoreRead(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	rows := make([]store.ExamAttemptManagerSnapshot, 3)
	for index := range rows {
		attempt, err := model.NewExamAttempt(model.NewExamAttemptID(), f.sitting.ExamID, f.sitting.ID, model.NewUserID(), f.revision.ID, f.at.Add(time.Duration(index)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		rows[index].Attempt = attempt
	}
	f.persistence.managerRows = rows
	page, err := f.service.ListManaged(context.Background(), f.call, ListManagedAttemptsQuery{
		ExamID: f.sitting.ExamID, SittingID: f.sitting.ID, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || !page.HasMore || f.persistence.managerOptions.Limit != 3 ||
		strings.Join(f.order, ",") != "manager.authorize,manager.list" {
		t.Fatalf("page=%#v options=%#v order=%v", page, f.persistence.managerOptions, f.order)
	}
}

func TestTrustedCloseCommitsBeforeManagerEffectAndSuppressesNoop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	credential := model.NewCredentialToken()
	connected, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
		SittingID: f.sitting.ID, ContinuityCredential: credential, Idempotency: &store.CommandIdempotency{},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.order = nil
	f.persistence.closeChanged = true
	closed, err := f.service.CloseConnection(context.Background(), f.call, CloseConnectionCommand{
		AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
	})
	if err != nil || !closed.Changed || strings.Join(f.order, ",") != "audit,close,effect.close" {
		t.Fatalf("closed=%#v error=%v order=%v", closed, err, f.order)
	}
	f.order = nil
	f.persistence.closeChanged = false
	if _, err = f.service.CloseConnection(context.Background(), f.call, CloseConnectionCommand{
		AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
	}); err != nil {
		t.Fatal(err)
	}
	if f.effects.closed != 1 {
		t.Fatalf("close effects = %d", f.effects.closed)
	}
}

func TestTrustedCloseStillFencesCandidateAndSessionOwnership(t *testing.T) {
	t.Parallel()
	for _, mutate := range []func(*model.Principal){
		func(principal *model.Principal) { principal.UserID = model.NewUserID() },
		func(principal *model.Principal) { principal.SessionID = model.NewSessionID() },
	} {
		f := newFixture(t)
		connected, err := f.service.Connect(context.Background(), f.call, ConnectCommand{
			SittingID: f.sitting.ID, ContinuityCredential: model.NewCredentialToken(), Idempotency: &store.CommandIdempotency{},
		})
		if err != nil {
			t.Fatal(err)
		}
		principal := f.call.Principal()
		mutate(&principal)
		_, err = f.service.CloseConnection(context.Background(), NewCall(principal, f.call.RequestMetadata()), CloseConnectionCommand{
			AttemptID: connected.Attempt.ID, SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
			ConnectionID: connected.Connection.ID, Reason: model.AttemptConnectionCloseTransport,
		})
		var fault *Fault
		if !errors.As(err, &fault) || fault.Code != "exam.attempt.not_found" || f.effects.closed != 0 {
			t.Fatalf("error=%v effects=%#v", err, f.effects)
		}
	}
}

func TestTrustedCloseRejectsUnknownReasonBeforeAudit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.service.CloseConnection(context.Background(), f.call, CloseConnectionCommand{
		AttemptID: model.NewExamAttemptID(), SittingID: f.sitting.ID, ClassID: f.sitting.ClassID,
		ConnectionID: model.NewAttemptConnectionID(), Reason: "unknown",
	})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.attempt.invalid" || len(f.order) != 0 {
		t.Fatalf("error=%v order=%v", err, f.order)
	}
}

type fixture struct {
	service      *Service
	call         Call
	userID       model.UserID
	attemptID    model.ExamAttemptID
	connectionID model.AttemptConnectionID
	at           time.Time
	sitting      *model.ExamSitting
	revision     *model.ExamRevision
	order        []string
	persistence  *attemptStoreFake
	audit        *auditFake
	effects      *effectsFake
	content      *contentFake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{userID: model.NewUserID(), attemptID: model.NewExamAttemptID(), connectionID: model.NewAttemptConnectionID(),
		at: time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)}
	f.sitting = &model.ExamSitting{ID: model.NewExamSittingID(), ExamID: model.NewExamID(), ExamRevisionID: model.NewExamRevisionID(),
		ClassID: model.NewClassID(), State: model.ExamSittingOpen, ScheduledStartAt: f.at.Add(-time.Hour), ScheduledEndAt: f.at.Add(time.Hour),
		CreatedAt: f.at.Add(-2 * time.Hour), UpdatedAt: f.at, Revision: 2}
	f.revision = &model.ExamRevision{ID: f.sitting.ExamRevisionID, ExamID: f.sitting.ExamID, StarterWorkspace: []model.ExamRevisionStarterWorkspaceEntry{
		{EntryID: model.NewStarterWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryDirectory, Path: "cmd"},
		{EntryID: model.NewStarterWorkspaceEntryID(), Kind: model.StarterWorkspaceEntryFile, Path: "cmd/main.go", ObjectID: model.NewStarterWorkspaceObjectID(),
			ContentVersion: model.NewWorkspaceContentVersion(), MediaType: "text/x-go", SizeBytes: 4, SHA256: strings.Repeat("a", 64)},
	}}
	f.persistence = &attemptStoreFake{f: f, firstAdmission: true, connectionOpened: true}
	f.audit = &auditFake{f: f}
	f.effects = &effectsFake{f: f}
	f.content = &contentFake{}
	service, err := New(Dependencies{
		Persistence: f.persistence, Sittings: &sittingFake{f: f}, Managers: &managerFake{f: f},
		Auditor: f.audit, Effects: f.effects, EffectFailures: f.effects, Content: f.content,
		Now: func() time.Time { return f.at }, NewAttemptID: model.NewExamAttemptID, NewWorkspaceID: model.NewExamAttemptWorkspaceID,
		NewParticipation: model.NewAttemptParticipationID, NewConnection: model.NewAttemptConnectionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	f.call = NewCall(model.Principal{UserID: f.userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewId()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password", AuthenticationStrength: model.AuthenticationSingleFactor,
		ClientType: model.SessionClientDesktop, AuthenticatedAt: f.at}, model.RequestMetadata{RequestID: "attempt-test"})
	return f
}

type sittingFake struct{ f *fixture }

func (fake *sittingFake) Resolve(context.Context, model.ExamSittingID) (*store.ExamSittingSnapshot, error) {
	fake.f.order = append(fake.f.order, "sitting")
	return &store.ExamSittingSnapshot{Sitting: fake.f.sitting}, nil
}

type managerFake struct{ f *fixture }

func (fake *managerFake) AuthorizeSittingView(context.Context, Call, model.ExamSittingID) error {
	fake.f.order = append(fake.f.order, "manager.authorize")
	return nil
}

type auditFake struct {
	f          *fixture
	values     map[string]any
	failedCode string
}

func (fake *auditFake) Begin(_ context.Context, _ Call, _ model.Action, _ model.Resource, _ model.RoleScopeType, _ string, _ string, values map[string]any) (string, error) {
	fake.f.order = append(fake.f.order, "audit")
	fake.values = values
	return model.NewId(), nil
}
func (fake *auditFake) Fail(_ context.Context, _ string, code string) error {
	fake.f.order = append(fake.f.order, "audit.fail")
	fake.failedCode = code
	return nil
}

type effectsFake struct {
	f              *fixture
	opened, closed int
}

func (fake *effectsFake) ConnectionOpened(context.Context, ConnectionResult) error {
	fake.f.order = append(fake.f.order, "effect.open")
	fake.opened++
	return nil
}
func (fake *effectsFake) ConnectionClosed(context.Context, ConnectionClosedResult) error {
	fake.f.order = append(fake.f.order, "effect.close")
	fake.closed++
	return nil
}
func (*effectsFake) Report(context.Context, string, error) {}

type contentFake struct {
	starterID model.StarterWorkspaceObjectID
}

func (*contentFake) OpenExamResource(context.Context, model.FileRevisionID, model.FileRenditionID) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("resource")), nil
}
func (fake *contentFake) OpenStarterWorkspaceObject(_ context.Context, id model.StarterWorkspaceObjectID) (io.ReadCloser, error) {
	fake.starterID = id
	return io.NopCloser(strings.NewReader("workspace")), nil
}

type attemptStoreFake struct {
	f                  *fixture
	connect            *store.ExamAttemptConnect
	connectErr         error
	replayed           bool
	firstAdmission     bool
	connectionOpened   bool
	connectClosed      bool
	participationEnded bool
	connectionSession  model.SessionID
	closeChanged       bool
	attempt            *model.ExamAttempt
	workspace          *model.ExamAttemptWorkspace
	participation      *store.ExamAttemptParticipationView
	connection         *model.AttemptConnection
	presentation       *store.CandidateExamPresentation
	candidateAccess    store.CandidateAttemptAccess
	authorizedSession  model.SessionID
	managerOptions     store.ExamAttemptManagerListOptions
	managerRows        []store.ExamAttemptManagerSnapshot
	workspaceLists     int
	workspaceContent   *store.CandidateWorkspaceContent
}

func (fake *attemptStoreFake) Connect(_ context.Context, input *store.ExamAttemptConnect, _ *store.CommandIdempotency) (*store.ExamAttemptConnectResult, error) {
	fake.f.order = append(fake.f.order, "connect")
	fake.connect = input
	if fake.connectErr != nil {
		return nil, fake.connectErr
	}
	var err error
	fake.attempt, err = model.NewExamAttempt(input.AttemptID, fake.f.sitting.ExamID, input.SittingID, input.CandidateUserID, fake.f.revision.ID, fake.f.at)
	if err != nil {
		return nil, err
	}
	fake.workspace, err = model.NewExamAttemptWorkspace(input.WorkspaceID, input.AttemptID, fake.f.at)
	if err != nil {
		return nil, err
	}
	fake.participation = &store.ExamAttemptParticipationView{ID: input.ParticipationID, AttemptID: input.AttemptID,
		State: model.AttemptParticipationActive, Generation: 1, StartedAt: fake.f.at, UpdatedAt: fake.f.at,
		LeaseExpiresAt: fake.f.at.Add(model.AttemptParticipationInitialLease)}
	if fake.participationEnded {
		fake.participation.State = model.AttemptParticipationEnded
		fake.participation.UpdatedAt = fake.f.at.Add(time.Second)
		fake.participation.EndedAt = model.OptionalTimeFrom(fake.f.at.Add(time.Second))
		fake.participation.EndReason = model.AttemptParticipationEndInterrupted
	}
	connectionSession := input.SessionID
	if fake.connectionSession.IsValid() {
		connectionSession = fake.connectionSession
	}
	fake.connection, err = model.NewAttemptConnection(input.ConnectionID, input.AttemptID, input.ParticipationID, connectionSession, fake.f.at)
	if err != nil {
		return nil, err
	}
	if fake.connectClosed {
		if err := fake.connection.Close(model.AttemptConnectionCloseTransport, fake.f.at.Add(time.Second)); err != nil {
			return nil, err
		}
	}
	return &store.ExamAttemptConnectResult{Attempt: fake.attempt, Workspace: fake.workspace, Participation: fake.participation,
		Connection: fake.connection, ClassID: fake.f.sitting.ClassID, FirstAdmission: fake.firstAdmission,
		ConnectionOpened: fake.connectionOpened, Replayed: fake.replayed}, nil
}
func (fake *attemptStoreFake) CloseConnection(_ context.Context, input *store.ExamAttemptConnectionClose) (*store.ExamAttemptConnectionCloseResult, error) {
	fake.f.order = append(fake.f.order, "close")
	if input.CandidateUserID != fake.connect.CandidateUserID || input.SessionID != fake.connect.SessionID {
		return nil, store.NewErrNotFound("attempt_connection", input.ConnectionID.String())
	}
	connection := *fake.connection
	if err := connection.Close(input.Reason, fake.f.at.Add(time.Second)); err != nil {
		return nil, err
	}
	return &store.ExamAttemptConnectionCloseResult{AttemptID: fake.attempt.ID, SittingID: fake.connect.SittingID,
		CandidateUserID: fake.connect.CandidateUserID, Connection: &connection, Changed: fake.closeChanged}, nil
}
func (*attemptStoreFake) Get(context.Context, model.ExamID, model.ExamAttemptID) (*store.ExamAttemptManagerSnapshot, error) {
	return nil, errors.New("not configured")
}
func (fake *attemptStoreFake) List(_ context.Context, options store.ExamAttemptManagerListOptions) ([]store.ExamAttemptManagerSnapshot, error) {
	fake.f.order = append(fake.f.order, "manager.list")
	fake.managerOptions = options
	return fake.managerRows, nil
}
func (fake *attemptStoreFake) GetCandidatePresentation(_ context.Context, access store.CandidateAttemptAccess) (*store.CandidateExamPresentation, error) {
	fake.candidateAccess = access
	if fake.authorizedSession.IsValid() && access.SessionID != fake.authorizedSession {
		return nil, store.NewErrNotFound("exam_attempt_access", access.AttemptID.String())
	}
	return fake.presentation, nil
}
func (fake *attemptStoreFake) ListCandidateWorkspace(context.Context, store.CandidateWorkspaceListOptions) (*store.CandidateAttemptWorkspacePage, error) {
	fake.workspaceLists++
	return &store.CandidateAttemptWorkspacePage{}, nil
}
func (*attemptStoreFake) ResolveCandidateResource(context.Context, store.CandidateAttemptAccess, model.ExamResourceID) (*store.CandidateResourceContent, error) {
	return nil, errors.New("not configured")
}
func (fake *attemptStoreFake) ResolveCandidateWorkspaceFile(context.Context, store.CandidateAttemptAccess, model.AttemptWorkspaceEntryID) (*store.CandidateWorkspaceContent, error) {
	if fake.workspaceContent != nil {
		return fake.workspaceContent, nil
	}
	return nil, errors.New("not configured")
}
