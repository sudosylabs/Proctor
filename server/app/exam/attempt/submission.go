// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"
	"io"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SubmitCommand struct {
	Access                    WorkspaceMutationAccess
	ExpectedCurrentRevisionID model.ExamRevisionID
	ExpectedWorkspaceCursor   int64
	FinalFocusLossSequence    int64
	BrowserActivity           model.BrowserActivitySubmission
	IdempotencyKey            string
}

type SubmissionResult struct {
	Receipt         store.ExamSubmissionReceipt
	Provenance      model.ExamSubmissionProvenance
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	ClassID         model.ClassID
	CandidateUserID model.UserID
	ParticipationID model.AttemptParticipationID
	Generation      int64
	ConnectionID    model.AttemptConnectionID
	Replayed        bool
}

type GetSubmissionQuery struct {
	ExamID       model.ExamID
	SittingID    model.ExamSittingID
	AttemptID    model.ExamAttemptID
	SubmissionID model.SubmissionID
}

type ManagedSubmission struct {
	Authorization store.ExamSubmissionAuthorization
	Submission    model.ExamSubmission
}

type ListSubmissionManifestQuery struct {
	GetSubmissionQuery
	AfterEntryID model.AttemptWorkspaceEntryID
	Limit        int
}

type SubmissionManifestPage struct {
	SubmissionID    model.SubmissionID
	WorkspaceCursor int64
	ManifestDigest  string
	Items           []store.ExamSubmissionManifestItem
	HasMore         bool
}

type OpenSubmissionFileQuery struct {
	GetSubmissionQuery
	EntryID model.AttemptWorkspaceEntryID
}

func (service *Service) OpenSubmissionFile(ctx context.Context, call Call,
	query OpenSubmissionFileQuery,
) (*OpenedContent, error) {
	if !query.EntryID.IsValid() {
		return nil, invalid("submission_entry_id")
	}
	if _, err := service.resolveManagedSubmission(ctx, call, query.GetSubmissionQuery); err != nil {
		return nil, err
	}
	selector, err := service.deps.Submissions.ResolveFile(ctx, query.SubmissionID, query.EntryID)
	if err != nil {
		return nil, mapStore(err)
	}
	if !validSubmissionFileSelector(selector, query.EntryID) {
		return nil, unavailable(errors.New("inconsistent Submission file selector"))
	}
	var body io.ReadCloser
	switch selector.StorageOrigin {
	case model.AttemptWorkspaceStorageStarter:
		body, err = service.deps.Content.OpenStarterWorkspaceObject(ctx, selector.StarterObjectID)
	case model.AttemptWorkspaceStorageAttempt:
		body, err = service.deps.Content.OpenAttemptWorkspaceObject(ctx, selector.AttemptObjectID)
	default:
		return nil, unavailable(errors.New("invalid Submission file storage origin"))
	}
	if err != nil {
		return nil, unavailable(err)
	}
	return &OpenedContent{Body: body, MediaType: selector.Entry.MediaType, SizeBytes: selector.Entry.SizeBytes,
		SHA256: selector.Entry.SHA256, ContentVersion: selector.ContentVersion}, nil
}

func validSubmissionFileSelector(selector *store.ExamSubmissionFileSelector,
	entryID model.AttemptWorkspaceEntryID,
) bool {
	if selector == nil || selector.Entry.EntryID != entryID || selector.Entry.Kind != model.StarterWorkspaceEntryFile ||
		!validSubmissionManifestItem(selector.Entry) || selector.ContentVersion != selector.Entry.ContentVersion {
		return false
	}
	switch selector.StorageOrigin {
	case model.AttemptWorkspaceStorageStarter:
		return selector.StarterObjectID.IsValid() && selector.AttemptObjectID.IsZero()
	case model.AttemptWorkspaceStorageAttempt:
		return selector.AttemptObjectID.IsValid() && selector.StarterObjectID.IsZero()
	default:
		return false
	}
}

func (service *Service) ListSubmissionManifest(ctx context.Context, call Call,
	query ListSubmissionManifestQuery,
) (SubmissionManifestPage, error) {
	if query.Limit < 1 || query.Limit > model.ExamSubmissionManifestReadMaximum {
		return SubmissionManifestPage{}, invalid("submission_manifest")
	}
	if _, err := service.resolveManagedSubmission(ctx, call, query.GetSubmissionQuery); err != nil {
		return SubmissionManifestPage{}, err
	}
	stored, err := service.deps.Submissions.ListManifest(ctx, store.ExamSubmissionManifestListOptions{
		SubmissionID: query.SubmissionID, AfterEntryID: query.AfterEntryID, Limit: query.Limit,
	})
	if err != nil {
		return SubmissionManifestPage{}, mapStore(err)
	}
	if !validSubmissionManifestPage(stored, query) {
		return SubmissionManifestPage{}, unavailable(errors.New("inconsistent Submission manifest page"))
	}
	items := append([]store.ExamSubmissionManifestItem(nil), stored.Items...)
	return SubmissionManifestPage{SubmissionID: stored.SubmissionID, WorkspaceCursor: stored.WorkspaceCursor,
		ManifestDigest: stored.ManifestDigest, Items: items, HasMore: stored.HasMore}, nil
}

func validSubmissionManifestPage(page *store.ExamSubmissionManifestPage, query ListSubmissionManifestQuery) bool {
	if page == nil || page.SubmissionID != query.SubmissionID || page.WorkspaceCursor < 0 ||
		!validWorkspaceSHA256(page.ManifestDigest) || len(page.Items) > query.Limit ||
		(page.HasMore && len(page.Items) != query.Limit) {
		return false
	}
	previous := query.AfterEntryID
	for _, item := range page.Items {
		if !validSubmissionManifestItem(item) || (!previous.IsZero() && item.EntryID.String() <= previous.String()) {
			return false
		}
		previous = item.EntryID
	}
	return true
}

func validSubmissionManifestItem(item store.ExamSubmissionManifestItem) bool {
	if !item.EntryID.IsValid() {
		return false
	}
	path, err := model.NormalizeAttemptWorkspacePath(item.Path)
	if err != nil || path != item.Path {
		return false
	}
	switch item.Kind {
	case model.StarterWorkspaceEntryDirectory:
		return item.ContentVersion.IsZero() && item.MediaType == "" && item.SizeBytes == 0 && item.SHA256 == ""
	case model.StarterWorkspaceEntryFile:
		return item.ContentVersion.IsValid() && (model.AttemptWorkspaceContent{MediaType: item.MediaType,
			SizeBytes: item.SizeBytes, SHA256: item.SHA256}).Validate() == nil
	default:
		return false
	}
}

func (service *Service) GetSubmission(ctx context.Context, call Call, query GetSubmissionQuery) (*ManagedSubmission, error) {
	authorization, err := service.resolveManagedSubmission(ctx, call, query)
	if err != nil {
		return nil, err
	}
	submission, err := service.deps.Submissions.Get(ctx, query.SubmissionID)
	if err != nil {
		return nil, mapStore(err)
	}
	if submission == nil || submission.Validate() != nil || submission.ID != query.SubmissionID ||
		submission.AttemptID != query.AttemptID {
		return nil, unavailable(errors.New("inconsistent managed Submission"))
	}
	return &ManagedSubmission{Authorization: *authorization, Submission: *submission}, nil
}

func (service *Service) resolveManagedSubmission(ctx context.Context, call Call,
	query GetSubmissionQuery,
) (*store.ExamSubmissionAuthorization, error) {
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || !query.AttemptID.IsValid() || !query.SubmissionID.IsValid() {
		return nil, invalid("submission")
	}
	if err := service.deps.Managers.AuthorizeSubmissionView(ctx, call, query.SubmissionID); err != nil {
		return nil, err
	}
	authorization, err := service.deps.Submissions.Resolve(ctx, query.SubmissionID)
	if err != nil {
		return nil, mapStore(err)
	}
	if !validSubmissionAuthorization(authorization, query.SubmissionID) {
		return nil, unavailable(errors.New("inconsistent Submission authorization projection"))
	}
	if authorization.ExamID != query.ExamID || authorization.SittingID != query.SittingID ||
		authorization.AttemptID != query.AttemptID {
		return nil, &Fault{Code: "exam.attempt.not_found"}
	}
	return authorization, nil
}

func validSubmissionAuthorization(value *store.ExamSubmissionAuthorization, submissionID model.SubmissionID) bool {
	return value != nil && value.SubmissionID == submissionID && value.ExamID.IsValid() && value.SittingID.IsValid() &&
		value.AttemptID.IsValid() && value.CandidateUserID.IsValid() && value.AcademicUnitID.IsValid()
}

func (service *Service) Submit(ctx context.Context, call Call, command SubmitCommand) (SubmissionResult, error) {
	workspaceAccess, err := workspaceMutationSelector(call, command.Access)
	if err != nil {
		return SubmissionResult{}, err
	}
	if !command.ExpectedCurrentRevisionID.IsValid() || command.ExpectedWorkspaceCursor < 0 || command.FinalFocusLossSequence < 0 ||
		command.BrowserActivity.ValidateClient() != nil {
		return SubmissionResult{}, invalid("submission")
	}
	idempotency, err := prepareSubmissionIdempotency(call, command.IdempotencyKey, workspaceAccess.AttemptID,
		command.ExpectedCurrentRevisionID, command.ExpectedWorkspaceCursor, command.FinalFocusLossSequence, command.BrowserActivity)
	if err != nil {
		return SubmissionResult{}, err
	}
	access := store.ExamSubmissionSealAccess{AttemptID: workspaceAccess.AttemptID,
		ParticipationID: workspaceAccess.ParticipationID, Generation: workspaceAccess.Generation,
		ConnectionID: workspaceAccess.ConnectionID, CandidateUserID: workspaceAccess.CandidateUserID,
		SessionID: workspaceAccess.SessionID, ContinuityCredentialHash: workspaceAccess.ContinuityCredentialHash,
		ExpectedCurrentRevisionID: command.ExpectedCurrentRevisionID, ExpectedWorkspaceCursor: command.ExpectedWorkspaceCursor,
		FinalFocusLossSequence: command.FinalFocusLossSequence, BrowserActivity: command.BrowserActivity.Clone()}
	target, err := service.deps.Submissions.ResolveSealTarget(ctx, access)
	if err != nil {
		return SubmissionResult{}, mapStore(err)
	}
	if !validSubmissionTarget(target, access) {
		return SubmissionResult{}, unavailable(errors.New("inconsistent Submission seal target"))
	}
	submissionID := service.deps.NewSubmission()
	if !submissionID.IsValid() {
		return SubmissionResult{}, invalid("submission_id")
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate,
		model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass,
		target.ClassID.String(), store.ExamSubmissionSealOperation,
		map[string]any{"exam_attempt_id": access.AttemptID.String()})
	if err != nil {
		return SubmissionResult{}, err
	}
	at := target.SealAt
	var notice *store.PreparedMail
	var expectedRecipientRevision int64
	if !target.Replayed {
		preparedMail, prepareErr := service.deps.Mail.PrepareSubmissionReceipt(ctx, SubmissionMailPreparation{
			CandidateUserID: target.CandidateUserID, ExamID: target.ExamID, SittingID: target.SittingID,
			SubmissionID: submissionID, SealedAt: at, Provenance: model.ExamSubmissionCandidateSubmitted,
		})
		if prepareErr != nil || preparedMail == nil || preparedMail.Notice == nil || preparedMail.ExpectedRecipientRevision < 1 {
			if prepareErr == nil {
				prepareErr = errors.New("invalid Submission receipt mail preparation")
			}
			return SubmissionResult{}, service.failAudit(ctx, auditID, unavailable(prepareErr))
		}
		notice, expectedRecipientRevision = preparedMail.Notice, preparedMail.ExpectedRecipientRevision
	}
	stored, err := service.deps.Submissions.Seal(ctx, &store.ExamSubmissionSeal{SubmissionID: submissionID,
		Access: access, AuditEventID: auditID, AuditAt: model.MillisFromTime(at), Notice: notice,
		ExpectedRecipientRevision: expectedRecipientRevision}, idempotency)
	if err != nil {
		return SubmissionResult{}, service.failAudit(ctx, auditID, err)
	}
	result, err := projectSubmissionResult(stored, target, access, submissionID)
	if err != nil {
		return SubmissionResult{}, err
	}
	if !result.Replayed {
		if effectErr := service.deps.Effects.AttemptSubmitted(ctx, result); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "exam_attempt_submitted", effectErr)
		}
	}
	return result, nil
}

func validSubmissionTarget(target *store.ExamSubmissionSealTarget, access store.ExamSubmissionSealAccess) bool {
	return target != nil && target.ExamID.IsValid() && target.SittingID.IsValid() && target.ClassID.IsValid() &&
		target.CandidateUserID == access.CandidateUserID && target.WorkspaceID.IsValid() &&
		target.CurrentRevisionID == access.ExpectedCurrentRevisionID && !target.SealAt.IsZero()
}

func projectSubmissionResult(stored *store.ExamSubmissionSealResult, target *store.ExamSubmissionSealTarget,
	access store.ExamSubmissionSealAccess, proposedID model.SubmissionID,
) (SubmissionResult, error) {
	if stored == nil || !stored.Receipt.SubmissionID.IsValid() || stored.Receipt.AttemptID != access.AttemptID ||
		stored.Receipt.State != model.ExamAttemptSubmitted || stored.Receipt.WorkspaceCursor != access.ExpectedWorkspaceCursor ||
		stored.Receipt.ExamRevisionID != access.ExpectedCurrentRevisionID ||
		!validWorkspaceSHA256(stored.Receipt.ManifestDigest) || stored.Receipt.SubmittedAt.IsZero() ||
		stored.ExamID != target.ExamID || stored.SittingID != target.SittingID || stored.ClassID != target.ClassID ||
		stored.CandidateUserID != target.CandidateUserID || stored.ParticipationID != access.ParticipationID ||
		stored.Generation != access.Generation || stored.ConnectionID != access.ConnectionID ||
		(!stored.Replayed && stored.Receipt.SubmissionID != proposedID) {
		return SubmissionResult{}, unavailable(errors.New("inconsistent Submission seal result"))
	}
	return SubmissionResult{Receipt: stored.Receipt, Provenance: model.ExamSubmissionCandidateSubmitted,
		ExamID: stored.ExamID, SittingID: stored.SittingID,
		ClassID: stored.ClassID, CandidateUserID: stored.CandidateUserID, ParticipationID: stored.ParticipationID,
		Generation: stored.Generation, ConnectionID: stored.ConnectionID, Replayed: stored.Replayed}, nil
}
