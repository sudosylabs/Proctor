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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
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
			fixture.service.deps.Lifecycle = lifecycle
			command := ChangeRetentionControlCommand{ExpectedRevision: 1, ExpectedPolicyRevision: 1, PreviewID: model.NewRetentionPreviewID(), State: model.RetentionControlEnabled, IdempotencyKey: "approve-cleanup"}
			call := fixture.invocation
			switch gate {
			case "authority":
				fixture.authorization.err = apperror.New("authorization.denied")
			case "audit":
				fixture.audit.beginErr = errors.New("audit unavailable")
			case "assurance":
				p := call.Principal()
				p.AuthenticationStrength = model.AuthenticationSingleFactor
				p.MFACompletedAt = model.OptionalTime{}
				call = invocation.New(p, call.RequestMetadata())
			case "missing_preview":
				command.PreviewID = ""
			case "missing_revision":
				command.ExpectedPolicyRevision = 0
			case "missing_key":
				command.IdempotencyKey = ""
			}
			application := &App{retentionPolicyService: fixture.service}
			value, err := application.ChangeRetentionControl(t.Context(), call, command)
			require.Error(t, err, "%s did not stop approval", gate)
			require.Nil(t, value, "%s did not stop approval", gate)
			require.Nil(t, lifecycle.change, "%s did not stop approval", gate)
		})
	}
}

func TestRetentionApprovalPassesExactReviewedPolicyAndCredential(t *testing.T) {
	fixture := newRetentionPolicyFixture(t)
	control := &model.RetentionControl{InstitutionID: fixture.institution.ID, Revision: 2, State: model.RetentionControlEnabled,
		ApprovedPolicyRevision: 7, ApprovedPreviewID: model.NewRetentionPreviewID(), ChangedByUserID: fixture.invocation.Principal().UserID, UpdatedAt: fixture.service.deps.Now()}
	lifecycle := &retentionLifecycleFake{result: &store.RetentionControlResult{Control: control}}
	fixture.service.deps.Lifecycle = lifecycle
	application := &App{retentionPolicyService: fixture.service}
	value, err := application.ChangeRetentionControl(t.Context(), fixture.invocation, ChangeRetentionControlCommand{
		ExpectedRevision: 1, ExpectedPolicyRevision: 7, PreviewID: control.ApprovedPreviewID, State: model.RetentionControlEnabled, IdempotencyKey: "approve-cleanup"})
	require.NoError(t, err, "approval")
	require.Same(t, control, value, "approval")
	require.Equal(t, int64(7), lifecycle.change.ExpectedPolicyRevision, "lost review, authority or atomic audit fence: %+v, %+v", lifecycle.change, lifecycle.command)
	require.Equal(t, control.ApprovedPreviewID, lifecycle.change.PreviewID, "lost review, authority or atomic audit fence: %+v, %+v", lifecycle.change, lifecycle.command)
	require.Equal(t, fixture.invocation.Principal().CredentialID, lifecycle.change.Principal.CredentialID, "lost review, authority or atomic audit fence: %+v, %+v", lifecycle.change, lifecycle.command)
	require.Equal(t, model.AuditEventID(fixture.audit.beginID), lifecycle.change.AuditEventID, "lost review, authority or atomic audit fence: %+v, %+v", lifecycle.change, lifecycle.command)
	require.Positive(t, int64(lifecycle.change.RecentAuthenticationTTL), "lost review, authority or atomic audit fence: %+v, %+v", lifecycle.change, lifecycle.command)
	require.Equal(t, store.RetentionControlOperation, lifecycle.command.Operation, "lost review, authority or atomic audit fence: %+v, %+v", lifecycle.change, lifecycle.command)
	require.Equal(t, model.ActionRetentionCleanupManage, fixture.authorization.action, "cleanup approval reused configuration permission")
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
		t.Run(test.code, func(t *testing.T) {
			got := retentionError(test.err)
			require.True(t, apperror.Is(got, test.code), "error = %v; want %s", got, test.code)
		})
	}
}
