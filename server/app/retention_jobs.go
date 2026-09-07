// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"time"

	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type retentionJobAudit struct{ audit *auditService }

type retentionNoticeDispatcher struct {
	records store.RetentionStore
	users   store.UserStore
	mail    *appmail.Composer
	now     func() time.Time
}

func (d retentionNoticeDispatcher) DispatchRetentionNotice(ctx context.Context, notice model.RetentionNotice) error {
	input := &store.RetentionNoticeMail{RetirementID: notice.RetirementID, RecipientUserID: notice.RecipientUserID}
	if d.records == nil {
		return NewError("retention.unavailable")
	}
	if d.users == nil || d.now == nil {
		input.FailureCode = "mail.preparation_unavailable"
		return d.records.CompleteNotice(ctx, input)
	}
	user, err := d.users.Get(ctx, notice.RecipientUserID.String())
	if err != nil || user == nil {
		input.FailureCode = "mail.recipient_unavailable"
		return d.records.CompleteNotice(ctx, input)
	}
	if d.mail == nil {
		input.FailureCode = "mail.preparation_unavailable"
		return d.records.CompleteNotice(ctx, input)
	}
	input.Mail, err = d.mail.PrepareRetentionScheduled(appmail.NoticePreparation{Recipient: user, At: model.TimeUTC(d.now())}, notice.RetireAfter)
	if err != nil {
		input.Mail = nil
		input.FailureCode = "mail.preparation_unavailable"
	}
	return d.records.CompleteNotice(ctx, input)
}

func (a retentionJobAudit) BeginRetention(ctx context.Context, institutionID model.InstitutionID, submissionID model.SubmissionID) (string, error) {
	if a.audit == nil {
		return "", NewError("audit.unavailable")
	}
	event, err := a.audit.BeginSystemCriticalActionAtScope(ctx, model.ActionRetentionCleanupManage,
		model.Resource{Type: model.ResourceSubmission, ID: submissionID.String()}, model.RoleScopeInstitution, institutionID.String(),
		map[string]any{"operation": "retention.reconcile", "submission_id": submissionID.String()})
	if err != nil {
		return "", err
	}
	return event.ID.String(), nil
}

func (a retentionJobAudit) FailRetention(ctx context.Context, id string) error {
	if a.audit == nil {
		return NewError("audit.unavailable")
	}
	_, err := a.audit.CompleteCriticalAction(ctx, id, model.AuditStatusFail, "retention.unavailable", nil)
	return err
}

func (a retentionJobAudit) BeginRetentionExpiry(ctx context.Context, institutionID model.InstitutionID, kind model.RetentionExpiryKind, id string) (string, error) {
	if a.audit == nil || !kind.IsValid() || !model.IsValidId(id) {
		return "", NewError("audit.unavailable")
	}
	event, err := a.audit.BeginSystemCriticalActionAtScope(ctx, model.ActionRetentionCleanupManage,
		model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, model.RoleScopeInstitution, institutionID.String(),
		map[string]any{"operation": "retention.expire", "kind": string(kind), "record_id": id})
	if err != nil {
		return "", err
	}
	return event.ID.String(), nil
}

func (a retentionJobAudit) PrepareRetentionCleanupAudit(_ context.Context, institutionID model.InstitutionID) (*model.AuditEvent, error) {
	if a.audit == nil || a.audit.nodeID == "" || !institutionID.IsValid() {
		return nil, NewError("audit.unavailable")
	}
	parameters, err := model.EncodeAuditData(map[string]string{"operation": "retention.expire_cleanup_history"})
	if err != nil {
		return nil, NewError("audit.unavailable")
	}
	return &model.AuditEvent{Action: string(model.ActionRetentionCleanupManage), Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, ScopeType: model.RoleScopeInstitution, ScopeID: institutionID.String(), Status: model.AuditStatusAttempt, NodeID: a.audit.nodeID, ClientType: "system", Parameters: parameters}, nil
}
