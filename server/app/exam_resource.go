// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	examresource "github.com/sudosylabs/proctor/server/app/exam/resource"
	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// ExamResourceRecord is the bounded Exam Draft projection of one Exam
// Resource, including its selected primary rendition; storage keys and upload
// leases are never exposed.
type ExamResourceRecord = store.ExamResourceRecord

// OpenedExamResource is an Exam Draft Resource with its content stream; the
// caller must close Body.
type OpenedExamResource = examresource.Opened

// CreateExamResourceCommand adds an Exam Resource to an Exam Draft from Body,
// verified against Size and ExpectedSHA256. It is fenced by
// ExpectedDraftRevision and replayed by IdempotencyKey.
type CreateExamResourceCommand = examresource.CreateCommand

// ReplaceExamResourceContentCommand replaces the content of an Exam Draft
// Resource, fenced by ExpectedDraftRevision and replayed by IdempotencyKey.
type ReplaceExamResourceContentCommand = examresource.ReplaceContentCommand

// EditExamResourceMetadataCommand edits an Exam Draft Resource's display name
// and/or description; nil fields are left unchanged. It is fenced by
// ExpectedDraftRevision and replayed by IdempotencyKey.
type EditExamResourceMetadataCommand = examresource.EditMetadataCommand

// ReorderExamResourcesCommand sets the order of an Exam Draft's Resources,
// fenced by ExpectedDraftRevision and replayed by IdempotencyKey.
type ReorderExamResourcesCommand = examresource.ReorderCommand

// RemoveExamResourceCommand removes an Exam Resource from an Exam Draft,
// fenced by ExpectedDraftRevision and replayed by IdempotencyKey.
type RemoveExamResourceCommand = examresource.RemoveCommand

// ListExamResourcesQuery names the Exam whose Exam Draft Resources are listed.
type ListExamResourcesQuery = examresource.ListQuery

// OpenExamResourceQuery names one Exam Draft Resource of an Exam.
type OpenExamResourceQuery = examresource.OpenQuery

type examResourceRealtimeEffects struct{ realtime *realtimeService }

func (e examResourceRealtimeEffects) Changed(ctx context.Context, examID model.ExamID, _ model.ExamResourceID, draftRevision int64, _ string) error {
	event, err := apprealtime.NewExamDraftUpdatedEvent(examID, draftRevision)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examResourceRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ examresource.Authorizer = (*accessControlService)(nil)
var _ examresource.Auditor = mutationAuditAdapter{}
var _ examresource.Effects = examResourceRealtimeEffects{}
var _ examresource.EffectFailures = examResourceRealtimeEffects{}
