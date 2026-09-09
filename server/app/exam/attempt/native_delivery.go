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

type NativeDeliveryQuery struct {
	Access   CandidateAccess
	StreamID string
}
type NativeDeliveryGapsCommand struct {
	Query          NativeDeliveryQuery
	Declaration    model.DeclareDeliveryGaps
	IdempotencyKey string
}
type NativeDeliveryFinalCommand struct {
	Query          NativeDeliveryQuery
	Declaration    model.FinalDeliveryDeclaration
	IdempotencyKey string
}
type NativeDeliverySummaryCommand struct {
	IdempotencyKey string
	Query          NativeDeliveryQuery
	Summary        model.UnretainedDeliverySummary
}

func nativeDeliverySelector(call Call, query NativeDeliveryQuery) (store.NativeDeliveryAccess, error) {
	p := call.Principal()
	if p.Validate() != nil || p.CredentialType != model.CredentialSessionAccess {
		return store.NativeDeliveryAccess{}, &Fault{Code: "authentication.invalid_token"}
	}
	if !p.HasRegisteredDesktopKey() {
		return store.NativeDeliveryAccess{}, &Fault{Code: "exam.attempt.registered_desktop_required"}
	}
	a := query.Access
	if !a.AttemptID.IsValid() || !model.IsValidAgreementID(query.StreamID) || a.ConnectionID != "" && !a.ConnectionID.IsValid() || a.ContinuityCredential != "" && !model.IsValidCredentialToken(a.ContinuityCredential) || (a.ConnectionID == "") != (a.ContinuityCredential == "") {
		return store.NativeDeliveryAccess{}, invalid("native_delivery_access")
	}
	credentialHash := ""
	if a.ContinuityCredential != "" {
		credentialHash = model.HashToken(a.ContinuityCredential)
	}
	return store.NativeDeliveryAccess{Access: store.CandidateAttemptAccess{AttemptID: a.AttemptID, ConnectionID: a.ConnectionID, ContinuityCredentialHash: credentialHash, CandidateUserID: p.UserID, SessionID: p.SessionID, DesktopRegistrationID: p.DesktopRegistrationID, DPoPKeyThumbprint: p.DPoPKeyThumbprint}, StreamID: query.StreamID}, nil
}

func (service *Service) NativeDeliveryStatus(ctx context.Context, call Call, query NativeDeliveryQuery) (*model.NativeSecurityStreamStatus, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	a, err := nativeDeliverySelector(call, query)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.NativeDeliveryStatus(ctx, a)
	if err != nil {
		return nil, service.nativeDeliveryFailure(ctx, a, err)
	}
	if value == nil || value.Validate() != nil || value.StreamID != query.StreamID {
		return nil, unavailable(model.ErrNativeDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) NativeDeliveryReceipt(ctx context.Context, call Call, query NativeDeliveryQuery, sequence int64) (*model.NativeBatchReceipt, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	a, err := nativeDeliverySelector(call, query)
	if err != nil {
		return nil, err
	}
	if sequence < 1 || sequence > model.NativeParticipationPositionLimit {
		return nil, invalid("batch_sequence")
	}
	value, err := service.deps.Persistence.NativeDeliveryReceipt(ctx, a, sequence)
	if err != nil {
		return nil, service.nativeDeliveryFailure(ctx, a, err)
	}
	if value == nil || value.Validate() != nil || value.StreamID != query.StreamID || value.BatchSequence != sequence {
		return nil, unavailable(model.ErrNativeDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) beginNativeDeliveryAudit(ctx context.Context, call Call, access store.NativeDeliveryAccess, operation string) (string, error) {
	target, err := service.deps.Persistence.ResolveNativeDeliveryTarget(ctx, access)
	if err != nil {
		return "", mapNativeDeliveryError(err)
	}
	if target == nil || !target.SittingID.IsValid() || !target.ClassID.IsValid() {
		return "", unavailable(model.ErrNativeDeliveryInvalid)
	}
	return service.deps.Auditor.Begin(ctx, call, model.ActionExamSittingParticipate, model.Resource{Type: model.ResourceExamSitting, ID: target.SittingID.String()}, model.RoleScopeClass, target.ClassID.String(), operation, map[string]any{"stream_id": access.StreamID})
}
func (service *Service) DeclareNativeDeliveryGaps(ctx context.Context, call Call, command NativeDeliveryGapsCommand) (*model.DeliveryGapReceipt, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := nativeDeliverySelector(call, command.Query)
	if err != nil {
		return nil, err
	}
	if command.Declaration.Validate() != nil {
		return nil, invalid("delivery_gaps")
	}
	idempotency, err := prepareIdempotency(call, store.NativeDeliveryGapsOperation, command.IdempotencyKey, struct {
		AttemptID   model.ExamAttemptID
		StreamID    string
		Declaration model.DeclareDeliveryGaps
	}{access.Access.AttemptID, access.StreamID, command.Declaration})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginNativeDeliveryAudit(ctx, call, access, store.NativeDeliveryGapsOperation)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.DeclareNativeDeliveryGaps(ctx, &store.NativeDeliveryGapDeclaration{Access: access, Declaration: command.Declaration, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		var capacity *model.DeliveryMetadataCapacity
		if errors.As(err, &capacity) {
			return nil, service.nativeDeliveryFailure(ctx, access, err)
		}
		var refusal *store.NativeDeliveryRefusal
		if errors.As(err, &refusal) {
			return nil, service.nativeDeliveryFailure(ctx, access, err)
		}
		return nil, service.failAudit(ctx, audit, service.nativeDeliveryFailure(ctx, access, err))
	}
	if value == nil || value.DeclarationID != command.Declaration.DeclarationID || value.DeclarationRevision < 1 || !model.IsValidSHA256Fingerprint(value.RequestDigest) {
		return nil, unavailable(model.ErrNativeDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) SealNativeDelivery(ctx context.Context, call Call, command NativeDeliveryFinalCommand) (*model.NativeSecurityStreamStatus, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := nativeDeliverySelector(call, command.Query)
	if err != nil {
		return nil, err
	}
	if command.Declaration.Validate() != nil {
		return nil, invalid("delivery_final")
	}
	idempotency, err := prepareIdempotency(call, store.NativeDeliveryFinalOperation, command.IdempotencyKey, struct {
		AttemptID   model.ExamAttemptID
		StreamID    string
		Declaration model.FinalDeliveryDeclaration
	}{access.Access.AttemptID, access.StreamID, command.Declaration})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginNativeDeliveryAudit(ctx, call, access, store.NativeDeliveryFinalOperation)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.SealNativeDelivery(ctx, &store.NativeDeliveryFinalDeclaration{Access: access, Declaration: command.Declaration, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		var capacity *model.DeliveryMetadataCapacity
		if errors.As(err, &capacity) {
			return nil, service.nativeDeliveryFailure(ctx, access, err)
		}
		var refusal *store.NativeDeliveryRefusal
		if errors.As(err, &refusal) {
			return nil, service.nativeDeliveryFailure(ctx, access, err)
		}
		return nil, service.failAudit(ctx, audit, service.nativeDeliveryFailure(ctx, access, err))
	}
	if value == nil || value.Validate() != nil || value.StreamID != access.StreamID {
		return nil, unavailable(model.ErrNativeDeliveryInvalid)
	}
	return value, nil
}
func (service *Service) UpdateNativeDeliverySummary(ctx context.Context, call Call, command NativeDeliverySummaryCommand) (*model.NativeSecurityStreamStatus, error) {
	release, admissionErr := service.enterControl(ctx, call, false)
	if admissionErr != nil {
		return nil, admissionErr
	}
	defer release()
	access, err := nativeDeliverySelector(call, command.Query)
	if err != nil {
		return nil, err
	}
	if command.Summary.Validate() != nil {
		return nil, invalid("delivery_summary")
	}
	idempotency, err := prepareIdempotency(call, store.NativeDeliverySummaryOperation, command.IdempotencyKey, struct {
		AttemptID model.ExamAttemptID
		StreamID  string
		Summary   model.UnretainedDeliverySummary
	}{access.Access.AttemptID, access.StreamID, command.Summary})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginNativeDeliveryAudit(ctx, call, access, store.NativeDeliverySummaryOperation)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.UpdateNativeDeliverySummary(ctx, &store.NativeDeliverySummaryUpdate{Access: access, Summary: command.Summary, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		return nil, service.failAudit(ctx, audit, service.nativeDeliveryFailure(ctx, access, err))
	}
	if value == nil || value.Validate() != nil || value.StreamID != access.StreamID {
		return nil, unavailable(model.ErrNativeDeliveryInvalid)
	}
	return value, nil
}

func mapNativeDeliveryError(err error) error {
	if errors.Is(err, store.ErrInvalidState) {
		return unavailable(err)
	}
	var refusal *store.NativeDeliveryRefusal
	if errors.As(err, &refusal) {
		return &Fault{Code: "exam.delivery." + refusal.Reason, Cause: err}
	}
	if errors.Is(err, model.ErrDeliveryExpired) {
		return &Fault{Code: "exam.delivery.upload_expired", Cause: err}
	}
	if errors.Is(err, model.ErrDeliveryConflict) {
		return &Fault{Code: "exam.delivery.declaration_conflict", Cause: err}
	}
	if errors.Is(err, model.ErrDeliveryInvalid) || errors.Is(err, model.ErrNativeDeliveryInvalid) {
		return invalidCause("native_delivery", err)
	}
	var conflict *store.ErrConflict
	if errors.As(err, &conflict) && conflict.Resource == "native_delivery" {
		return &Fault{Code: "exam.delivery." + conflict.Constraint, Cause: err}
	}
	return mapStore(err)
}

type NativeDeliveryAppendCommand struct {
	Query          NativeDeliveryQuery
	Batch          model.NativeSecurityBatch
	IdempotencyKey string
}

func (service *Service) AppendNativeDelivery(ctx context.Context, call Call, command NativeDeliveryAppendCommand) (*model.NativeSecurityAcknowledgement, error) {
	access, err := nativeDeliverySelector(call, command.Query)
	if err != nil {
		return nil, err
	}
	if command.Batch.Validate() != nil || command.Batch.StreamID != access.StreamID {
		return nil, invalid("native_batch")
	}
	access.ParticipationID, access.Generation = command.Batch.ParticipationID, command.Batch.Generation
	target, err := service.deps.Persistence.ResolveNativeDeliveryTarget(ctx, access)
	if err != nil {
		return nil, service.nativeDeliveryFailure(ctx, access, err)
	}
	if target == nil || target.Security.Validate() != nil {
		return nil, unavailable(model.ErrNativeDeliveryInvalid)
	}
	policy := target.Security.Policy
	build, err := service.deps.DesktopBuilds.ResolveNativeDeliveryBuild(ctx, NativeDeliveryBuildIdentity{ReleaseID: policy.ApplicationReleaseID, MatrixID: policy.MatrixID, MatrixDigest: target.MatrixDigest, TargetTuple: policy.TargetTuple})
	if err != nil {
		return nil, unavailable(err)
	}
	idempotency, err := prepareIdempotency(call, store.NativeDeliveryAppendOperation, command.IdempotencyKey, struct {
		AttemptID model.ExamAttemptID
		Batch     model.NativeSecurityBatch
	}{access.Access.AttemptID, command.Batch})
	if err != nil {
		return nil, err
	}
	audit, err := service.beginNativeDeliveryAudit(ctx, call, access, store.NativeDeliveryAppendOperation)
	if err != nil {
		return nil, err
	}
	value, err := service.deps.Persistence.AppendNativeDelivery(ctx, &store.NativeDeliveryAppend{Access: access, Batch: command.Batch, DesktopBuild: build, AuditEventID: audit, AuditAt: model.MillisFromTime(service.deps.Now())}, idempotency)
	if err != nil {
		var capacity *model.DeliveryMetadataCapacity
		if errors.As(err, &capacity) {
			return nil, service.nativeDeliveryFailure(ctx, access, err)
		}
		var refusal *store.NativeDeliveryRefusal
		if errors.As(err, &refusal) {
			return nil, service.nativeDeliveryFailure(ctx, access, err)
		}
		return nil, service.failAudit(ctx, audit, service.nativeDeliveryFailure(ctx, access, err))
	}
	if value == nil || value.Receipt.Validate() != nil || value.Receipt.StreamID != access.StreamID || value.Receipt.BatchSequence != command.Batch.BatchSequence {
		return nil, unavailable(model.ErrNativeDeliveryInvalid)
	}
	return value, nil
}
