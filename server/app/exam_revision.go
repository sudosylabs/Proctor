// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/app/exam"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/store"
)

func (e examRealtimeEffects) RevisionPublished(ctx context.Context, revision store.ExamRevisionSummary) error {
	event, err := realtime.NewExamRevisionPublishedEvent(revision.ID, revision.ExamID, revision.Number, revision.PolicyDigest, revision.Kind, revision.PublishedAt)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}

var _ exam.PublicationEffects = examRealtimeEffects{}
