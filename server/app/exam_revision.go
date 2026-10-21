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

type ExamRevisionSummary = store.ExamRevisionSummary
type ExamRevisionPage = examengine.RevisionPage

type PublishExamRevisionCommand = examengine.PublishRevisionCommand
type GetExamRevisionQuery = examengine.GetRevisionQuery
type ListExamRevisionsQuery = examengine.ListRevisionsQuery

func (e examRealtimeEffects) RevisionPublished(ctx context.Context, revision store.ExamRevisionSummary) error {
	event, err := apprealtime.NewExamRevisionPublishedEvent(revision.ID, revision.ExamID, revision.Number, revision.PolicyDigest, revision.Kind, revision.PublishedAt)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}

var _ examengine.PublicationEffects = examRealtimeEffects{}
