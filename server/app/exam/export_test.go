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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type exportAuthorizerFake struct {
	actions []model.Action
	deny    model.Action
	self    bool
	denyErr error
}

func (a *exportAuthorizerFake) Authorize(_ context.Context, _ invocation.Invocation, action model.Action, _ model.Resource) error {
	a.actions = append(a.actions, action)
	if action == a.deny {
		return errors.New("current authority denied")
	}
	return nil
}
func (a *exportAuthorizerFake) RecordDenial(context.Context, invocation.Invocation, model.Action, model.Resource, model.AcademicUnitID) error {
	a.self = true
	return a.denyErr
}

func TestBrowserHistoryExportRejectsOverrideAndRechecksBeforeOpeningBytes(t *testing.T) {
	for _, removed := range []string{"manager"} {
		t.Run(removed, func(t *testing.T) {
			f, service, persistence, auth, content := newExportFixture(t)
			f.persistence.actorIsManager = true
			f.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: f.unitID, UserID: f.userID}}
			persistence.value.Categories = []model.RetentionCategory{model.RetentionCategoryBrowserActivity}
			query := ExportQuery{Scope: persistence.value.Scope, ExportID: persistence.value.ID}
			_, err := service.GetExamExport(context.Background(), f.invocation, query)
			require.NoError(t, err)
			// Unit membership is part of ordinary export authorization;
			// the independently permitted export override leaves the exact
			// Browser Activity Manager predicate and permission intact.
			f.memberships.items = nil
			_, err = service.GetExamExport(context.Background(), f.invocation, query)
			require.NoError(t, err)
			f.persistence.actorIsManager = false
			_, err = service.GetExamExport(context.Background(), f.invocation, query)
			require.Error(t, err, "export metadata retained removed Browser Activity access")
			require.True(t, auth.self, "export metadata retained removed Browser Activity access")
			_, err = service.OpenExamExport(context.Background(), f.invocation, query)
			require.Error(t, err, "unauthorized history reached archive bytes")
			require.Equal(t, 0, content.opens, "unauthorized history reached archive bytes")
			_, err = service.CreateExamExport(context.Background(), f.invocation, CreateExportCommand{Scope: query.Scope, Categories: persistence.value.Categories, IdempotencyKey: "replay"})
			require.Error(t, err, "override created browser history export")
			require.Nil(t, persistence.create, "override created browser history export")
			auditErr := errors.New("denial audit unavailable")
			auth.denyErr = auditErr
			_, err = service.GetExamExport(context.Background(), f.invocation, query)
			require.ErrorIs(t, err, auditErr)
		})
	}
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
	b, _ := json.Marshal(map[string]any{"export_id": s.value.ID})
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
	service, err := NewExports(ExportsDependencies{
		Persistence: s,
		Access:      f.persistence,
		Memberships: f.memberships,
		Authorizer:  auth,
		Auditor:     f.auditor,
		Content:     content,
		Now:         func() time.Time { return now },
		NewID:       model.NewExamExportID,
		NewJobID:    model.NewJobID,
	})
	require.NoError(t, err)
	return f, service, s, auth, content
}

func TestExamExportsRequireDedicatedAndEveryOrdinaryReadPermission(t *testing.T) {
	for _, test := range []struct {
		name                                         string
		manager, member, integrity, browser, sitting bool
		deny                                         model.Action
	}{
		{name: "ordinary manager", manager: true, member: true}, {name: "scoped override"}, {name: "manager without membership", manager: true},
		{name: "integrity", manager: true, member: true, integrity: true}, {name: "Browser history", manager: true, member: true, browser: true}, {name: "Sitting", sitting: true},
		{name: "missing export", deny: model.ActionExamRecordsExportOverride},
		{name: "missing Submission read", deny: model.ActionSubmissionViewOverride},
		{name: "missing browser read", manager: true, member: true, browser: true, deny: model.ActionExamAttemptBrowserActivityView},
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
			if test.browser {
				categories = []model.RetentionCategory{model.RetentionCategoryBrowserActivity}
			}
			_, err := service.CreateExamExport(context.Background(), f.invocation, CreateExportCommand{Scope: scope, Categories: categories, IdempotencyKey: "export-key"})
			if test.deny != "" {
				require.Error(t, err)
				require.Nil(t, s.create)
				return
			}
			require.NoError(t, err)
			ordinary := test.manager && test.member
			want := model.ActionExamRecordsExportOverride
			read := model.ActionSubmissionViewOverride
			if ordinary {
				want = model.ActionExamRecordsExport
				read = model.ActionSubmissionView
			}
			require.Equal(t, want, s.create.Action, "authorization=%v creation=%#v", auth.actions, s.create)
			require.Contains(t, auth.actions, read, "authorization=%v creation=%#v", auth.actions, s.create)
			require.True(t, slices.Equal(s.create.SubmissionIDs, []model.SubmissionID{s.selected[0].SubmissionID}), "authorization=%v creation=%#v", auth.actions, s.create)
		})
	}
}

func TestExamExportsSelfAccessAndMissingAuditFailClosed(t *testing.T) {
	f, service, s, auth, _ := newExportFixture(t)
	s.selected[0].CandidateUserID = f.userID
	command := CreateExportCommand{Scope: s.value.Scope, Categories: s.value.Categories, IdempotencyKey: "export-key"}
	_, err := service.CreateExamExport(context.Background(), f.invocation, command)
	require.Error(t, err, "candidate exported own manager-only records")
	require.True(t, auth.self, "candidate exported own manager-only records")
	require.Nil(t, s.create, "candidate exported own manager-only records")
	s.selected[0].CandidateUserID = model.NewUserID()
	f.auditor.err = errors.New("audit unavailable")
	_, err = service.CreateExamExport(context.Background(), f.invocation, command)
	require.Error(t, err, "creation bypassed required audit")
	require.Nil(t, s.create, "creation bypassed required audit")
}

func TestExamExportDownloadRechecksOrdinaryPermissionAndExpiry(t *testing.T) {
	f, service, s, auth, content := newExportFixture(t)
	s.value.State = model.ExamExportReady
	s.value.ArchiveSizeBytes = 42
	s.value.ArchiveSHA256 = strings.Repeat("a", 64)
	s.value.ReadyAt = model.OptionalTimeFrom(s.value.CreatedAt)
	query := ExportQuery{Scope: s.value.Scope, ExportID: s.value.ID}
	auth.deny = model.ActionSubmissionViewOverride
	_, err := service.OpenExamExport(context.Background(), f.invocation, query)
	require.Error(t, err, "download bypassed ordinary read authority")
	require.Equal(t, 0, content.opens, "download bypassed ordinary read authority")
	auth.deny = ""
	s.value.CreatedAt = s.value.CreatedAt.Add(-48 * time.Hour)
	s.value.ExpiresAt = s.value.ExpiresAt.Add(-48 * time.Hour)
	s.value.SourceExpiresAt = s.value.SourceExpiresAt.Add(-48 * time.Hour)
	s.value.ReadyAt = model.OptionalTimeFrom(s.value.CreatedAt)
	_, err = service.OpenExamExport(context.Background(), f.invocation, query)
	require.Error(t, err, "expired download reached content")
	require.Equal(t, 0, content.opens, "expired download reached content")
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
	_, err := service.OpenExamExport(context.Background(), f.invocation, ExportQuery{Scope: s.value.Scope, ExportID: s.value.ID})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault, "late storage Open escaped expiry")
	require.Equal(t, "exam.export.expired", fault.Code(), "late storage Open escaped expiry")
	require.True(t, body.closed, "late storage Open escaped expiry")
}

type exportUncertainWriteError struct{}

func (*exportUncertainWriteError) Error() string             { return "write uncertain" }
func (*exportUncertainWriteError) ExamExportWriteUncertain() {}

func TestExamExportBuildKeepsUnverifiedRemoteWriterReservation(t *testing.T) {
	_, service, s, _, content := newExportFixture(t)
	content.err = &exportUncertainWriteError{}
	input := store.ExamExportBuild{ExamExportArtifact: store.ExamExportArtifact{ExportID: s.value.ID, AttemptID: model.NewJobAttemptID()}, JobID: model.NewJobID()}
	input.ClaimToken, _ = model.NewJobClaimToken()
	require.Error(t, service.BuildFromJob(context.Background(), input), "unverified remote write lost its uncertain cleanup reservation")
	require.Equal(t, 0, s.finished, "unverified remote write lost its uncertain cleanup reservation")
	require.Equal(t, 0, s.published, "unverified remote write lost its uncertain cleanup reservation")
	require.Equal(t, 0, content.purges, "unverified remote write lost its uncertain cleanup reservation")
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
			require.Error(t, service.BuildFromJob(ctx, input), "uncertain construction reported success")
			require.Equal(t, 1, s.finished, "cleanup/expiry: finished=%d canceled=%v purges=%d deadline=%v", s.finished, s.finishCanceled, content.purges, content.deadline)
			require.False(t, s.finishCanceled, "cleanup/expiry: finished=%d canceled=%v purges=%d deadline=%v", s.finished, s.finishCanceled, content.purges, content.deadline)
			require.Equal(t, 0, content.purges, "cleanup/expiry: finished=%d canceled=%v purges=%d deadline=%v", s.finished, s.finishCanceled, content.purges, content.deadline)
			require.True(t, content.deadline.Equal(s.value.SourceExpiresAt), "cleanup/expiry: finished=%d canceled=%v purges=%d deadline=%v", s.finished, s.finishCanceled, content.purges, content.deadline)
			if cancelDuringBuild {
				require.Equal(t, 0, s.published, "failed copy reached publication")
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
	_, err := service.CreateExamExport(context.Background(), f.invocation, command)
	require.NoError(t, err)
	require.NotNil(t, s.creationCommand, "original captured scope was not preserved")
	require.Equal(t, store.ExamExportCreateOperation, s.creationCommand.Operation, "original captured scope was not preserved")
	require.NotNil(t, s.create, "original captured scope was not preserved")
	require.Len(t, s.create.SubmissionIDs, 1, "original captured scope was not preserved")
	require.Equal(t, original[0].SubmissionID, s.create.SubmissionIDs[0], "original captured scope was not preserved")
	auth.deny = model.ActionSubmissionViewOverride
	s.create = nil
	_, err = service.CreateExamExport(context.Background(), f.invocation, command)
	require.Error(t, err, "replay-aware scope skipped current ordinary read authority")
	require.Nil(t, s.create, "replay-aware scope skipped current ordinary read authority")
}
