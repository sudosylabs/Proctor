// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestPublicationPublishesAfterCurrentManagerAuthorizationAndSuppressesReplayEffect(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	f.persistence.actorIsManager = true
	f.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: f.unitID}}
	revisions := &revisionStoreFake{}
	effects := &publicationEffectsFake{}
	service, err := NewPublication(PublicationDependencies{
		Persistence:       revisions,
		Access:            f.persistence,
		Memberships:       f.memberships,
		Authorizer:        f.authorizer,
		Auditor:           f.auditor,
		Effects:           effects,
		EffectFailures:    f.effects,
		Now:               fixedPublicationTime,
		NewID:             model.NewExamRevisionID,
		InstitutionOrigin: "https://institution.example",
	})
	require.NoError(t, err)
	command := PublishRevisionCommand{ExamID: f.examID, ExpectedDraftRevision: 3, IdempotencyKey: "test-key"}
	got, err := service.PublishExamRevision(context.Background(), f.invocation, command)
	require.NoError(t, err)
	require.Equal(t, model.ActionExamPublish, f.authorizer.action, "publication action=%q input=%#v result=%#v effects", f.authorizer.action, revisions.input, got)
	require.NotNil(t, revisions.input, "publication action=%q input=%#v result=%#v effects", f.authorizer.action, revisions.input, got)
	require.False(t, revisions.input.ManagerOverride, "publication action=%q input=%#v result=%#v effects", f.authorizer.action, revisions.input, got)
	require.Equal(t, revisions.summary.ID, got.ID, "publication action=%q input=%#v result=%#v effects", f.authorizer.action, revisions.input, got)
	require.Equal(t, 1, effects.calls, "publication action=%q input=%#v result=%#v effects", f.authorizer.action, revisions.input, got)
	assertStoreIdempotency(t, revisions.idempotency, f.userID, idempotencyOperationPublishRevision, "test-key",
		fmt.Sprintf(`{"exam_id":%q,"expected_draft_revision":3}`, f.examID))
	revisions.replayed = true
	_, err = service.PublishExamRevision(context.Background(), f.invocation, command)
	require.NoError(t, err)
	require.Equal(t, 1, effects.calls, "replay republished effect")
}

func TestPublicationUsesOverrideForNonManager(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	revisions := &revisionStoreFake{}
	service, err := NewPublication(PublicationDependencies{
		Persistence:       revisions,
		Access:            f.persistence,
		Memberships:       f.memberships,
		Authorizer:        f.authorizer,
		Auditor:           f.auditor,
		Effects:           &publicationEffectsFake{},
		EffectFailures:    f.effects,
		Now:               fixedPublicationTime,
		NewID:             model.NewExamRevisionID,
		InstitutionOrigin: "https://institution.example",
	})
	require.NoError(t, err)
	_, err = service.PublishExamRevision(context.Background(), f.invocation, PublishRevisionCommand{ExamID: f.examID, ExpectedDraftRevision: 1, IdempotencyKey: "test-key"})
	require.NoError(t, err)
	require.Equal(t, model.ActionExamPublishOverride, f.authorizer.action, "override action=%q input=%#v", f.authorizer.action, revisions.input)
	require.True(t, revisions.input.ManagerOverride, "override action=%q input=%#v", f.authorizer.action, revisions.input)
}

func TestPublicationMapsCapacityConflict(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	f.persistence.actorIsManager = true
	f.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: f.unitID}}
	revisions := &revisionStoreFake{err: store.NewErrConflict("exam_revision", "exam_revision_capacity", nil)}
	service, err := NewPublication(PublicationDependencies{
		Persistence:       revisions,
		Access:            f.persistence,
		Memberships:       f.memberships,
		Authorizer:        f.authorizer,
		Auditor:           f.auditor,
		Effects:           &publicationEffectsFake{},
		EffectFailures:    f.effects,
		Now:               fixedPublicationTime,
		NewID:             model.NewExamRevisionID,
		InstitutionOrigin: "https://institution.example",
	})
	require.NoError(t, err)
	_, err = service.PublishExamRevision(context.Background(), f.invocation,
		PublishRevisionCommand{ExamID: f.examID, ExpectedDraftRevision: 3, IdempotencyKey: "test-key"})
	var fault *apperror.Error
	require.ErrorAs(t, err, &fault)
	require.Equal(t, "exam.revision.capacity_exceeded", fault.Code())
}

func fixedPublicationTime() time.Time { return time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC) }

type publicationEffectsFake struct{ calls int }

func (f *publicationEffectsFake) RevisionPublished(context.Context, store.ExamRevisionSummary) error {
	f.calls++
	return nil
}

type revisionStoreFake struct {
	store.ExamRevisionStore
	input       *store.ExamRevisionPublication
	idempotency *store.CommandIdempotency
	replayed    bool
	summary     store.ExamRevisionSummary
	err         error
}

func (f *revisionStoreFake) Publish(_ context.Context, input *store.ExamRevisionPublication, command *store.CommandIdempotency) (*store.ExamRevisionPublicationResult, error) {
	f.input, f.idempotency = input, command
	if f.err != nil {
		return nil, f.err
	}
	if !f.summary.ID.IsValid() {
		f.summary = store.ExamRevisionSummary{ID: input.RevisionID, ExamID: input.ExamID, Number: 1, SourceDraftRevision: input.ExpectedDraftRevision,
			Title: "Exam", PolicySchemaVersion: 1, PolicyDigest: string(make([]byte, 64)), StarterWorkspaceDigest: string(make([]byte, 64)),
			ContentDigest: string(make([]byte, 64)), PublishedByUserID: input.ActorUserID, PublishedAt: input.PublishedAt, Kind: input.Kind}
	}
	return &store.ExamRevisionPublicationResult{Revision: &f.summary, ExamRevision: 2, DraftRevision: input.ExpectedDraftRevision + 1, Replayed: f.replayed}, nil
}
