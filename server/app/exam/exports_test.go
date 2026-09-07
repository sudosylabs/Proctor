// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type exportAuthorizerFake struct {
	actions []model.Action
	deny    model.Action
	self    bool
}

func (a *exportAuthorizerFake) Authorize(_ context.Context, _ Call, action model.Action, _ model.Resource) error {
	a.actions = append(a.actions, action)
	if action == a.deny {
		return errors.New("current authority denied")
	}
	return nil
}
func (a *exportAuthorizerFake) DenySelf(context.Context, Call, model.Action, model.Resource, model.AcademicUnitID) error {
	a.self = true
	return &Fault{Code: "exam.not_found"}
}

type exportStoreFake struct {
	store.ExamExportStore
	selected        []store.ExamSubmissionAuthorization
	creationScope   []store.ExamSubmissionAuthorization
	creationCommand *store.CommandIdempotency
	value           *model.ExamExport
	readAt          func() time.Time
	getErr          error
	artifactID      model.JobAttemptID
	create          *store.ExamExportCreation
	createErr       error
	publishErr      error
	finished        int
	finishCanceled  bool
	published       int
}

func (s *exportStoreFake) ListScope(context.Context, model.RetentionHoldScope) ([]store.ExamSubmissionAuthorization, error) {
	if len(s.selected) > model.ExamExportMaximumSubmissions {
		return nil, store.NewErrConflict("exam_export", "limit", nil)
	}
	return s.selected, nil
}
func (s *exportStoreFake) ListCreationScope(_ context.Context, _ model.RetentionHoldScope, command *store.CommandIdempotency) ([]store.ExamSubmissionAuthorization, error) {
	s.creationCommand = command
	if s.creationScope != nil {
		return s.creationScope, nil
	}
	return s.selected, nil
}
func (s *exportStoreFake) Create(_ context.Context, c *store.ExamExportCreation, _ *store.CommandIdempotency) (*model.ExamExport, error) {
	s.create = c
	if s.createErr != nil {
		return nil, s.createErr
	}
	v := *s.value
	v.ID = c.ExportID
	v.Scope = c.Scope
	v.Categories = slices.Clone(c.Categories)
	return &v, nil
}
func (s *exportStoreFake) Get(context.Context, *store.ExamExportAccess) (*store.ExamExportDownload, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	value := *s.value
	artifact := store.ExamExportArtifact{ExportID: value.ID, AttemptID: s.artifactID}
	if !s.readAt().Before(value.ExpiresAt) {
		value.State = model.ExamExportExpired
		value.ArchiveSizeBytes, value.ArchiveSHA256, value.ReadyAt = 0, "", model.OptionalTime{}
		artifact = store.ExamExportArtifact{}
	}
	return &store.ExamExportDownload{Export: &value, Artifact: artifact, Submissions: s.selected}, nil
}
func (s *exportStoreFake) BeginBuild(context.Context, *store.ExamExportBuild) (*store.ExamExportSnapshot, error) {
	b, _ := json.Marshal(map[string]any{"schema_version": 1, "export_id": s.value.ID})
	return &store.ExamExportSnapshot{Export: s.value, Records: b}, nil
}
func (s *exportStoreFake) Publish(context.Context, *store.ExamExportPublication) error {
	s.published++
	return s.publishErr
}
func (s *exportStoreFake) FinishWriter(ctx context.Context, _ store.ExamExportArtifact) error {
	s.finished++
	s.finishCanceled = ctx.Err() != nil
	return nil
}

type exportContentFake struct {
	opens, builds, purges int
	opened                func()
	body                  io.ReadCloser
	cancel                context.CancelFunc
	err                   error
	deadline              time.Time
}

func (c *exportContentFake) BuildExamExport(ctx context.Context, _ ExportArchiveInput) (*ExportArchiveContent, error) {
	c.builds++
	c.deadline, _ = ctx.Deadline()
	if c.cancel != nil {
		c.cancel()
	}
	if c.err != nil {
		return nil, c.err
	}
	return &ExportArchiveContent{SizeBytes: 42, SHA256: strings.Repeat("a", 64)}, nil
}
func (c *exportContentFake) OpenExamExport(context.Context, model.ExamExportID, model.JobAttemptID) (io.ReadCloser, error) {
	c.opens++
	if c.opened != nil {
		c.opened()
	}
	if c.body != nil {
		return c.body, c.err
	}
	return io.NopCloser(strings.NewReader("archive")), c.err
}
func (c *exportContentFake) PurgeExamExport(context.Context, model.ExamExportID, model.JobAttemptID) error {
	c.purges++
	return nil
}

func newExportFixture(t *testing.T) (authoringFixture, *Exports, *exportStoreFake, *exportAuthorizerFake, *exportContentFake) {
	t.Helper()
	f := newAuthoringFixture(t)
	now := model.NowUTC()
	scope := model.RetentionHoldScope{ExamID: f.examID, SittingID: model.NewExamSittingID(), SubmissionID: model.NewSubmissionID()}
	s := &exportStoreFake{value: &model.ExamExport{ID: model.NewExamExportID(), Scope: scope, RequesterUserID: f.userID, Categories: []model.RetentionCategory{model.RetentionCategoryWork}, State: model.ExamExportQueued, PolicyRevision: 1, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), SourceExpiresAt: now.Add(24 * time.Hour), SubmissionCount: 1}}
	s.readAt, s.artifactID = func() time.Time { return now }, model.NewJobAttemptID()
	s.selected = []store.ExamSubmissionAuthorization{{SubmissionID: scope.SubmissionID, ExamID: scope.ExamID, SittingID: scope.SittingID, AttemptID: model.NewExamAttemptID(), CandidateUserID: model.NewUserID(), AcademicUnitID: f.unitID}}
	auth, content := &exportAuthorizerFake{}, &exportContentFake{}
	service, err := NewExports(s, f.persistence, f.memberships, auth, f.auditor, content, func() time.Time { return now }, model.NewExamExportID, model.NewJobID)
	if err != nil {
		t.Fatal(err)
	}
	return f, service, s, auth, content
}

func TestExamExportsRequireDedicatedAndEveryOrdinaryReadPermission(t *testing.T) {
	for _, test := range []struct {
		name                                string
		manager, member, integrity, sitting bool
		deny                                model.Action
	}{
		{name: "ordinary manager", manager: true, member: true}, {name: "scoped override"}, {name: "manager without membership", manager: true},
		{name: "integrity", manager: true, member: true, integrity: true}, {name: "Sitting", sitting: true},
		{name: "missing export", deny: model.ActionExamRecordsExportOverride},
		{name: "missing Submission read", deny: model.ActionSubmissionViewOverride},
		{name: "missing browser read", integrity: true, deny: model.ActionExamAttemptBrowserActivityViewOverride},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, service, s, auth, _ := newExportFixture(t)
			f.persistence.actorIsManager = test.manager
			if test.member {
				f.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: f.unitID, UserID: f.userID}}
			}
			auth.deny = test.deny
			scope := s.value.Scope
			if test.sitting {
				scope.SubmissionID = ""
			}
			categories := []model.RetentionCategory{model.RetentionCategoryWork}
			if test.integrity {
				categories = append(categories, model.RetentionCategoryIntegrity)
			}
			_, err := service.Create(context.Background(), f.call, CreateExportCommand{Scope: scope, Categories: categories, IdempotencyKey: "export-key"})
			if test.deny != "" {
				if err == nil || s.create != nil {
					t.Fatalf("denied action reached persistence: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ordinary := test.manager && test.member
			want := model.ActionExamRecordsExportOverride
			read := model.ActionSubmissionViewOverride
			if ordinary {
				want = model.ActionExamRecordsExport
				read = model.ActionSubmissionView
			}
			if s.create.Action != want || !slices.Contains(auth.actions, read) || !slices.Equal(s.create.SubmissionIDs, []model.SubmissionID{s.selected[0].SubmissionID}) {
				t.Fatalf("authorization=%v creation=%#v", auth.actions, s.create)
			}
		})
	}
}

func TestExamExportsSelfAccessAndMissingAuditFailClosed(t *testing.T) {
	f, service, s, auth, _ := newExportFixture(t)
	s.selected[0].CandidateUserID = f.userID
	command := CreateExportCommand{Scope: s.value.Scope, Categories: s.value.Categories, IdempotencyKey: "export-key"}
	if _, err := service.Create(context.Background(), f.call, command); err == nil || !auth.self || s.create != nil {
		t.Fatal("candidate exported own manager-only records")
	}
	s.selected[0].CandidateUserID = model.NewUserID()
	f.auditor.err = errors.New("audit unavailable")
	if _, err := service.Create(context.Background(), f.call, command); err == nil || s.create != nil {
		t.Fatal("creation bypassed required audit")
	}
}

func TestExamExportDownloadRechecksOrdinaryPermissionAndExpiry(t *testing.T) {
	f, service, s, auth, content := newExportFixture(t)
	s.value.State = model.ExamExportReady
	s.value.ArchiveSizeBytes = 42
	s.value.ArchiveSHA256 = strings.Repeat("a", 64)
	s.value.ReadyAt = model.OptionalTimeFrom(s.value.CreatedAt)
	query := ExportQuery{Scope: s.value.Scope, ExportID: s.value.ID}
	auth.deny = model.ActionSubmissionViewOverride
	if _, err := service.Open(context.Background(), f.call, query); err == nil || content.opens != 0 {
		t.Fatal("download bypassed ordinary read authority")
	}
	auth.deny = ""
	s.value.CreatedAt = s.value.CreatedAt.Add(-48 * time.Hour)
	s.value.ExpiresAt = s.value.ExpiresAt.Add(-48 * time.Hour)
	s.value.SourceExpiresAt = s.value.SourceExpiresAt.Add(-48 * time.Hour)
	s.value.ReadyAt = model.OptionalTimeFrom(s.value.CreatedAt)
	if _, err := service.Open(context.Background(), f.call, query); err == nil || content.opens != 0 {
		t.Fatal("expired download reached content")
	}
}

type exportClosingBody struct{ closed bool }

func (*exportClosingBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *exportClosingBody) Close() error           { b.closed = true; return nil }

func TestExamExportDownloadExpiresDuringStorageOpen(t *testing.T) {
	f, service, s, _, content := newExportFixture(t)
	s.value.State, s.value.ArchiveSizeBytes, s.value.ArchiveSHA256, s.value.ReadyAt = model.ExamExportReady, 42, strings.Repeat("a", 64), model.OptionalTimeFrom(s.value.CreatedAt)
	now := s.value.CreatedAt
	s.readAt = func() time.Time { return now }
	body := &exportClosingBody{}
	content.body = body
	content.opened = func() { now = s.value.ExpiresAt }
	_, err := service.Open(context.Background(), f.call, ExportQuery{Scope: s.value.Scope, ExportID: s.value.ID})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.export.expired" || !body.closed {
		t.Fatalf("late storage Open escaped expiry: %v, closed=%v", err, body.closed)
	}
}

type exportUncertainWriteError struct{}

func (*exportUncertainWriteError) Error() string             { return "write uncertain" }
func (*exportUncertainWriteError) ExamExportWriteUncertain() {}

func TestExamExportBuildKeepsUnverifiedRemoteWriterReservation(t *testing.T) {
	_, service, s, _, content := newExportFixture(t)
	content.err = &exportUncertainWriteError{}
	input := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: s.value.ID, AttemptID: model.NewJobAttemptID()}, JobID: model.NewJobID()}
	input.ClaimToken, _ = model.NewJobClaimToken()
	if err := service.BuildFromJob(context.Background(), input); err == nil || s.finished != 0 || s.published != 0 || content.purges != 0 {
		t.Fatal("unverified remote write lost its uncertain cleanup reservation")
	}
}

func TestExamExportBuildKeepsUnknownPublicationAndFinishesWriterOutsideCancellation(t *testing.T) {
	for _, cancelDuringBuild := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown publication", true: "canceled copy"}[cancelDuringBuild], func(t *testing.T) {
			_, service, s, _, content := newExportFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelDuringBuild {
				content.cancel = cancel
				content.err = context.Canceled
			} else {
				s.publishErr = errors.New("commit acknowledgement lost")
			}
			input := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: s.value.ID, AttemptID: model.NewJobAttemptID()}, JobID: model.NewJobID()}
			input.ClaimToken, _ = model.NewJobClaimToken()
			if err := service.BuildFromJob(ctx, input); err == nil {
				t.Fatal("uncertain construction reported success")
			}
			if s.finished != 1 || s.finishCanceled || content.purges != 0 || !content.deadline.Equal(s.value.SourceExpiresAt) {
				t.Fatalf("cleanup/expiry: finished=%d canceled=%v purges=%d deadline=%v", s.finished, s.finishCanceled, content.purges, content.deadline)
			}
			if cancelDuringBuild && s.published != 0 {
				t.Fatal("failed copy reached publication")
			}
		})
	}
}

func TestExamExportReplayUsesCapturedScopeAfterSittingGrowsPastBound(t *testing.T) {
	f, service, s, auth, _ := newExportFixture(t)
	original := slices.Clone(s.selected)
	s.creationScope = original
	s.selected = make([]store.ExamSubmissionAuthorization, model.ExamExportMaximumSubmissions+1)
	s.value.Scope.SubmissionID = ""
	command := CreateExportCommand{Scope: s.value.Scope, Categories: s.value.Categories, IdempotencyKey: "retained-export"}
	if _, err := service.Create(context.Background(), f.call, command); err != nil {
		t.Fatalf("exact replay enumerated the grown Sitting: %v", err)
	}
	if s.creationCommand == nil || s.creationCommand.Operation != store.ExamExportCreateOperation || s.create == nil || len(s.create.SubmissionIDs) != 1 || s.create.SubmissionIDs[0] != original[0].SubmissionID {
		t.Fatal("original captured scope was not preserved")
	}
	auth.deny = model.ActionSubmissionViewOverride
	s.create = nil
	if _, err := service.Create(context.Background(), f.call, command); err == nil || s.create != nil {
		t.Fatal("replay-aware scope skipped current ordinary read authority")
	}
}
