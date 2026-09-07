// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"errors"
	"io"
	"slices"
	"time"

	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ExportArchiveInput is private byte-processing input owned by its consumer.
// The content adapter writes the versioned portable representation, verifies
// original bytes and the completed archive, and never chooses domain scope.
type ExportArchiveInput struct {
	ExportID  model.ExamExportID
	AttemptID model.JobAttemptID
	Records   []byte
	Files     []ExportArchiveFile
}

type ExportArchiveFile struct {
	SubmissionID model.SubmissionID
	Entry        model.ExamSubmissionManifestEntry
}

type ExportArchiveContent struct {
	SizeBytes int64
	SHA256    string
}

type ExportContent interface {
	// An unsuccessful write whose backend outcome is still uncertain must
	// return an error implementing ExamExportWriteUncertain(). Its durable
	// artifact reservation remains necessary even after one absence check.
	BuildExamExport(context.Context, ExportArchiveInput) (*ExportArchiveContent, error)
	OpenExamExport(context.Context, model.ExamExportID, model.JobAttemptID) (io.ReadCloser, error)
	PurgeExamExport(context.Context, model.ExamExportID, model.JobAttemptID) error
}

type CreateExportCommand struct {
	Scope          model.RetentionHoldScope
	Categories     []model.RetentionCategory
	IdempotencyKey string
}

type ExportQuery struct {
	Scope    model.RetentionHoldScope
	ExportID model.ExamExportID
}

type ExportDownload struct {
	Export *model.ExamExport
	Body   io.ReadCloser
}

type Exports struct {
	persistence store.ExamExportStore
	access      store.ExamAuthoringStore
	memberships manageraccess.Memberships
	authorizer  RecordsAuthorizer
	auditor     Auditor
	content     ExportContent
	now         func() time.Time
	newID       func() model.ExamExportID
	newJobID    func() model.JobID
}

func NewExports(persistence store.ExamExportStore, access store.ExamAuthoringStore, memberships manageraccess.Memberships, authorizer RecordsAuthorizer, auditor Auditor, content ExportContent, now func() time.Time, newID func() model.ExamExportID, newJobID func() model.JobID) (*Exports, error) {
	if persistence == nil || access == nil || memberships == nil || authorizer == nil || auditor == nil || content == nil || now == nil || newID == nil || newJobID == nil {
		return nil, errors.New("Exam export dependencies are required")
	}
	return &Exports{persistence: persistence, access: access, memberships: memberships, authorizer: authorizer, auditor: auditor, content: content, now: now, newID: newID, newJobID: newJobID}, nil
}

func (s *Exports) authorize(ctx context.Context, call Call, scope model.RetentionHoldScope) (model.Action, model.AcademicUnitID, error) {
	if scope.Validate() != nil || !scope.SittingID.IsValid() || call.Principal().Validate() != nil || call.Principal().CredentialType != model.CredentialSessionAccess {
		return "", "", invalid("export_scope")
	}
	access, err := s.access.Access(ctx, scope.ExamID, call.Principal().UserID)
	if err != nil {
		return "", "", exportStoreError(err)
	}
	if access == nil || access.Exam == nil || access.Exam.ID != scope.ExamID || !access.Exam.AcademicUnitID.IsValid() {
		return "", "", exportUnavailable(err)
	}
	action, err := manageraccess.SelectAction(ctx, s.memberships, call.Principal().UserID, access, model.TimeUTC(s.now()), model.ActionExamRecordsExport, model.ActionExamRecordsExportOverride)
	if err != nil {
		return "", "", exportUnavailable(err)
	}
	if err = s.authorizer.Authorize(ctx, call, action, scope.Resource()); err != nil {
		return "", "", err
	}
	return action, access.Exam.AcademicUnitID, nil
}

func (s *Exports) authorizeSources(ctx context.Context, call Call, scope model.RetentionHoldScope, action model.Action, unit model.AcademicUnitID, categories []model.RetentionCategory, selected []store.ExamSubmissionAuthorization) error {
	if len(selected) < 1 || len(selected) > model.ExamExportMaximumSubmissions {
		return exportUnavailable(errors.New("invalid export source scope"))
	}
	view, sittingView, browser := model.ActionSubmissionViewOverride, model.ActionExamSittingViewOverride, model.ActionExamAttemptBrowserActivityViewOverride
	if action == model.ActionExamRecordsExport {
		view, sittingView, browser = model.ActionSubmissionView, model.ActionExamSittingView, model.ActionExamAttemptBrowserActivityView
	}
	if scope.SubmissionID.IsZero() {
		if err := s.authorizer.Authorize(ctx, call, sittingView, scope.Resource()); err != nil {
			return err
		}
	}
	if slices.Contains(categories, model.RetentionCategoryIntegrity) {
		if err := s.authorizer.Authorize(ctx, call, browser, model.Resource{Type: model.ResourceExamSitting, ID: scope.SittingID.String()}); err != nil {
			return err
		}
	}
	var previous model.SubmissionID
	for _, a := range selected {
		if !a.SubmissionID.IsValid() || a.SubmissionID <= previous || a.ExamID != scope.ExamID || a.SittingID != scope.SittingID || a.AcademicUnitID != unit || !a.CandidateUserID.IsValid() || !a.AttemptID.IsValid() || scope.SubmissionID.IsValid() && a.SubmissionID != scope.SubmissionID {
			return exportUnavailable(errors.New("invalid export source scope"))
		}
		resource := model.Resource{Type: model.ResourceSubmission, ID: a.SubmissionID.String()}
		if a.CandidateUserID == call.Principal().UserID {
			return s.authorizer.DenySelf(ctx, call, view, resource, unit)
		}
		if err := s.authorizer.Authorize(ctx, call, view, resource); err != nil {
			return err
		}
		previous = a.SubmissionID
	}
	return nil
}

func (s *Exports) Create(ctx context.Context, call Call, command CreateExportCommand) (*model.ExamExport, error) {
	command.Categories = slices.Clone(command.Categories)
	if len(command.Categories) == 2 && command.Categories[0] == model.RetentionCategoryIntegrity && command.Categories[1] == model.RetentionCategoryWork {
		command.Categories[0], command.Categories[1] = command.Categories[1], command.Categories[0]
	}
	if !model.ValidExamExportCategories(command.Categories) {
		return nil, invalid("export_categories")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := prepareIdempotency(call, store.ExamExportCreateOperation, key, command)
	if err != nil {
		return nil, err
	}
	action, unit, err := s.authorize(ctx, call, command.Scope)
	if err != nil {
		return nil, err
	}
	selected, err := s.persistence.ListCreationScope(ctx, command.Scope, idempotency)
	if err != nil {
		return nil, exportStoreError(err)
	}
	if err = s.authorizeSources(ctx, call, command.Scope, action, unit, command.Categories, selected); err != nil {
		return nil, err
	}
	exportID, jobID := s.newID(), s.newJobID()
	if !exportID.IsValid() || !jobID.IsValid() {
		return nil, exportUnavailable(errors.New("invalid export identities"))
	}
	auditID, err := s.auditor.Begin(ctx, call, action, command.Scope.Resource(), model.RoleScopeAcademicUnit, unit.String(), "create_exam_export", map[string]any{"exam_export_id": exportID.String(), "submission_count": len(selected), "category_count": len(command.Categories)}, nil)
	if err != nil {
		return nil, err
	}
	input := &store.ExamExportCreation{ExamExportAccess: store.ExamExportAccess{ExamRecordsMutation: store.ExamRecordsMutation{Scope: command.Scope, Principal: call.Principal(), Action: action, AuditEventID: auditID, AuditAt: model.MillisFromTime(model.TimeUTC(s.now()))}, ExportID: exportID, Categories: command.Categories}, JobID: jobID}
	for _, a := range selected {
		input.SubmissionIDs = append(input.SubmissionIDs, a.SubmissionID)
	}
	result, err := s.persistence.Create(ctx, input, idempotency)
	if err != nil {
		mapped := exportStoreError(err)
		var f *Fault
		code := "exam.export.unavailable"
		if errors.As(mapped, &f) {
			code = f.Code
		}
		if auditErr := s.auditor.Fail(ctx, auditID, code); auditErr != nil {
			return nil, auditErr
		}
		return nil, mapped
	}
	if result.Validate() != nil || result.Scope != command.Scope || result.RequesterUserID != call.Principal().UserID || !slices.Equal(result.Categories, command.Categories) {
		return nil, exportUnavailable(errors.New("invalid export creation result"))
	}
	return result, nil
}

func (s *Exports) get(ctx context.Context, call Call, q ExportQuery) (*store.ExamExportDownload, error) {
	if !q.ExportID.IsValid() {
		return nil, invalid("exam_export_id")
	}
	action, unit, err := s.authorize(ctx, call, q.Scope)
	if err != nil {
		return nil, err
	}
	result, err := s.persistence.Get(ctx, &store.ExamExportAccess{ExamRecordsMutation: store.ExamRecordsMutation{Scope: q.Scope, Principal: call.Principal(), Action: action}, ExportID: q.ExportID})
	if err != nil {
		return nil, exportStoreError(err)
	}
	if result == nil || result.Export.Validate() != nil || result.Export.ID != q.ExportID || result.Export.Scope != q.Scope || result.Export.RequesterUserID != call.Principal().UserID {
		return nil, exportUnavailable(errors.New("invalid export read result"))
	}
	if err = s.authorizeSources(ctx, call, q.Scope, action, unit, result.Export.Categories, result.Submissions); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Exports) Get(ctx context.Context, call Call, q ExportQuery) (*model.ExamExport, error) {
	result, err := s.get(ctx, call, q)
	if err != nil {
		return nil, err
	}
	return result.Export, nil
}

func (s *Exports) Open(ctx context.Context, call Call, q ExportQuery) (*ExportDownload, error) {
	result, err := s.get(ctx, call, q)
	if err != nil {
		return nil, err
	}
	if err = requireReadyExport(result); err != nil {
		return nil, err
	}
	body, err := s.content.OpenExamExport(ctx, result.Artifact.ExportID, result.Artifact.AttemptID)
	if err != nil {
		return nil, exportUnavailable(err)
	}
	if body == nil {
		return nil, exportUnavailable(errors.New("missing export body"))
	}
	// Storage can block across expiry or an authority change. Re-read through
	// the authoritative Store before exposing a body; node wall-clock time
	// cannot extend or shorten the archive's PostgreSQL-clocked lifetime.
	current, err := s.get(ctx, call, q)
	if err == nil {
		err = requireReadyExport(current)
	}
	if err == nil && current.Artifact != result.Artifact {
		err = exportUnavailable(errors.New("export artifact changed during open"))
	}
	if err != nil {
		_ = body.Close()
		return nil, err
	}
	return &ExportDownload{Export: current.Export, Body: body}, nil
}

func requireReadyExport(result *store.ExamExportDownload) error {
	if result.Export.State == model.ExamExportExpired {
		return &Fault{Code: "exam.export.expired"}
	}
	if result.Export.State != model.ExamExportReady {
		return &Fault{Code: "exam.export.not_ready"}
	}
	if result.Artifact.ExportID != result.Export.ID || !result.Artifact.AttemptID.IsValid() {
		return exportUnavailable(errors.New("invalid export artifact"))
	}
	return nil
}

func (s *Exports) BuildFromJob(ctx context.Context, input store.ExamExportBuild) (err error) {
	snapshot, err := s.persistence.BeginBuild(ctx, &input)
	if err != nil {
		return exportStoreError(err)
	}
	if snapshot == nil || snapshot.Export.Validate() != nil || snapshot.Export.ID != input.ExportID {
		return exportUnavailable(errors.New("invalid export build snapshot"))
	}
	if snapshot.Export.State == model.ExamExportReady {
		return nil
	}
	// Publication may have an unknown outcome. Cleanup consults durable ready
	// metadata; this path never guesses whether it is safe to delete bytes.
	defer func() {
		var uncertain interface{ ExamExportWriteUncertain() }
		if errors.As(err, &uncertain) {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if finishErr := s.persistence.FinishWriter(cleanup, input.ExamExportArtifact); finishErr != nil && err == nil {
			err = exportUnavailable(finishErr)
		}
	}()
	if snapshot.Export.State != model.ExamExportQueued || len(snapshot.Records) < 1 || len(snapshot.Records) > model.ExamExportMaximumSnapshotBytes || len(snapshot.Files) != snapshot.Export.FileCount {
		return exportUnavailable(errors.New("invalid export build input"))
	}
	buildCtx, cancel := context.WithDeadline(ctx, snapshot.Export.SourceExpiresAt)
	defer cancel()
	archive := ExportArchiveInput{ExportID: input.ExportID, AttemptID: input.AttemptID, Records: snapshot.Records}
	for _, f := range snapshot.Files {
		archive.Files = append(archive.Files, ExportArchiveFile{SubmissionID: f.SubmissionID, Entry: f.Entry})
	}
	content, err := s.content.BuildExamExport(buildCtx, archive)
	if err != nil {
		return exportUnavailable(err)
	}
	if content == nil || !model.ValidExamExportContent(content.SizeBytes, content.SHA256) {
		return exportUnavailable(errors.New("invalid verified export content"))
	}
	if err = s.persistence.Publish(buildCtx, &store.ExamExportPublication{ExamExportBuild: input, SizeBytes: content.SizeBytes, SHA256: content.SHA256}); err != nil {
		return exportStoreError(err)
	}
	return nil
}

func exportUnavailable(err error) error { return &Fault{Code: "exam.export.unavailable", Cause: err} }
func exportStoreError(err error) error {
	if err == nil {
		return nil
	}
	var keyConflict *store.ErrIdempotencyConflict
	var inProgress *store.ErrIdempotencyInProgress
	if errors.As(err, &keyConflict) {
		return &Fault{Code: "idempotency.conflict", Cause: err}
	}
	if errors.As(err, &inProgress) {
		return &Fault{Code: "idempotency.in_progress", Cause: err}
	}
	if store.IsNotFound(err) {
		return &Fault{Code: "exam.export.not_found", Cause: err}
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) && conflict.Resource == "authorization" {
		return &Fault{Code: "exam.export.not_found", Cause: err}
	}
	if errors.As(err, &conflict) && conflict.Resource == "exam_export" {
		switch conflict.Constraint {
		case "policy_unconfigured", "limit", "source_retired", "scope_changed", "expired", "claim_lost":
			return &Fault{Code: "exam.export." + conflict.Constraint, Cause: err}
		}
	}
	if store.IsConflict(err) {
		return &Fault{Code: "exam.export.conflict", Cause: err}
	}
	return exportUnavailable(err)
}
