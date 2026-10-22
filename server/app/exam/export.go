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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/exam/manageraccess"
	"github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/app/invocation"
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

// ExportArchiveFile names one sealed Submission file to include in an
// Examination Export archive.
type ExportArchiveFile struct {
	SubmissionID model.SubmissionID
	Entry        model.ExamSubmissionManifestEntry
}

// ExportArchiveContent describes the verified archive bytes written by
// ExportContent.
type ExportArchiveContent struct {
	SizeBytes int64
	SHA256    string
}

// ExportContent writes, opens, and purges the stored archive bytes of an
// Examination Export, keyed by export and Job attempt.
type ExportContent interface {
	// An unsuccessful write whose backend outcome is still uncertain must
	// return an error implementing ExamExportWriteUncertain(). Its durable
	// artifact reservation remains necessary even after one absence check.
	BuildExamExport(context.Context, ExportArchiveInput) (*ExportArchiveContent, error)
	OpenExamExport(context.Context, model.ExamExportID, model.JobAttemptID) (io.ReadCloser, error)
	PurgeExamExport(context.Context, model.ExamExportID, model.JobAttemptID) error
}

// CreateExportCommand requests an Examination Export of the given retention
// categories for a Sitting or one of its Submissions.
type CreateExportCommand struct {
	Scope          model.RetentionHoldScope
	Categories     []model.RetentionCategory
	IdempotencyKey string
}

// ExportQuery names one Examination Export within its scope.
type ExportQuery struct {
	Scope    model.RetentionHoldScope
	ExportID model.ExamExportID
}

// ExportDownload pairs a ready Examination Export with its archive body,
// which the caller must close.
type ExportDownload struct {
	Export *model.ExamExport
	Body   io.ReadCloser
}

// Exports requests, reads, and builds Examination Exports.
type Exports struct{ deps ExportsDependencies }

// ExportsDependencies are the collaborators of Exports; NewExports validates them once and the
// service retains them unchanged.
type ExportsDependencies struct {
	Persistence store.ExamExportStore
	Access      store.ExamAuthoringStore
	Memberships manageraccess.Memberships
	Authorizer  RecordsAuthorizer
	Auditor     Auditor
	Content     ExportContent
	Now         func() time.Time
	NewID       func() model.ExamExportID
	NewJobID    func() model.JobID
}

// NewExports returns an Exports service, or an error when a required
// dependency is missing.
func NewExports(deps ExportsDependencies) (*Exports, error) {
	if deps.Persistence == nil || deps.Access == nil || deps.Memberships == nil || deps.Authorizer == nil || deps.Auditor == nil || deps.Content == nil || deps.Now == nil || deps.NewID == nil || deps.NewJobID == nil {
		return nil, errors.New("Exam export dependencies are required")
	}
	return &Exports{deps: deps}, nil
}

func (s *Exports) authorize(ctx context.Context, invocation invocation.Invocation, scope model.RetentionHoldScope) (model.Action, model.AcademicUnitID, error) {
	if scope.Validate() != nil || !scope.SittingID.IsValid() || invocation.Principal().Validate() != nil || invocation.Principal().CredentialType != model.CredentialSessionAccess {
		return "", "", apperror.Invalid("exam.invalid", "export_scope")
	}
	access, err := s.deps.Access.Access(ctx, scope.ExamID, invocation.Principal().UserID)
	if err != nil {
		return "", "", examExportStoreErrors.Translate(err)
	}
	if access == nil || access.Exam == nil || access.Exam.ID != scope.ExamID || !access.Exam.AcademicUnitID.IsValid() {
		return "", "", apperror.New("exam.export.unavailable").Wrap(err)
	}
	action, err := manageraccess.SelectAction(ctx, s.deps.Memberships, invocation.Principal().UserID, access, model.TimeUTC(s.deps.Now()), model.ActionExamRecordsExport, model.ActionExamRecordsExportOverride)
	if err != nil {
		return "", "", apperror.New("exam.export.unavailable").Wrap(err)
	}
	if err = s.deps.Authorizer.Authorize(ctx, invocation, action, scope.Resource()); err != nil {
		return "", "", err
	}
	return action, access.Exam.AcademicUnitID, nil
}

func (s *Exports) authorizeSources(ctx context.Context, invocation invocation.Invocation, scope model.RetentionHoldScope, action model.Action, unit model.AcademicUnitID, categories []model.RetentionCategory, selected []store.ExamSubmissionAuthorization) error {
	if len(selected) < 1 || len(selected) > model.ExamExportMaximumSubmissions {
		return apperror.New("exam.export.unavailable").Wrap(errors.New("invalid export source scope"))
	}
	view, sittingView := model.ActionSubmissionViewOverride, model.ActionExamSittingViewOverride
	if action == model.ActionExamRecordsExport {
		view, sittingView = model.ActionSubmissionView, model.ActionExamSittingView
	}
	if scope.SubmissionID.IsZero() {
		if err := s.deps.Authorizer.Authorize(ctx, invocation, sittingView, scope.Resource()); err != nil {
			return err
		}
	}
	if slices.Contains(categories, model.RetentionCategoryBrowserActivity) {
		access, err := s.deps.Access.Access(ctx, scope.ExamID, invocation.Principal().UserID)
		if err != nil {
			return examExportStoreErrors.Translate(err)
		}
		if access == nil || access.Exam == nil || access.Exam.ID != scope.ExamID || access.Exam.AcademicUnitID != unit {
			return apperror.New("exam.export.unavailable").Wrap(errors.New("invalid browser history access projection"))
		}
		if !access.ActorIsManager {
			if err := s.deps.Authorizer.RecordDenial(ctx, invocation, model.ActionExamAttemptBrowserActivityView, model.Resource{Type: model.ResourceExamSitting, ID: scope.SittingID.String()}, unit); err != nil {
				return err
			}
			return apperror.New("exam.not_found")
		}
		if err := s.deps.Authorizer.Authorize(ctx, invocation, model.ActionExamAttemptBrowserActivityView, model.Resource{Type: model.ResourceExamSitting, ID: scope.SittingID.String()}); err != nil {
			return err
		}
	}
	var previous model.SubmissionID
	for _, a := range selected {
		if !a.SubmissionID.IsValid() || a.SubmissionID <= previous || a.ExamID != scope.ExamID || a.SittingID != scope.SittingID || a.AcademicUnitID != unit || !a.CandidateUserID.IsValid() || !a.AttemptID.IsValid() || scope.SubmissionID.IsValid() && a.SubmissionID != scope.SubmissionID {
			return apperror.New("exam.export.unavailable").Wrap(errors.New("invalid export source scope"))
		}
		resource := model.Resource{Type: model.ResourceSubmission, ID: a.SubmissionID.String()}
		if a.CandidateUserID == invocation.Principal().UserID {
			if err := s.deps.Authorizer.RecordDenial(ctx, invocation, view, resource, unit); err != nil {
				return err
			}
			return apperror.New("exam.not_found")
		}
		if err := s.deps.Authorizer.Authorize(ctx, invocation, view, resource); err != nil {
			return err
		}
		previous = a.SubmissionID
	}
	return nil
}

// CreateExamExport idempotently queues an Examination Export for the caller
// and its build Job, after authorizing records export for the scope and
// access to every selected source category.
func (s *Exports) CreateExamExport(ctx context.Context, invocation invocation.Invocation, command CreateExportCommand) (_ *model.ExamExport, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	command.Categories = slices.Clone(command.Categories)
	if len(command.Categories) == 2 && command.Categories[0] == model.RetentionCategoryIntegrity && command.Categories[1] == model.RetentionCategoryWork {
		command.Categories[0], command.Categories[1] = command.Categories[1], command.Categories[0]
	}
	if !model.ValidExamExportCategories(command.Categories) {
		return nil, apperror.Invalid("exam.invalid", "export_categories")
	}
	key := command.IdempotencyKey
	command.IdempotencyKey = ""
	idempotency, err := idempotency.Require(invocation, store.ExamExportCreateOperation, key, command)
	if err != nil {
		return nil, err
	}
	action, unit, err := s.authorize(ctx, invocation, command.Scope)
	if err != nil {
		return nil, err
	}
	selected, err := s.deps.Persistence.ListCreationScope(ctx, command.Scope, idempotency)
	if err != nil {
		return nil, examExportStoreErrors.Translate(err)
	}
	if err = s.authorizeSources(ctx, invocation, command.Scope, action, unit, command.Categories, selected); err != nil {
		return nil, err
	}
	exportID, jobID := s.deps.NewID(), s.deps.NewJobID()
	if !exportID.IsValid() || !jobID.IsValid() {
		return nil, apperror.New("exam.export.unavailable").Wrap(errors.New("invalid export identities"))
	}
	auditID, err := s.deps.Auditor.BeginAtScope(ctx, invocation, action, command.Scope.Resource(), model.RoleScopeAcademicUnit, unit.String(), "create_exam_export", map[string]any{"exam_export_id": exportID.String(), "submission_count": len(selected), "category_count": len(command.Categories)}, nil)
	if err != nil {
		return nil, err
	}
	input := &store.ExamExportCreation{ExamExportAccess: store.ExamExportAccess{ExamRecordsMutation: store.ExamRecordsMutation{Scope: command.Scope, Principal: invocation.Principal(), Action: action, AuditEventID: auditID, AuditAt: model.MillisFromTime(model.TimeUTC(s.deps.Now()))}, ExportID: exportID, Categories: command.Categories}, JobID: jobID}
	for _, a := range selected {
		input.SubmissionIDs = append(input.SubmissionIDs, a.SubmissionID)
	}
	result, err := s.deps.Persistence.Create(ctx, input, idempotency)
	if err != nil {
		mapped := examExportStoreErrors.Translate(err)
		code := apperror.CodeOf(mapped, "exam.export.unavailable")
		if auditErr := s.deps.Auditor.Fail(ctx, auditID, code); auditErr != nil {
			return nil, errors.Join(mapped, auditErr)
		}
		return nil, mapped
	}
	if result.Validate() != nil || result.Scope != command.Scope || result.RequesterUserID != invocation.Principal().UserID || !slices.Equal(result.Categories, command.Categories) {
		return nil, apperror.New("exam.export.unavailable").Wrap(errors.New("invalid export creation result"))
	}
	return result, nil
}

func (s *Exports) get(ctx context.Context, invocation invocation.Invocation, q ExportQuery) (*store.ExamExportDownload, error) {
	if !q.ExportID.IsValid() {
		return nil, apperror.Invalid("exam.invalid", "exam_export_id")
	}
	action, unit, err := s.authorize(ctx, invocation, q.Scope)
	if err != nil {
		return nil, err
	}
	result, err := s.deps.Persistence.Get(ctx, &store.ExamExportAccess{ExamRecordsMutation: store.ExamRecordsMutation{Scope: q.Scope, Principal: invocation.Principal(), Action: action}, ExportID: q.ExportID})
	if err != nil {
		return nil, examExportStoreErrors.Translate(err)
	}
	if result == nil || result.Export.Validate() != nil || result.Export.ID != q.ExportID || result.Export.Scope != q.Scope || result.Export.RequesterUserID != invocation.Principal().UserID {
		return nil, apperror.New("exam.export.unavailable").Wrap(errors.New("invalid export read result"))
	}
	if err = s.authorizeSources(ctx, invocation, q.Scope, action, unit, result.Export.Categories, result.Submissions); err != nil {
		return nil, err
	}
	return result, nil
}

// GetExamExport returns the caller's own Examination Export after re-checking
// authority over its scope and sources.
func (s *Exports) GetExamExport(ctx context.Context, invocation invocation.Invocation, q ExportQuery) (_ *model.ExamExport, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	result, err := s.get(ctx, invocation, q)
	if err != nil {
		return nil, err
	}
	return result.Export, nil
}

// OpenExamExport opens the archive of the caller's own ready Examination
// Export, re-reading the export after opening so expiry or a change in
// authority is not bypassed.
func (s *Exports) OpenExamExport(ctx context.Context, invocation invocation.Invocation, q ExportQuery) (_ *ExportDownload, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	result, err := s.get(ctx, invocation, q)
	if err != nil {
		return nil, err
	}
	if err = requireReadyExport(result); err != nil {
		return nil, err
	}
	body, err := s.deps.Content.OpenExamExport(ctx, result.Artifact.ExportID, result.Artifact.AttemptID)
	if err != nil {
		return nil, apperror.New("exam.export.unavailable").Wrap(err)
	}
	if body == nil {
		return nil, apperror.New("exam.export.unavailable").Wrap(errors.New("missing export body"))
	}
	// Storage can block across expiry or an authority change. Re-read through
	// the authoritative Store before exposing a body; node wall-clock time
	// cannot extend or shorten the archive's PostgreSQL-clocked lifetime.
	current, err := s.get(ctx, invocation, q)
	if err == nil {
		err = requireReadyExport(current)
	}
	if err == nil && current.Artifact != result.Artifact {
		err = apperror.New("exam.export.unavailable").Wrap(errors.New("export artifact changed during open"))
	}
	if err != nil {
		_ = body.Close()
		return nil, err
	}
	return &ExportDownload{Export: current.Export, Body: body}, nil
}

func requireReadyExport(result *store.ExamExportDownload) error {
	if result.Export.State == model.ExamExportExpired {
		return apperror.New("exam.export.expired")
	}
	if result.Export.State != model.ExamExportReady {
		return apperror.New("exam.export.not_ready")
	}
	if result.Artifact.ExportID != result.Export.ID || !result.Artifact.AttemptID.IsValid() {
		return apperror.New("exam.export.unavailable").Wrap(errors.New("invalid export artifact"))
	}
	return nil
}

// BuildFromJob builds and publishes the archive for one queued Examination
// Export on behalf of its build Job. An export that is already ready is a
// no-op, and the build is bounded by the source expiry.
func (s *Exports) BuildFromJob(ctx context.Context, input store.ExamExportBuild) (err error) {
	snapshot, err := s.deps.Persistence.BeginBuild(ctx, &input)
	if err != nil {
		return examExportStoreErrors.Translate(err)
	}
	if snapshot == nil || snapshot.Export.Validate() != nil || snapshot.Export.ID != input.ExportID {
		return apperror.New("exam.export.unavailable").Wrap(errors.New("invalid export build snapshot"))
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
		if finishErr := s.deps.Persistence.FinishWriter(cleanup, input.ExamExportArtifact); finishErr != nil && err == nil {
			err = apperror.New("exam.export.unavailable").Wrap(finishErr)
		}
	}()
	if snapshot.Export.State != model.ExamExportQueued || len(snapshot.Records) < 1 || len(snapshot.Records) > model.ExamExportMaximumSnapshotBytes || len(snapshot.Files) != snapshot.Export.FileCount {
		return apperror.New("exam.export.unavailable").Wrap(errors.New("invalid export build input"))
	}
	buildCtx, cancel := context.WithDeadline(ctx, snapshot.Export.SourceExpiresAt)
	defer cancel()
	archive := ExportArchiveInput{ExportID: input.ExportID, AttemptID: input.AttemptID, Records: snapshot.Records}
	for _, f := range snapshot.Files {
		archive.Files = append(archive.Files, ExportArchiveFile{SubmissionID: f.SubmissionID, Entry: f.Entry})
	}
	content, err := s.deps.Content.BuildExamExport(buildCtx, archive)
	if err != nil {
		return apperror.New("exam.export.unavailable").Wrap(err)
	}
	if content == nil || !model.ValidExamExportContent(content.SizeBytes, content.SHA256) {
		return apperror.New("exam.export.unavailable").Wrap(errors.New("invalid verified export content"))
	}
	if err = s.deps.Persistence.Publish(buildCtx, &store.ExamExportPublication{ExamExportBuild: input, SizeBytes: content.SizeBytes, SHA256: content.SHA256}); err != nil {
		return examExportStoreErrors.Translate(err)
	}
	return nil
}

var examExportStoreErrors = apperror.StoreTable{
	Rules: append([]apperror.StoreRule{{Kind: apperror.StoreConflict, Entity: "authorization", Code: "exam.export.not_found"}},
		apperror.Conflicts("exam_export", map[string]apperror.Code{
			"policy_unconfigured": "exam.export.policy_unconfigured",
			"limit":               "exam.export.limit",
			"source_retired":      "exam.export.source_retired",
			"scope_changed":       "exam.export.scope_changed",
			"expired":             "exam.export.expired",
			"claim_lost":          "exam.export.claim_lost",
		})...),
	NotFound:    "exam.export.not_found",
	Conflict:    "exam.export.conflict",
	Unavailable: "exam.export.unavailable",
}
