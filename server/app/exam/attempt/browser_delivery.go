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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type BrowserDeliveryGapsCommand struct {
	Query          BrowserSourceQuery
	Declaration    model.DeclareDeliveryGaps
	IdempotencyKey string
}
type BrowserDeliveryFinalCommand struct {
	Query          BrowserSourceQuery
	Declaration    model.FinalDeliveryDeclaration
	IdempotencyKey string
}
type BrowserDeliverySummaryCommand struct {
	Query          BrowserSourceQuery
	Summary        model.UnretainedDeliverySummary
	IdempotencyKey string
}
type BrowserDeliveryAppendCommand struct {
	Query          BrowserSourceQuery
	Batch          model.BrowserActivityBatch
	IdempotencyKey string
}

func (service *Service) beginBrowserDeliveryAudit(ctx context.Context, call Call, access store.BrowserDeliveryAccess, operation string) (string, error) {
	target, err := service.deps.Persistence.ResolveBrowserDeliveryTarget(ctx, access)
	if err != nil {
		return "", mapBrowserDeliveryError(err)
	}
	if target == nil || !target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return "", unavailable(model.ErrDeliveryInvalid)
	}
	return service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass, target.ClassID.String(), operation, map[string]any{"source_session_id": string(access.SourceSessionID)})
}
func (service *Service) DeclareBrowserDeliveryGaps(ctx context.Context, call Call, command BrowserDeliveryGapsCommand) (*model.BrowserDeliveryGapResult, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := browserSourceSelector(call, command.Query)
	if err != nil {
		return nil, err
	}
	if command.Declaration.Validate() != nil {
		return nil, invalid("delivery_gaps")
	}
	idempotency, err := prepareIdempotency(call, store.BrowserDeliveryGapsOperation, command.IdempotencyKey, struct {
		AttemptID   model.ExamAttemptID
		StreamID    string
		Declaration model.DeclareDeliveryGaps
	}{access.Access.AttemptID, string(access.SourceSessionID), command.Declaration})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginBrowserDeliveryAudit(ctx, call, access, store.BrowserDeliveryGapsOperation)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.DeclareBrowserDeliveryGaps(ctx, &store.BrowserDeliveryGapDeclaration{Access: access, Declaration: command.Declaration, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		var capacity *model.DeliveryMetadataCapacity
		if errors.As(err, &capacity) {
			return nil, service.browserDeliveryFailure(ctx, access, err)
		}
		var refusal *store.BrowserDeliveryRefusal
		if errors.As(err, &refusal) {
			return nil, service.browserDeliveryFailure(ctx, access, err)
		}
		return nil, service.failAudit(ctx, audit, service.browserDeliveryFailure(ctx, access, err))
	}
	if value == nil || value.Receipt.DeclarationID != command.Declaration.DeclarationID || value.Receipt.DeclarationRevision < 1 || !model.IsValidSHA256Fingerprint(value.Receipt.RequestDigest) || value.Status.Validate() != nil || value.Status.SourceSessionID != access.SourceSessionID {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) SealBrowserDelivery(ctx context.Context, call Call, command BrowserDeliveryFinalCommand) (*model.BrowserSourceStatus, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := browserSourceSelector(call, command.Query)
	if err != nil {
		return nil, err
	}
	if command.Declaration.Validate() != nil {
		return nil, invalid("delivery_final")
	}
	idempotency, err := prepareIdempotency(call, store.BrowserDeliveryFinalOperation, command.IdempotencyKey, struct {
		AttemptID   model.ExamAttemptID
		StreamID    string
		Declaration model.FinalDeliveryDeclaration
	}{access.Access.AttemptID, string(access.SourceSessionID), command.Declaration})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginBrowserDeliveryAudit(ctx, call, access, store.BrowserDeliveryFinalOperation)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.SealBrowserDelivery(ctx, &store.BrowserDeliveryFinalDeclaration{Access: access, Declaration: command.Declaration, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		var capacity *model.DeliveryMetadataCapacity
		if errors.As(err, &capacity) {
			return nil, service.browserDeliveryFailure(ctx, access, err)
		}
		var refusal *store.BrowserDeliveryRefusal
		if errors.As(err, &refusal) {
			return nil, service.browserDeliveryFailure(ctx, access, err)
		}
		return nil, service.failAudit(ctx, audit, service.browserDeliveryFailure(ctx, access, err))
	}
	if value == nil || value.Validate() != nil || value.SourceSessionID != access.SourceSessionID {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	return value, nil
}

func mapBrowserDeliveryError(err error) error {
	if errors.Is(err, store.ErrInvalidState) {
		return unavailable(err)
	}
	var refusal *store.BrowserDeliveryRefusal
	if errors.As(err, &refusal) {
		return &Fault{Code: "exam.delivery." + refusal.Reason, Cause: err}
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) && conflict.Resource == "browser_delivery" {
		return &Fault{Code: "exam.delivery." + conflict.Constraint, Cause: err}
	}
	return mapNativeDeliveryError(err)
}
func (service *Service) UpdateBrowserDeliverySummary(ctx context.Context, call Call, command BrowserDeliverySummaryCommand) (*model.BrowserDeliverySummaryResult, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := browserSourceSelector(call, command.Query)
	if err != nil {
		return nil, err
	}
	if command.Summary.Validate() != nil {
		return nil, invalid("delivery_summary")
	}
	idem, err := prepareIdempotency(call, store.BrowserDeliverySummaryOperation, command.IdempotencyKey, struct {
		Attempt model.ExamAttemptID
		Source  model.BrowserSourceSessionID
		Summary model.UnretainedDeliverySummary
	}{access.Access.AttemptID, access.SourceSessionID, command.Summary})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginBrowserDeliveryAudit(ctx, call, access, store.BrowserDeliverySummaryOperation)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.UpdateBrowserDeliverySummary(ctx, &store.BrowserDeliverySummaryUpdate{Access: access, Summary: command.Summary, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idem)
	if err != nil {
		return nil, service.failAudit(ctx, audit, service.browserDeliveryFailure(ctx, access, err))
	}
	if value == nil || value.Summary.Validate() != nil || value.Status.Validate() != nil || value.Status.SourceSessionID != access.SourceSessionID {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) BrowserDeliveryReceipts(ctx context.Context, call Call, query BrowserSourceQuery, first int64, limit int) (*model.BrowserReceiptPage, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := browserSourceSelector(call, query)
	if err != nil {
		return nil, err
	}
	if first < 1 || first > model.BrowserParticipationPositionLimit || limit < 1 || limit > 64 {
		return nil, invalid("receipt_page")
	}
	value, err := service.deps.Persistence.BrowserDeliveryReceipts(ctx, access, first, limit)
	if err != nil {
		return nil, service.browserDeliveryFailure(ctx, access, err)
	}
	if value == nil || value.Receipts == nil || len(value.Receipts) > limit {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	previous := first - 1
	for _, receipt := range value.Receipts {
		if receipt.Validate() != nil || receipt.Sequence <= previous {
			return nil, unavailable(model.ErrDeliveryInvalid)
		}
		previous = receipt.Sequence
	}
	if value.NextSequence != nil && (*value.NextSequence <= previous || *value.NextSequence > model.BrowserParticipationPositionLimit || len(value.Receipts) != limit) {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) AppendHistoricalBrowserDelivery(ctx context.Context, call Call, command BrowserDeliveryAppendCommand) (*model.BrowserActivityAcknowledgement, error) {
	access, err := browserSourceSelector(call, command.Query)
	if err != nil {
		return nil, err
	}
	if command.Batch.Validate() != nil || command.Batch.SourceSessionID != access.SourceSessionID || access.Access.ConnectionID != "" || access.Access.ContinuityCredentialHash != "" {
		return nil, invalid("historical_browser_append")
	}
	access.ParticipationID = command.Batch.ParticipationID
	idem, err := prepareIdempotency(call, store.BrowserDeliveryAppendOperation, command.IdempotencyKey, struct {
		Attempt model.ExamAttemptID
		Batch   model.BrowserActivityBatch
	}{access.Access.AttemptID, command.Batch})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginBrowserDeliveryAudit(ctx, call, access, store.BrowserDeliveryAppendOperation)
	if err != nil {
		return nil, err
	}
	b := command.Batch
	value, err := service.deps.Persistence.AppendHistoricalBrowserDelivery(ctx, &store.BrowserActivityAppend{Access: access.Access, SourceSessionID: b.SourceSessionID, ParticipationID: b.ParticipationID, Generation: b.Generation, PolicyRevisionID: b.PolicyRevisionID, PolicyDigest: b.PolicyDigest, Events: b.Events, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idem)
	if err != nil {
		var refusal *store.BrowserDeliveryRefusal
		if errors.As(err, &refusal) {
			return nil, service.browserDeliveryFailure(ctx, access, err)
		}
		return nil, service.failAudit(ctx, audit, service.browserDeliveryFailure(ctx, access, err))
	}
	if !validBrowserActivityAcknowledgement(value, b.SourceSessionID, b.Events) {
		return nil, unavailable(model.ErrDeliveryInvalid)
	}
	return value, nil
}
