// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamResourceStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	institution := saveInstitution(t, ctx, ss)
	unit := saveAcademicUnit(t, ctx, ss, institution.ID.String(), "", "resource-unit")
	creator := saveUser(t, ctx, ss)
	at := model.NowUTC()
	created := createCatalogExam(t, ctx, ss, unit.ID, creator.ID, at, "resource-exam")
	examID := created.Value.Exam.ID
	first := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Reference", 0, 1, at.Add(time.Second), model.ExamResourceMediaPDF, nil)
	command := examCommand(creator.ID, "exam.resource.add.v1", "resource-add", "resource-add-command")
	finalized, err := ss.ExamResource().FinalizeUpload(ctx, first.finalization, command)
	requireNoError(t, err)
	if finalized.Replayed || finalized.Value.DraftRevision != 2 || finalized.Value.Resource.Position != 0 || finalized.Value.Rendition.Name != "original" {
		t.Fatalf("finalized=%#v", finalized)
	}
	replay := *first.finalization
	replay.AuditEventID = saveExamResourceAudit(t, ctx, ss, examID, creator.ID, unit.ID).ID.String()
	replay.AuditAt = model.GetMillis()
	replayed, err := ss.ExamResource().FinalizeUpload(ctx, &replay, command)
	requireNoError(t, err)
	if !replayed.Replayed || replayed.Value.Resource.ID != first.finalization.Resource.ID {
		t.Fatalf("replay=%#v", replayed)
	}
	retryReservation := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Reference", 0, 1, at.Add(2*time.Second), model.ExamResourceMediaPDF, nil)
	retried, err := ss.ExamResource().FinalizeUpload(ctx, retryReservation.finalization, command)
	requireNoError(t, err)
	if !retried.Replayed || retried.Value.Resource.ID != finalized.Value.Resource.ID || retried.Value.DraftRevision != 2 {
		t.Fatalf("post-success create retry=%#v first=%#v", retried, finalized)
	}

	listed, err := ss.ExamResource().List(ctx, examID)
	requireNoError(t, err)
	if len(listed) != 1 || listed[0].Resource.ID != first.finalization.Resource.ID {
		t.Fatalf("list=%#v", listed)
	}
	got, err := ss.ExamResource().Get(ctx, examID, first.finalization.Resource.ID)
	requireNoError(t, err)
	if got.Rendition.SHA256 != first.finalization.Rendition.SHA256 {
		t.Fatalf("get=%#v", got)
	}

	replacement := reserveExamResource(t, ctx, ss, examID, creator.ID, first.finalization.Resource.ID, "Reference", 0, 2, at.Add(3*time.Second), model.ExamResourceMediaText, got)
	replacementCommand := examCommand(creator.ID, "exam.resource.replace.v1", "resource-replace", "resource-replace-command")
	replaced, err := ss.ExamResource().FinalizeUpload(ctx, replacement.finalization, replacementCommand)
	requireNoError(t, err)
	if replaced.Value.Resource.FileEntryID != got.Resource.FileEntryID || replaced.Value.Resource.SelectedFileRevisionID == got.Resource.SelectedFileRevisionID || replaced.Value.DraftRevision != 3 {
		t.Fatalf("replaced=%#v old=%#v", replaced, got)
	}
	replacementRetry := reserveExamResource(t, ctx, ss, examID, creator.ID, first.finalization.Resource.ID, "Reference", 0, 2, at.Add(4*time.Second), model.ExamResourceMediaText, got)
	retriedReplacement, err := ss.ExamResource().FinalizeUpload(ctx, replacementRetry.finalization, replacementCommand)
	requireNoError(t, err)
	if !retriedReplacement.Replayed || retriedReplacement.Value.Resource.SelectedFileRevisionID != replaced.Value.Resource.SelectedFileRevisionID || retriedReplacement.Value.DraftRevision != 3 {
		t.Fatalf("post-success replacement retry=%#v first=%#v", retriedReplacement, replaced)
	}
	metadata := &store.ExamResourceMetadataUpdate{ExamID: examID, ActorUserID: creator.ID, ExpectedDraftRevision: 3, ResourceID: got.Resource.ID, DisplayName: "Updated", DescriptionMarkdown: "New **description**", ChangedAt: at.Add(3 * time.Second), AuditEventID: saveExamResourceAudit(t, ctx, ss, examID, creator.ID, unit.ID).ID.String(), AuditAt: model.MillisFromTime(at.Add(3 * time.Second))}
	updated, err := ss.ExamResource().UpdateMetadata(ctx, metadata, examCommand(creator.ID, "exam.resource.metadata.v1", "resource-metadata", "resource-metadata-command"))
	requireNoError(t, err)
	if updated.Value.Resource.DisplayName != "Updated" || updated.Value.DraftRevision != 4 {
		t.Fatalf("updated=%#v", updated)
	}

	second := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Second", 1, 4, at.Add(4*time.Second), model.ExamResourceMediaText, nil)
	secondResult, err := ss.ExamResource().FinalizeUpload(ctx, second.finalization, examCommand(creator.ID, "exam.resource.add.v1", "resource-second", "resource-second-command"))
	requireNoError(t, err)
	reorder := &store.ExamResourceReorder{ExamID: examID, ActorUserID: creator.ID, ExpectedDraftRevision: 5, ResourceIDs: []model.ExamResourceID{secondResult.Value.Resource.ID, updated.Value.Resource.ID}, ChangedAt: at.Add(5 * time.Second), AuditEventID: saveExamResourceAudit(t, ctx, ss, examID, creator.ID, unit.ID).ID.String(), AuditAt: model.MillisFromTime(at.Add(5 * time.Second))}
	ordered, err := ss.ExamResource().Reorder(ctx, reorder, examCommand(creator.ID, "exam.resource.reorder.v1", "resource-order", "resource-order-command"))
	requireNoError(t, err)
	if len(ordered.Items) != 2 || ordered.Items[0].Resource.ID != secondResult.Value.Resource.ID || ordered.Items[1].Resource.Position != 1 {
		t.Fatalf("ordered=%#v", ordered)
	}
	removal := &store.ExamResourceRemoval{ExamID: examID, ActorUserID: creator.ID, ExpectedDraftRevision: 6, ResourceID: secondResult.Value.Resource.ID, ChangedAt: at.Add(6 * time.Second), AuditEventID: saveExamResourceAudit(t, ctx, ss, examID, creator.ID, unit.ID).ID.String(), AuditAt: model.MillisFromTime(at.Add(6 * time.Second))}
	removed, err := ss.ExamResource().Remove(ctx, removal, examCommand(creator.ID, "exam.resource.remove.v1", "resource-remove", "resource-remove-command"))
	requireNoError(t, err)
	if !removed.Value.Resource.IsArchived() || removed.Value.DraftRevision != 7 {
		t.Fatalf("removed=%#v", removed)
	}
	remaining, err := ss.ExamResource().List(ctx, examID)
	requireNoError(t, err)
	if len(remaining) != 1 || remaining[0].Resource.Position != 0 || remaining[0].Resource.ID != updated.Value.Resource.ID {
		t.Fatalf("remaining=%#v", remaining)
	}

	// A validation failure or an unknown VFS/write acknowledgement after the
	// durable reservation deliberately leaves a pending revision. It must stay
	// invisible during the safety window and then enter the shared claimed
	// revision-prefix cleanup flow without an Exam Resource-specific object key.
	abandonedAt := model.NowUTC().Add(-27 * time.Hour)
	abandoned := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Abandoned", 1, 7, abandonedAt, model.ExamResourceMediaText, nil)
	visible, err := ss.ExamResource().List(ctx, examID)
	requireNoError(t, err)
	if len(visible) != 1 {
		t.Fatalf("pending reservation became visible: %#v", visible)
	}
	candidates, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	requireNoError(t, err)
	found := false
	for _, candidate := range candidates.Candidates {
		if candidate.Kind == store.FilePurgeCandidateExpiredLease && candidate.LeaseID == abandoned.finalization.LeaseID && candidate.RevisionID == abandoned.finalization.Resource.SelectedFileRevisionID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("abandoned Exam Resource upload is not cleanup-eligible: %#v", candidates.Candidates)
	}
}

type reservedExamResource struct {
	finalization *store.ExamResourceUploadFinalization
}

func reserveExamResource(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, resourceID model.ExamResourceID, name string, position int, expected int64, at time.Time, media model.ExamResourceMediaType, current *store.ExamResourceRecord) reservedExamResource {
	t.Helper()
	entryID := model.NewFileEntryID()
	replacement := current != nil
	var entry *model.FileEntry
	if replacement {
		entryID = current.Resource.FileEntryID
	} else {
		var err error
		entry, err = model.NewFileEntryForPurpose(entryID, model.FilePurposeExamResource, model.FileIndexingNone, at)
		requireNoError(t, err)
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	requireNoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, actorID, at, at.Add(time.Hour))
	requireNoError(t, err)
	_, err = ss.ExamResource().ReserveUpload(ctx, &store.ExamResourceUploadReservation{ExamID: examID, ActorUserID: actorID, ExpectedDraftRevision: expected, ResourceID: resourceID, Entry: entry, EntryID: func() model.FileEntryID {
		if replacement {
			return entryID
		}
		return ""
	}(), Revision: revision, Lease: lease, Replacement: replacement})
	requireNoError(t, err)
	resource, err := model.NewExamResource(resourceID, examID, entryID, revision.ID, name, "", position, at)
	requireNoError(t, err)
	if replacement {
		copy := *current.Resource
		resource = &copy
		_, err = resource.ReplaceContent(revision.ID, at)
		requireNoError(t, err)
	}
	rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, "original", string(media), 8, 0, 0, strings.Repeat("a", 64), at)
	requireNoError(t, err)
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	requireNoError(t, err)
	audit := saveExamResourceAudit(t, ctx, ss, examID, actorID, exam.AcademicUnitID)
	return reservedExamResource{finalization: &store.ExamResourceUploadFinalization{ExamID: examID, ActorUserID: actorID, ExpectedDraftRevision: expected, Resource: resource, LeaseID: lease.ID, Rendition: rendition, ChangedAt: at, AuditEventID: audit.ID.String(), AuditAt: model.MillisFromTime(at)}}
}
func saveExamResourceAudit(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, unitID model.AcademicUnitID) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actorID, Action: string(model.ActionExamManage), Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	requireNoError(t, err)
	return audit
}
