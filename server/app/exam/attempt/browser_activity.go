// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type StartBrowserActivityCommand struct {
	CandidateAccess
	ParticipationID      model.AttemptParticipationID
	Generation           int64
	SourceSessionID      model.BrowserSourceSessionID
	PredecessorSessionID model.BrowserSourceSessionID
	ResetReason          model.BrowserSourceResetReason
}

type AppendBrowserActivityCommand struct {
	CandidateAccess
	ParticipationID model.AttemptParticipationID
	Generation      int64
	SourceSessionID model.BrowserSourceSessionID
	Events          []model.BrowserActivityEvent
}

func (service *Service) StartBrowserActivity(ctx context.Context, call Call, command StartBrowserActivityCommand) (model.BrowserActivityAcknowledgement, error) {
	access, err := candidateSelector(call, command.CandidateAccess)
	if err != nil || !command.ParticipationID.IsValid() || command.Generation < 1 || !command.SourceSessionID.IsValid() ||
		(command.PredecessorSessionID.IsValid() != command.ResetReason.IsValid()) {
		return model.BrowserActivityAcknowledgement{}, invalid("browser_activity_source")
	}
	acknowledgement, err := service.deps.Persistence.StartBrowserActivity(ctx, &store.BrowserActivitySourceStart{Access: access,
		ParticipationID: command.ParticipationID, Generation: command.Generation, SourceSessionID: command.SourceSessionID,
		PredecessorSessionID: command.PredecessorSessionID, ResetReason: command.ResetReason})
	if err != nil {
		return model.BrowserActivityAcknowledgement{}, mapStore(err)
	}
	if !validBrowserActivityAcknowledgement(acknowledgement, command.SourceSessionID) {
		return model.BrowserActivityAcknowledgement{}, unavailable(errors.New("invalid Browser Activity acknowledgement"))
	}
	if acknowledgement.GapAttentionChanged {
		if effectErr := service.deps.Effects.BrowserActivityGapChanged(ctx, acknowledgement.ExamID,
			acknowledgement.SittingID); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "browser_activity_gap_changed", effectErr)
		}
	}
	return *acknowledgement, nil
}

func (service *Service) AppendBrowserActivity(ctx context.Context, call Call, command AppendBrowserActivityCommand) (model.BrowserActivityAcknowledgement, error) {
	access, err := candidateSelector(call, command.CandidateAccess)
	if err != nil || !command.ParticipationID.IsValid() || command.Generation < 1 || !command.SourceSessionID.IsValid() ||
		len(command.Events) < 1 || len(command.Events) > model.BrowserActivityAppendMaximumEvents {
		return model.BrowserActivityAcknowledgement{}, invalid("browser_activity_append")
	}
	events := append([]model.BrowserActivityEvent(nil), command.Events...)
	acknowledgement, err := service.deps.Persistence.AppendBrowserActivity(ctx, &store.BrowserActivityAppend{Access: access,
		ParticipationID: command.ParticipationID, Generation: command.Generation, SourceSessionID: command.SourceSessionID, Events: events})
	if err != nil {
		return model.BrowserActivityAcknowledgement{}, mapStore(err)
	}
	if !validBrowserActivityAcknowledgement(acknowledgement, command.SourceSessionID) {
		return model.BrowserActivityAcknowledgement{}, unavailable(errors.New("invalid Browser Activity acknowledgement"))
	}
	if acknowledgement.GapAttentionChanged {
		if effectErr := service.deps.Effects.BrowserActivityGapChanged(ctx, acknowledgement.ExamID,
			acknowledgement.SittingID); effectErr != nil {
			service.deps.EffectFailures.Report(ctx, "browser_activity_gap_changed", effectErr)
		}
	}
	return *acknowledgement, nil
}

func validBrowserActivityAcknowledgement(value *model.BrowserActivityAcknowledgement, sourceID model.BrowserSourceSessionID) bool {
	if value == nil || value.SourceSessionID != sourceID || !value.ExamID.IsValid() || !value.SittingID.IsValid() ||
		value.HighestContiguous < 0 || value.HighestSeen < value.HighestContiguous ||
		value.ServerTime.IsZero() || len(value.MissingRanges) > model.BrowserActivityMaximumMissingRanges {
		return false
	}
	previous := value.HighestContiguous
	for _, missing := range value.MissingRanges {
		if missing.First <= previous || missing.Last < missing.First || missing.Last > value.HighestSeen {
			return false
		}
		previous = missing.Last
	}
	return true
}

type BrowserActivityPageQuery struct {
	ExamID          model.ExamID
	SittingID       model.ExamSittingID
	AttemptID       model.ExamAttemptID
	AfterReceivedAt time.Time
	AfterSourceID   model.BrowserSourceSessionID
	AfterSequence   int64
	Limit           int
}

type BrowserActivityPage struct {
	Items   []store.BrowserActivityRecord
	HasMore bool
}

func (service *Service) ListBrowserActivity(ctx context.Context, call Call, query BrowserActivityPageQuery) (BrowserActivityPage, error) {
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || !query.AttemptID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		(query.AfterReceivedAt.IsZero() != !query.AfterSourceID.IsValid()) || !query.AfterReceivedAt.IsZero() && query.AfterSequence < 1 {
		return BrowserActivityPage{}, invalid("browser_activity_list")
	}
	snapshot, err := service.deps.Persistence.Get(ctx, query.ExamID, query.AttemptID)
	if err != nil {
		return BrowserActivityPage{}, mapStore(err)
	}
	if snapshot == nil || snapshot.Attempt == nil || snapshot.Attempt.Validate() != nil || snapshot.Attempt.ExamID != query.ExamID {
		return BrowserActivityPage{}, unavailable(errors.New("inconsistent Browser Activity Attempt projection"))
	}
	if snapshot.Attempt.SittingID != query.SittingID {
		return BrowserActivityPage{}, &Fault{Code: "exam.attempt.not_found"}
	}
	unitID, _, err := service.deps.Managers.AuthorizeBrowserActivityView(ctx, call, snapshot.Attempt.SittingID)
	if err != nil {
		return BrowserActivityPage{}, err
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: snapshot.Attempt.SittingID.String()}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamAttemptBrowserActivityView, resource,
		model.RoleScopeAcademicUnit, unitID.String(), "view_browser_activity", map[string]any{"exam_id": snapshot.Attempt.ExamID.String(),
			"exam_sitting_id": snapshot.Attempt.SittingID.String(), "exam_attempt_id": snapshot.Attempt.ID.String(), "limit": query.Limit})
	if err != nil {
		return BrowserActivityPage{}, err
	}
	if snapshot.Attempt.CandidateUserID == call.Principal().UserID {
		if err = service.deps.Auditor.Fail(ctx, auditID, "exam.attempt.not_found"); err != nil {
			return BrowserActivityPage{}, err
		}
		return BrowserActivityPage{}, &Fault{Code: "exam.attempt.not_found"}
	}
	items, err := service.deps.Persistence.ListBrowserActivity(ctx, store.BrowserActivityListOptions{ExamID: query.ExamID,
		SittingID: query.SittingID, AttemptID: query.AttemptID, AfterReceivedAt: model.TimeUTC(query.AfterReceivedAt),
		AfterSourceID: query.AfterSourceID, AfterSequence: query.AfterSequence, Limit: query.Limit + 1})
	if err != nil {
		return BrowserActivityPage{}, service.failAudit(ctx, auditID, err)
	}
	page := BrowserActivityPage{Items: append([]store.BrowserActivityRecord(nil), items[:min(len(items), query.Limit)]...), HasMore: len(items) > query.Limit}
	if err = service.deps.Auditor.Complete(ctx, auditID, map[string]any{"returned_count": len(page.Items), "has_more": page.HasMore}); err != nil {
		return BrowserActivityPage{}, err
	}
	return page, nil
}
