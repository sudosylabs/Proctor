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

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
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

func (service *Service) StartBrowserActivity(ctx context.Context, invocation invocation.Invocation, command StartBrowserActivityCommand) (model.BrowserSourceStatus, error) {
	release, admissionErr := service.enterControl(ctx, invocation, false)
	if admissionErr != nil {
		return model.BrowserSourceStatus{}, admissionErr
	}
	defer release()
	access, err := candidateSelector(invocation, command.CandidateAccess)
	if err != nil {
		return model.BrowserSourceStatus{}, err
	}
	input := &store.BrowserActivitySourceStart{Access: access, ParticipationID: command.ParticipationID, Generation: command.Generation, SourceSessionID: command.SourceSessionID, PolicyRevisionID: command.PolicyRevisionID, PolicyDigest: command.PolicyDigest, Transition: command.Transition}
	if input.Declaration().Validate() != nil {
		return model.BrowserSourceStatus{}, apperror.Invalid("exam.attempt.invalid", "browser_activity_source")
	}
	target, err := service.deps.Persistence.ResolveLiveDeliveryTarget(ctx, access)
	if err != nil {
		return model.BrowserSourceStatus{}, storeError(storeErrors, err)
	}
	if target == nil || !target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return model.BrowserSourceStatus{}, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	auditID, err := service.deps.Auditor.Begin(ctx, invocation, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass, target.ClassID.String(), "start_browser_source", map[string]any{"source_session_id": string(command.SourceSessionID)})
	if err != nil {
		return model.BrowserSourceStatus{}, err
	}
	input.AuditEventID = auditID
	input.AuditAt = model.MillisFromTime(service.deps.Now())
	status, err := service.deps.Persistence.StartBrowserActivity(ctx, input)
	if err != nil {
		var refusal *store.BrowserSourceRefusal
		if errors.As(err, &refusal) {
			presentation, projectionErr := service.GetPresentation(ctx, invocation, command.CandidateAccess)
			if projectionErr != nil {
				return model.BrowserSourceStatus{}, projectionErr
			}
			refusal.Capabilities = &presentation.RuntimeCapabilities
			return model.BrowserSourceStatus{}, apperror.New(browserSourceRefusalCode(refusal.Code)).Wrap(err)
		}
		return model.BrowserSourceStatus{}, service.failAudit(ctx, auditID, storeError(storeErrors, err))
	}
	if status == nil || status.Validate() != nil || status.SourceSessionID != command.SourceSessionID {
		return model.BrowserSourceStatus{}, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	return *status, nil
}

func (service *Service) AppendBrowserActivity(ctx context.Context, invocation invocation.Invocation, command AppendBrowserActivityCommand) (model.BrowserActivityAcknowledgement, error) {
	access, err := candidateSelector(invocation, command.CandidateAccess)
	if err != nil || !command.ParticipationID.IsValid() || command.Generation < 1 || !command.SourceSessionID.IsValid() ||
		len(command.Events) < 1 || len(command.Events) > model.BrowserActivityAppendMaximumEvents {
		return model.BrowserActivityAcknowledgement{}, apperror.Invalid("exam.attempt.invalid", "browser_activity_append")
	}
	events := append([]model.BrowserActivityEvent(nil), command.Events...)
	target, err := service.deps.Persistence.ResolveLiveDeliveryTarget(ctx, access)
	if err != nil {
		return model.BrowserActivityAcknowledgement{}, storeError(storeErrors, err)
	}
	if target == nil || !target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return model.BrowserActivityAcknowledgement{}, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	auditID, err := service.deps.Auditor.Begin(ctx, invocation, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass, target.ClassID.String(), "append_browser_activity", map[string]any{"source_session_id": string(command.SourceSessionID), "event_count": len(events)})
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
		return model.BrowserActivityAcknowledgement{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("invalid Browser Activity acknowledgement"))
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

func (service *Service) ListBrowserActivity(ctx context.Context, invocation invocation.Invocation, query BrowserActivityPageQuery) (BrowserActivityPage, error) {
	if !query.ExamID.IsValid() || !query.SittingID.IsValid() || !query.AttemptID.IsValid() || query.Limit < 1 || query.Limit > 200 ||
		(query.AfterReceivedAt.IsZero() != !query.AfterSourceID.IsValid()) || !query.AfterReceivedAt.IsZero() && query.AfterSequence < 1 {
		return BrowserActivityPage{}, apperror.Invalid("exam.attempt.invalid", "browser_activity_list")
	}
	// Authorize the exact Sitting before reading the Attempt so existence is
	// not probeable; later exits complete the audit attempt as failed.
	unitID, err := service.deps.Managers.AuthorizeBrowserActivityView(ctx, invocation, query.SittingID)
	if err != nil {
		return BrowserActivityPage{}, err
	}
	resource := model.Resource{Type: model.ResourceExamSitting, ID: query.SittingID.String()}
	auditID, err := service.deps.Auditor.Begin(ctx, invocation, model.ActionExamAttemptBrowserActivityView, resource,
		model.RoleScopeAcademicUnit, unitID.String(), "view_browser_activity", map[string]any{"exam_id": query.ExamID.String(),
			"exam_sitting_id": query.SittingID.String(), "exam_attempt_id": query.AttemptID.String(), "limit": query.Limit})
	if err != nil {
		return BrowserActivityPage{}, err
	}
	snapshot, err := service.deps.Persistence.Get(ctx, query.ExamID, query.AttemptID)
	if err != nil {
		return BrowserActivityPage{}, service.failAudit(ctx, auditID, err)
	}
	if snapshot == nil || snapshot.Attempt == nil || snapshot.Attempt.Validate() != nil || snapshot.Attempt.ExamID != query.ExamID {
		return BrowserActivityPage{}, service.failAudit(ctx, auditID, apperror.New("exam.attempt.unavailable").Wrap(errors.New("inconsistent Browser Activity Attempt projection")))
	}
	if snapshot.Attempt.SittingID != query.SittingID || snapshot.Attempt.CandidateUserID == invocation.Principal().UserID {
		return BrowserActivityPage{}, service.failAudit(ctx, auditID, apperror.New("exam.attempt.not_found"))
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

func browserSourceSelector(invocation invocation.Invocation, query BrowserSourceQuery) (store.BrowserDeliveryAccess, error) {
	selector := string(query.SourceSessionID)
	if query.SourceSessionID == "" {
		selector = query.ParticipationID.String()
	} else if !query.SourceSessionID.IsValid() {
		return store.BrowserDeliveryAccess{}, apperror.Invalid("exam.attempt.invalid", "browser_source")
	}
	if query.ParticipationID != "" && !query.ParticipationID.IsValid() {
		return store.BrowserDeliveryAccess{}, apperror.Invalid("exam.attempt.invalid", "participation_id")
	}
	access, err := nativeDeliverySelector(invocation, NativeDeliveryQuery{Access: query.Access, StreamID: selector})
	if err != nil {
		return store.BrowserDeliveryAccess{}, err
	}
	return store.BrowserDeliveryAccess{Access: access.Access, SourceSessionID: query.SourceSessionID, ParticipationID: query.ParticipationID}, nil
}
func (service *Service) BrowserSourceStatus(ctx context.Context, invocation invocation.Invocation, query BrowserSourceQuery) (*model.BrowserSourceStatus, error) {
	release, admissionErr := service.enterControl(ctx, invocation, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := browserSourceSelector(invocation, query)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.BrowserSourceStatus(ctx, access)
	if err != nil {
		return nil, storeError(storeErrors, err)
	}
	if value == nil || value.Validate() != nil || value.SourceSessionID != query.SourceSessionID {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) BrowserSourceList(ctx context.Context, invocation invocation.Invocation, query BrowserSourceQuery) ([]model.BrowserSourceStatus, error) {
	release, admissionErr := service.enterControl(ctx, invocation, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	if !query.ParticipationID.IsValid() || query.SourceSessionID != "" {
		return nil, apperror.Invalid("exam.attempt.invalid", "participation_id")
	}
	access, err := browserSourceSelector(invocation, query)
	if err != nil {
		return nil, err
	}
	values, err := service.deps.Persistence.BrowserSourceList(ctx, access)
	if err != nil {
		return nil, storeError(storeErrors, err)
	}
	if values == nil || len(values) > model.BrowserSourceMaximumPerParticipation {
		return nil, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
	}
	for _, value := range values {
		if value.Validate() != nil || value.ParticipationID != query.ParticipationID || value.AttemptID != query.Access.AttemptID {
			return nil, apperror.New("exam.attempt.unavailable").Wrap(model.ErrDeliveryInvalid)
		}
	}
	return values, nil
}

// browserSourceRefusalCode presents the bounded refusal reason that the Store
// persisted with a committed predecessor closure.
func browserSourceRefusalCode(reason string) apperror.Code {
	switch reason {
	case "exam.delivery.metadata_capacity":
		return "exam.delivery.metadata_capacity"
	case "exam.browser.source_budget_exhausted":
		return "exam.browser.source_budget_exhausted"
	default:
		return "exam.attempt.unavailable"
	}
}
