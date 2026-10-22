// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
	command := examCommand(creator.ID, "exam.resource.add", "resource-add", "resource-add-command")
	finalized, err := ss.ExamResource().FinalizeUpload(ctx, first.finalization, command)
	require.NoError(t, err)
	require.False(t, finalized.Replayed)
	require.Equal(t, int64(2), finalized.Value.DraftRevision)
	require.Equal(t, 0, finalized.Value.Resource.Position)
	require.Equal(t, "original", finalized.Value.Rendition.Name)
	replay := *first.finalization
	replay.AuditEventID = saveExamResourceAudit(t, ctx, ss, examID, creator.ID, unit.ID).ID
	replay.AuditAt = model.NowUTC()
	replayed, err := ss.ExamResource().FinalizeUpload(ctx, &replay, command)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, first.finalization.Resource.ID, replayed.Value.Resource.ID)
	retryReservation := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Reference", 0, 1, at.Add(2*time.Second), model.ExamResourceMediaPDF, nil)
	retried, err := ss.ExamResource().FinalizeUpload(ctx, retryReservation.finalization, command)
	require.NoError(t, err)
	require.True(t, retried.Replayed, "post-success create retry=%#v first=%#v", retried, finalized)
	require.Equal(t, finalized.Value.Resource.ID, retried.Value.Resource.ID, "post-success create retry=%#v first=%#v", retried, finalized)
	require.Equal(t, int64(2), retried.Value.DraftRevision, "post-success create retry=%#v first=%#v", retried, finalized)

	listed, err := ss.ExamResource().List(ctx, examID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, first.finalization.Resource.ID, listed[0].Resource.ID)
	got, err := ss.ExamResource().Get(ctx, examID, first.finalization.Resource.ID)
	require.NoError(t, err)
	require.Equal(t, first.finalization.Rendition.SHA256, got.Rendition.SHA256)

	replacement := reserveExamResource(t, ctx, ss, examID, creator.ID, first.finalization.Resource.ID, "Reference", 0, 2, at.Add(3*time.Second), model.ExamResourceMediaText, got)
	replacementCommand := examCommand(creator.ID, "exam.resource.replace", "resource-replace", "resource-replace-command")
	replaced, err := ss.ExamResource().FinalizeUpload(ctx, replacement.finalization, replacementCommand)
	require.NoError(t, err)
	require.Equal(t, got.Resource.FileEntryID, replaced.Value.Resource.FileEntryID, "replaced=%#v old=%#v", replaced, got)
	require.NotEqual(t, got.Resource.SelectedFileRevisionID, replaced.Value.Resource.SelectedFileRevisionID, "replaced=%#v old=%#v", replaced, got)
	require.Equal(t, int64(3), replaced.Value.DraftRevision, "replaced=%#v old=%#v", replaced, got)
	replacementRetry := reserveExamResource(t, ctx, ss, examID, creator.ID, first.finalization.Resource.ID, "Reference", 0, 2, at.Add(4*time.Second), model.ExamResourceMediaText, got)
	retriedReplacement, err := ss.ExamResource().FinalizeUpload(ctx, replacementRetry.finalization, replacementCommand)
	require.NoError(t, err)
	require.True(t, retriedReplacement.Replayed, "post-success replacement retry=%#v first=%#v", retriedReplacement, replaced)
	require.Equal(t, replaced.Value.Resource.SelectedFileRevisionID, retriedReplacement.Value.Resource.SelectedFileRevisionID, "post-success replacement retry=%#v first=%#v", retriedReplacement, replaced)
	require.Equal(t, int64(3), retriedReplacement.Value.DraftRevision, "post-success replacement retry=%#v first=%#v", retriedReplacement, replaced)
	metadata := &store.ExamResourceMetadataUpdate{ExamID: examID, ActorUserID: creator.ID, ExpectedDraftRevision: 3, ResourceID: got.Resource.ID, DisplayName: "Updated", DescriptionMarkdown: "New **description**", ChangedAt: at.Add(3 * time.Second), AuditEventID: saveExamResourceAudit(t, ctx, ss, examID, creator.ID, unit.ID).ID, AuditAt: at.Add(3 * time.Second)}
	updated, err := ss.ExamResource().UpdateMetadata(ctx, metadata, examCommand(creator.ID, "exam.resource.metadata", "resource-metadata", "resource-metadata-command"))
	require.NoError(t, err)
	require.Equal(t, "Updated", updated.Value.Resource.DisplayName)
	require.Equal(t, int64(4), updated.Value.DraftRevision)

	second := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Second", 1, 4, at.Add(4*time.Second), model.ExamResourceMediaText, nil)
	secondResult, err := ss.ExamResource().FinalizeUpload(ctx, second.finalization, examCommand(creator.ID, "exam.resource.add", "resource-second", "resource-second-command"))
	require.NoError(t, err)
	reorder := &store.ExamResourceReorder{ExamID: examID, ActorUserID: creator.ID, ExpectedDraftRevision: 5, ResourceIDs: []model.ExamResourceID{secondResult.Value.Resource.ID, updated.Value.Resource.ID}, ChangedAt: at.Add(5 * time.Second), AuditEventID: saveExamResourceAudit(t, ctx, ss, examID, creator.ID, unit.ID).ID, AuditAt: at.Add(5 * time.Second)}
	ordered, err := ss.ExamResource().Reorder(ctx, reorder, examCommand(creator.ID, "exam.resource.reorder", "resource-order", "resource-order-command"))
	require.NoError(t, err)
	require.Len(t, ordered.Items, 2)
	require.Equal(t, secondResult.Value.Resource.ID, ordered.Items[0].Resource.ID)
	require.Equal(t, 1, ordered.Items[1].Resource.Position)
	policy := model.DefaultExamCapacityPolicy()
	policy.ResourceMaximumCount = 1
	institution.ExamCapacity = policy
	institution, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	overCountReplacement := reserveExamResource(t, ctx, ss, examID, creator.ID, updated.Value.Resource.ID,
		"Updated", 1, 6, at.Add(5500*time.Millisecond), model.ExamResourceMediaText, &ordered.Items[1])
	_, err = ss.ExamResource().FinalizeUpload(ctx, overCountReplacement.finalization,
		examCommand(creator.ID, "exam.resource.replace", "resource-over-count-replacement", "resource-over-count-replacement-command"))
	var capacityConflict *store.ErrConflict
	require.ErrorAs(t, err, &capacityConflict)
	require.Equal(t, "exam_resource_limit", capacityConflict.Constraint)
	institution.ExamCapacity = model.DefaultExamCapacityPolicy()
	institution, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	removal := &store.ExamResourceRemoval{ExamID: examID, ActorUserID: creator.ID, ExpectedDraftRevision: 6, ResourceID: secondResult.Value.Resource.ID, ChangedAt: at.Add(6 * time.Second), AuditEventID: saveExamResourceAudit(t, ctx, ss, examID, creator.ID, unit.ID).ID, AuditAt: at.Add(6 * time.Second)}
	removed, err := ss.ExamResource().Remove(ctx, removal, examCommand(creator.ID, "exam.resource.remove", "resource-remove", "resource-remove-command"))
	require.NoError(t, err)
	require.True(t, removed.Value.Resource.IsArchived())
	require.Equal(t, int64(7), removed.Value.DraftRevision)
	remaining, err := ss.ExamResource().List(ctx, examID)
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	require.Equal(t, 0, remaining[0].Resource.Position)
	require.Equal(t, updated.Value.Resource.ID, remaining[0].Resource.ID)

	policy = model.DefaultExamCapacityPolicy()
	policy.ResourceMaximumBytes = 7
	institution.ExamCapacity = policy
	institution, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	oversized := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Oversized", 1, 7, at.Add(7*time.Second), model.ExamResourceMediaText, nil)
	_, err = ss.ExamResource().FinalizeUpload(ctx, oversized.finalization,
		examCommand(creator.ID, "exam.resource.add", "resource-oversized", "resource-oversized-command"))
	require.ErrorAs(t, err, &capacityConflict)
	require.Equal(t, "exam_resource_limit", capacityConflict.Constraint)

	policy = model.DefaultExamCapacityPolicy()
	policy.ResourceMaximumCount = 1
	institution.ExamCapacity = policy
	institution, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)
	overCount := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Over count", 1, 7, at.Add(8*time.Second), model.ExamResourceMediaText, nil)
	_, err = ss.ExamResource().FinalizeUpload(ctx, overCount.finalization,
		examCommand(creator.ID, "exam.resource.add", "resource-over-count", "resource-over-count-command"))
	require.ErrorAs(t, err, &capacityConflict)
	require.Equal(t, "exam_resource_limit", capacityConflict.Constraint)
	institution.ExamCapacity = model.DefaultExamCapacityPolicy()
	_, err = UpdateInstitution(ctx, ss, institution)
	require.NoError(t, err)

	// A validation failure or an unknown VFS/write acknowledgement after the
	// durable reservation deliberately leaves a pending revision. It must stay
	// invisible during the safety window and then enter the shared claimed
	// revision-prefix cleanup flow without an Exam Resource-specific object key.
	abandonedAt := model.NowUTC().Add(-27 * time.Hour)
	abandoned := reserveExamResource(t, ctx, ss, examID, creator.ID, model.NewExamResourceID(), "Abandoned", 1, 7, abandonedAt, model.ExamResourceMediaText, nil)
	visible, err := ss.ExamResource().List(ctx, examID)
	require.NoError(t, err)
	require.Len(t, visible, 1)
	candidates, err := ss.File().ListPurgeCandidates(ctx, &store.FilePurgeCandidateRequest{Limit: 100})
	require.NoError(t, err)
	found := false
	for _, candidate := range candidates.Candidates {
		if candidate.Kind == store.FilePurgeCandidateExpiredLease && candidate.LeaseID == abandoned.finalization.LeaseID && candidate.RevisionID == abandoned.finalization.Resource.SelectedFileRevisionID {
			found = true
			break
		}
	}
	require.True(t, found, "abandoned Exam Resource upload is not cleanup-eligible: %#v", candidates.Candidates)
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
		require.NoError(t, err)
	}
	revision, err := model.NewFileRevision(model.NewFileRevisionID(), entryID, model.FileAvailabilityPending, model.FileIndexingNotRequired, at)
	require.NoError(t, err)
	lease, err := model.NewUploadLease(model.NewUploadLeaseID(), revision.ID, actorID, at, at.Add(time.Hour))
	require.NoError(t, err)
	_, err = ss.ExamResource().ReserveUpload(ctx, &store.ExamResourceUploadReservation{ExamID: examID, ActorUserID: actorID, ExpectedDraftRevision: expected, ResourceID: resourceID, Entry: entry, EntryID: func() model.FileEntryID {
		if replacement {
			return entryID
		}
		return ""
	}(), Revision: revision, Lease: lease, Replacement: replacement})
	require.NoError(t, err)
	resource, err := model.NewExamResource(resourceID, examID, entryID, revision.ID, name, "", position, at)
	require.NoError(t, err)
	if replacement {
		copy := *current.Resource
		resource = &copy
		_, err = resource.ReplaceContent(revision.ID, at)
		require.NoError(t, err)
	}
	rendition, err := model.NewFileRendition(model.NewFileRenditionID(), revision.ID, "original", string(media), 8, 0, 0, strings.Repeat("a", 64), at)
	require.NoError(t, err)
	exam, err := ss.ExamAuthoring().Resolve(ctx, examID)
	require.NoError(t, err)
	audit := saveExamResourceAudit(t, ctx, ss, examID, actorID, exam.AcademicUnitID)
	return reservedExamResource{finalization: &store.ExamResourceUploadFinalization{ExamID: examID, ActorUserID: actorID, ExpectedDraftRevision: expected, Resource: resource, LeaseID: lease.ID, Rendition: rendition, ChangedAt: at, AuditEventID: audit.ID, AuditAt: at}}
}
func saveExamResourceAudit(t *testing.T, ctx context.Context, ss store.Store, examID model.ExamID, actorID model.UserID, unitID model.AcademicUnitID) *model.AuditEvent {
	t.Helper()
	audit, err := ss.Audit().Save(ctx, &model.AuditEvent{ActorID: actorID, Action: string(model.ActionExamManage), Resource: model.Resource{Type: model.ResourceExam, ID: examID.String()}, ScopeType: model.RoleScopeAcademicUnit, ScopeID: unitID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
	require.NoError(t, err)
	return audit
}
