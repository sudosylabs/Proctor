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
)

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
