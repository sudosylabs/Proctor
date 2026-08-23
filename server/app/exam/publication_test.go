// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package exam

import (
	"context"
	"errors"
	"testing"
	"time"

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
	service, err := NewPublication(revisions, f.persistence, f.memberships, f.authorizer, f.auditor, effects, f.effects, fixedPublicationTime, model.NewExamRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	command := PublishRevisionCommand{ExamID: f.examID, ExpectedDraftRevision: 3, Idempotency: &store.CommandIdempotency{}}
	got, err := service.Publish(context.Background(), f.call, command)
	if err != nil {
		t.Fatal(err)
	}
	if f.authorizer.action != model.ActionExamPublish || revisions.input == nil || revisions.input.ManagerOverride || got.ID != revisions.summary.ID || effects.calls != 1 {
		t.Fatalf("publication action=%q input=%#v result=%#v effects=%d", f.authorizer.action, revisions.input, got, effects.calls)
	}
	revisions.replayed = true
	if _, err = service.Publish(context.Background(), f.call, command); err != nil {
		t.Fatal(err)
	}
	if effects.calls != 1 {
		t.Fatalf("replay republished effect: %d", effects.calls)
	}
}

func TestPublicationUsesOverrideForNonManager(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	revisions := &revisionStoreFake{}
	service, err := NewPublication(revisions, f.persistence, f.memberships, f.authorizer, f.auditor, &publicationEffectsFake{}, f.effects, fixedPublicationTime, model.NewExamRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Publish(context.Background(), f.call, PublishRevisionCommand{ExamID: f.examID, ExpectedDraftRevision: 1, Idempotency: &store.CommandIdempotency{}})
	if err != nil {
		t.Fatal(err)
	}
	if f.authorizer.action != model.ActionExamPublishOverride || !revisions.input.ManagerOverride {
		t.Fatalf("override action=%q input=%#v", f.authorizer.action, revisions.input)
	}
}

func TestPublicationMapsCapacityConflict(t *testing.T) {
	t.Parallel()
	f := newAuthoringFixture(t)
	f.persistence.actorIsManager = true
	f.memberships.items = []*model.AcademicUnitMember{{AcademicUnitID: f.unitID}}
	revisions := &revisionStoreFake{err: store.NewErrConflict("exam_revision", "exam_revision_capacity", nil)}
	service, err := NewPublication(revisions, f.persistence, f.memberships, f.authorizer, f.auditor,
		&publicationEffectsFake{}, f.effects, fixedPublicationTime, model.NewExamRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Publish(context.Background(), f.call,
		PublishRevisionCommand{ExamID: f.examID, ExpectedDraftRevision: 3, Idempotency: &store.CommandIdempotency{}})
	var fault *Fault
	if !errors.As(err, &fault) || fault.Code != "exam.revision.capacity_exceeded" {
		t.Fatalf("Publish() error = %v", err)
	}
}

func fixedPublicationTime() time.Time { return time.Date(2026, 8, 15, 8, 0, 0, 0, time.UTC) }

type publicationEffectsFake struct{ calls int }

func (f *publicationEffectsFake) RevisionPublished(context.Context, store.ExamRevisionSummary) error {
	f.calls++
	return nil
}

type revisionStoreFake struct {
	store.ExamRevisionStore
	input    *store.ExamRevisionPublication
	replayed bool
	summary  store.ExamRevisionSummary
	err      error
}

func (f *revisionStoreFake) Publish(_ context.Context, input *store.ExamRevisionPublication, _ *store.CommandIdempotency) (*store.ExamRevisionPublicationResult, error) {
	f.input = input
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
