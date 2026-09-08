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
	ParticipationID  model.AttemptParticipationID
	Generation       int64
	SourceSessionID  model.BrowserSourceSessionID
	PolicyRevisionID model.ExamRevisionID
	PolicyDigest     string
	Transition       model.BrowserStartTransition
}

type AppendBrowserActivityCommand struct {
	PolicyRevisionID model.ExamRevisionID
	PolicyDigest     string
	CandidateAccess
	ParticipationID model.AttemptParticipationID
	Generation      int64
	SourceSessionID model.BrowserSourceSessionID
	Events          []model.BrowserActivityEvent
}

func (service *Service) StartBrowserActivity(ctx context.Context, call Call, command StartBrowserActivityCommand) (model.BrowserSourceStatus, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return model.BrowserSourceStatus{}, admissionErr
	}
	defer release()
	access, err := candidateSelector(call, command.CandidateAccess)
	if err != nil {
		return model.BrowserSourceStatus{}, err
	}
	input := &store.BrowserActivitySourceStart{Access: access, ParticipationID: command.ParticipationID, Generation: command.Generation, SourceSessionID: command.SourceSessionID, PolicyRevisionID: command.PolicyRevisionID, PolicyDigest: command.PolicyDigest, Transition: command.Transition}
	if input.Declaration().Validate() != nil {
		return model.BrowserSourceStatus{}, invalid("browser_activity_source")
	}
	target, err := service.deps.Persistence.ResolveLiveDeliveryTarget(ctx, access)
	if err != nil {
		return model.BrowserSourceStatus{}, mapStore(err)
	}
	if target == nil || !target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return model.BrowserSourceStatus{}, unavailable(model.ErrDeliveryInvalid)
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass, target.ClassID.String(), "start_browser_source", map[string]any{"source_session_id": string(command.SourceSessionID)})
	if err != nil {
		return model.BrowserSourceStatus{}, err
	}
	input.AuditEventID = auditID
	input.AuditAt = model.MillisFromTime(service.deps.Now())
	status, err := service.deps.Persistence.StartBrowserActivity(ctx, input)
	if err != nil {
		var refusal *store.BrowserSourceRefusal
		if errors.As(err, &refusal) {
			presentation, projectionErr := service.GetPresentation(ctx, call, command.CandidateAccess)
			if projectionErr != nil {
				return model.BrowserSourceStatus{}, projectionErr
			}
			refusal.Capabilities = &presentation.RuntimeCapabilities
			return model.BrowserSourceStatus{}, &Fault{Code: refusal.Code, Cause: err}
		}
		return model.BrowserSourceStatus{}, service.failAudit(ctx, auditID, mapStore(err))
	}
	if status == nil || status.Validate() != nil || status.SourceSessionID != command.SourceSessionID {
		return model.BrowserSourceStatus{}, unavailable(model.ErrDeliveryInvalid)
	}
	return *status, nil
}

func (service *Service) AppendBrowserActivity(ctx context.Context, call Call, command AppendBrowserActivityCommand) (model.BrowserActivityAcknowledgement, error) {
	access, err := candidateSelector(call, command.CandidateAccess)
	if err != nil || !command.ParticipationID.IsValid() || command.Generation < 1 || !command.SourceSessionID.IsValid() ||
		len(command.Events) < 1 || len(command.Events) > model.BrowserActivityAppendMaximumEvents {
		return model.BrowserActivityAcknowledgement{}, invalid("browser_activity_append")
	}
	events := append([]model.BrowserActivityEvent(nil), command.Events...)
	target, err := service.deps.Persistence.ResolveLiveDeliveryTarget(ctx, access)
	if err != nil {
		return model.BrowserActivityAcknowledgement{}, mapStore(err)
	}
	if target == nil || !target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return model.BrowserActivityAcknowledgement{}, unavailable(model.ErrDeliveryInvalid)
	}
	auditID, err := service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass, target.ClassID.String(), "append_browser_activity", map[string]any{"source_session_id": string(command.SourceSessionID), "event_count": len(events)})
	if err != nil {
		return model.BrowserActivityAcknowledgement{}, err
	}
	acknowledgement, err := service.deps.Persistence.AppendBrowserActivity(ctx, &store.BrowserActivityAppend{Access: access, AuditEventID: auditID, AuditAt: model.MillisFromTime(service.deps.Now()),
		ParticipationID: command.ParticipationID, Generation: command.Generation, SourceSessionID: command.SourceSessionID, PolicyRevisionID: command.PolicyRevisionID, PolicyDigest: command.PolicyDigest, Events: events})
	deliveryAccess := store.BrowserDeliveryAccess{Access: access, ParticipationID: command.ParticipationID, SourceSessionID: command.SourceSessionID}
	if err != nil {
		var refusal *store.BrowserDeliveryRefusal
		if errors.As(err, &refusal) {
			return model.BrowserActivityAcknowledgement{}, service.browserDeliveryFailure(ctx, deliveryAccess, err)
		}
		return model.BrowserActivityAcknowledgement{}, service.failAudit(ctx, auditID, service.browserDeliveryFailure(ctx, deliveryAccess, err))
	}
	if !validBrowserActivityAcknowledgement(acknowledgement, command.SourceSessionID, events) {
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

func validBrowserActivityAcknowledgement(value *model.BrowserActivityAcknowledgement, sourceID model.BrowserSourceSessionID, events []model.BrowserActivityEvent) bool {
	if value == nil || value.SourceSessionID != sourceID || !value.ExamID.IsValid() || !value.SittingID.IsValid() ||
		value.HighestContiguous < 0 || value.HighestSeen < value.HighestContiguous ||
		value.ServerTime.IsZero() || len(value.MissingRanges) > model.BrowserActivityMaximumMissingRanges {
		return false
	}
	progress := model.BrowserDeliveryProgress{HighestContiguous: value.HighestContiguous, SettledThrough: value.SettledThrough, HighestSeen: value.HighestSeen, AllocatedThrough: value.AllocatedThrough, TerminalMissingThrough: value.TerminalMissingThrough, MissingRanges: []model.SequenceRange{}, MissingRangesTruncated: value.MissingRangesTruncated}
	for _, missing := range value.MissingRanges {
		progress.MissingRanges = append(progress.MissingRanges, model.SequenceRange{First: missing.First, Last: missing.Last})
	}
	if progress.Validate() != nil || len(value.Receipts) != len(events) {
		return false
	}
	for i, receipt := range value.Receipts {
		digest, err := events[i].Fingerprint()
		if err != nil || receipt.Validate() != nil || receipt.Sequence != events[i].Sequence || receipt.EventDigest != digest || receipt.ReceivedAt.After(value.ServerTime) {
			return false
		}
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
	unitID, err := service.deps.Managers.AuthorizeBrowserActivityView(ctx, call, snapshot.Attempt.SittingID)
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

type BrowserSourceQuery struct {
	Access          CandidateAccess
	SourceSessionID model.BrowserSourceSessionID
	ParticipationID model.AttemptParticipationID
}

func browserSourceSelector(call Call, query BrowserSourceQuery) (store.BrowserDeliveryAccess, error) {
	selector := string(query.SourceSessionID)
	if query.SourceSessionID == "" {
		selector = query.ParticipationID.String()
	} else if !query.SourceSessionID.IsValid() {
		return store.BrowserDeliveryAccess{}, invalid("browser_source")
	}
	if query.ParticipationID != "" && !query.ParticipationID.IsValid() {
		return store.BrowserDeliveryAccess{}, invalid("participation_id")
	}
	access, err := nativeDeliverySelector(call, NativeDeliveryQuery{Access: query.Access, StreamID: selector})
	if err != nil {
		return store.BrowserDeliveryAccess{}, err
	}
	return store.BrowserDeliveryAccess{Access: access.Access, SourceSessionID: query.SourceSessionID, ParticipationID: query.ParticipationID}, nil
}
func (service *Service) BrowserSourceStatus(ctx context.Context, call Call, query BrowserSourceQuery) (*model.BrowserSourceStatus, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := browserSourceSelector(call, query)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.BrowserSourceStatus(ctx, access)
	if err != nil {
		return nil, mapStore(err)
	}
	if value == nil || value.Validate() != nil || value.SourceSessionID != query.SourceSessionID {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) BrowserSourceList(ctx context.Context, call Call, query BrowserSourceQuery) ([]model.BrowserSourceStatus, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !query.ParticipationID.IsValid() || query.SourceSessionID != "" {
		return nil, invalid("participation_id")
	}
	access, err := browserSourceSelector(call, query)
	if err != nil {
		return nil, err
	}
	values, err := service.deps.Persistence.BrowserSourceList(ctx, access)
	if err != nil {
		return nil, mapStore(err)
	}
	if values == nil || len(values) > model.BrowserSourceMaximumPerParticipation {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	for _, value := range values {
		if value.Validate() != nil || value.ParticipationID != query.ParticipationID || value.AttemptID != query.Access.AttemptID {
			return nil, unavailable(model.ErrDeliveryInvalid)
		}
	}
	return values, nil
}
