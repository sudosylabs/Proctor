// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type retentionLifecycleFake struct {
	store.RetentionStore
	change  *store.RetentionControlChange
	command *store.CommandIdempotency
	result  *store.RetentionControlResult
	err     error
}

func (f *retentionLifecycleFake) ChangeControl(_ context.Context, change *store.RetentionControlChange, command *store.CommandIdempotency) (*store.RetentionControlResult, error) {
	f.change, f.command = change, command
	return f.result, f.err
}

func TestRetentionApprovalRequiresAuthorityAndCriticalAudit(t *testing.T) {
	for _, gate := range []string{"authority", "audit", "assurance", "missing_preview", "missing_revision", "missing_key"} {
		t.Run(gate, func(t *testing.T) {
			fixture := newRetentionPolicyFixture(t)
			lifecycle := &retentionLifecycleFake{}
			fixture.service.lifecycle = lifecycle
			command := ChangeRetentionControlCommand{ExpectedRevision: 1, ExpectedPolicyRevision: 1, PreviewID: model.NewRetentionPreviewID(), State: model.RetentionControlEnabled, IdempotencyKey: "approve-cleanup"}
			invocation := fixture.invocation
			switch gate {
			case "authority":
				fixture.authorization.err = NewError("authorization.denied")
			case "audit":
				fixture.audit.beginErr = errors.New("audit unavailable")
			case "assurance":
				p := invocation.Principal()
				p.AuthenticationStrength = model.AuthenticationSingleFactor
				p.MFACompletedAt = model.OptionalTime{}
				invocation = NewInvocation(p, invocation.RequestMetadata())
			case "missing_preview":
				command.PreviewID = ""
			case "missing_revision":
				command.ExpectedPolicyRevision = 0
			case "missing_key":
				command.IdempotencyKey = ""
			}
			application := &App{retentionPolicy: fixture.service}
			value, err := application.ChangeRetentionControl(t.Context(), invocation, command)
			if err == nil || value != nil || lifecycle.change != nil {
				t.Fatalf("%s did not stop approval: %+v, %v, %+v", gate, value, err, lifecycle.change)
			}
		})
	}
}

func TestRetentionApprovalPassesExactReviewedPolicyAndCredential(t *testing.T) {
	fixture := newRetentionPolicyFixture(t)
	control := &model.RetentionControl{InstitutionID: fixture.institution.ID, Revision: 2, State: model.RetentionControlEnabled,
		ApprovedPolicyRevision: 7, ApprovedPreviewID: model.NewRetentionPreviewID(), ChangedByUserID: fixture.invocation.Principal().UserID, UpdatedAt: fixture.service.now()}
	lifecycle := &retentionLifecycleFake{result: &store.RetentionControlResult{Control: control}}
	fixture.service.lifecycle = lifecycle
	application := &App{retentionPolicy: fixture.service}
	value, err := application.ChangeRetentionControl(t.Context(), fixture.invocation, ChangeRetentionControlCommand{
		ExpectedRevision: 1, ExpectedPolicyRevision: 7, PreviewID: control.ApprovedPreviewID, State: model.RetentionControlEnabled, IdempotencyKey: "approve-cleanup"})
	if err != nil || value != control {
		t.Fatalf("approval = %+v, %v", value, err)
	}
	if lifecycle.change.ExpectedPolicyRevision != 7 || lifecycle.change.PreviewID != control.ApprovedPreviewID ||
		lifecycle.change.Principal.CredentialID != fixture.invocation.Principal().CredentialID || lifecycle.change.AuditEventID != fixture.audit.beginID ||
		lifecycle.change.RecentAuthenticationTTL <= 0 || lifecycle.command.Operation != store.RetentionControlOperation {
		t.Fatalf("lost review, authority or atomic audit fence: %+v, %+v", lifecycle.change, lifecycle.command)
	}
	if fixture.authorization.action != model.ActionRetentionCleanupManage {
		t.Fatal("cleanup approval reused configuration permission")
	}
}

func TestRetentionStoreConflictsStayBounded(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{store.NewErrConflict("retention_control", "preview_stale", nil), "retention.conflict"},
		{store.NewErrConflict("authorization", "assurance", nil), "authorization.denied"},
		{store.NewErrNotFound("retention_preview", model.NewRetentionPreviewID().String()), "resource.not_found"},
		{errors.New("private database error"), "retention.unavailable"},
	} {
		if got := retentionError(test.err); !Is(got, test.code) {
			t.Fatalf("error = %v; want %s", got, test.code)
		}
	}
}
