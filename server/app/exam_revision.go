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
	"github.com/sudosylabs/proctor/server/store"
)

// ExamRevisionSummary is the bounded authoring projection of one Exam
// Revision. It excludes Exam Instructions, canonical policy bytes, Exam
// Resource metadata, Starter Workspace paths and opaque content identities.
type ExamRevisionSummary = store.ExamRevisionSummary

// ExamRevisionPage is one page of Exam Revision summaries.
type ExamRevisionPage = examengine.RevisionPage

// PublishExamRevisionCommand publishes the current Exam Draft as a new
// immutable Exam Revision, fenced by ExpectedDraftRevision and replayed by
// IdempotencyKey.
type PublishExamRevisionCommand = examengine.PublishRevisionCommand

// GetExamRevisionQuery names one Exam Revision.
type GetExamRevisionQuery = examengine.GetRevisionQuery

// ListExamRevisionsQuery pages an Exam's Revisions, newest first.
type ListExamRevisionsQuery = examengine.ListRevisionsQuery

func (e examRealtimeEffects) RevisionPublished(ctx context.Context, revision store.ExamRevisionSummary) error {
	event, err := apprealtime.NewExamRevisionPublishedEvent(revision.ID, revision.ExamID, revision.Number, revision.PolicyDigest, revision.Kind, revision.PublishedAt)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}

var _ examengine.PublicationEffects = examRealtimeEffects{}
