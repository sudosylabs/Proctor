// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	examengine "github.com/sudosylabs/proctor/server/app/exam"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ExamRevisionSummary = store.ExamRevisionSummary
type ExamRevisionPage = examengine.RevisionPage

type PublishExamRevisionCommand = examengine.PublishRevisionCommand
type GetExamRevisionQuery struct {
	ExamID     model.ExamID
	RevisionID model.ExamRevisionID
}
type ListExamRevisionsQuery = examengine.ListRevisionsQuery

func (a *App) PublishExamRevision(ctx context.Context, invocation Invocation, command PublishExamRevisionCommand) (result ExamRevisionSummary, resultErr error) {
	defer func() { a.recordOperational("exam", "publish_revision", resultErr) }()
	summary, err := a.examRevisions.Publish(ctx, invocation, command)
	if err != nil {
		return ExamRevisionSummary{}, examError(err, true)
	}
	return summary, nil
}

func (a *App) GetExamRevision(ctx context.Context, invocation Invocation, query GetExamRevisionQuery) (ExamRevisionSummary, error) {
	summary, err := a.examRevisions.Get(ctx, invocation, query.ExamID, query.RevisionID)
	if err != nil {
		return ExamRevisionSummary{}, examError(err, true)
	}
	return summary, nil
}

func (a *App) ListExamRevisions(ctx context.Context, invocation Invocation, query ListExamRevisionsQuery) (ExamRevisionPage, error) {
	page, err := a.examRevisions.List(ctx, invocation, query)
	if err != nil {
		return ExamRevisionPage{}, examError(err, true)
	}
	return page, nil
}

func (e examRealtimeEffects) RevisionPublished(ctx context.Context, revision store.ExamRevisionSummary) error {
	event, err := apprealtime.NewExamRevisionPublishedEvent(revision.ID, revision.ExamID, revision.Number, revision.PolicyDigest, revision.Kind, revision.PublishedAt)
	if err != nil {
		return err
	}
	return e.realtime.Publish(ctx, event)
}

var _ examengine.PublicationEffects = examRealtimeEffects{}
