// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/app/exam/resource"
	"github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
)

type examResourceRealtimeEffects struct{ realtime *realtimeService }

func (e examResourceRealtimeEffects) Changed(ctx context.Context, examID model.ExamID, _ model.ExamResourceID, draftRevision int64, _ string) error {
	event, err := realtime.NewExamDraftUpdatedEvent(examID, draftRevision)
	if err != nil {
		return err
	}
	return e.realtime.PublishRealtimeEvent(ctx, event)
}
func (e examResourceRealtimeEffects) Report(ctx context.Context, operation string, err error) {
	e.realtime.reportTransientFailure(ctx, operation, err)
}

var _ resource.Authorizer = (*accessControlService)(nil)
var _ resource.Auditor = mutationAuditAdapter{}
var _ resource.Effects = examResourceRealtimeEffects{}
var _ resource.EffectFailures = examResourceRealtimeEffects{}
