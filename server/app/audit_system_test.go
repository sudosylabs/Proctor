// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	appinvocation "github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSystemCriticalAuditHasNoFabricatedActor(t *testing.T) {
	t.Parallel()
	audits := &systemAuditStoreFake{}
	service, err := newAuditService(audits, securityInstitutionStore{}, "node-system")
	require.NoError(t, err)
	sittingID, unitID := model.NewExamSittingID(), model.NewAcademicUnitID()
	jobID, attemptID := model.NewJobID(), model.NewJobAttemptID()
	event, err := service.BeginSystemCriticalActionAtScope(context.Background(), model.ActionExamSittingManage,
		model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, model.RoleScopeAcademicUnit, unitID.String(),
		map[string]any{"job_id": jobID.String(), "job_attempt_id": attemptID.String(), "trigger": "deadline"})
	require.NoError(t, err)
	require.Empty(t, event.ActorID)
	require.Empty(t, event.SessionID)
	require.Equal(t, "node-system", event.NodeID)
	require.Equal(t, model.AuditStatusAttempt, event.Status)
	require.Equal(t, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, event.Resource)
	require.Equal(t, unitID.String(), event.ScopeID)
}

func TestExamSittingSystemAuditAdapterRetainsJobIdentityAtAcademicUnitScope(t *testing.T) {
	t.Parallel()
	audits := &systemAuditStoreFake{}
	service, err := newAuditService(audits, securityInstitutionStore{}, "node-system")
	require.NoError(t, err)
	sittingID, unitID := model.NewExamSittingID(), model.NewAcademicUnitID()
	call := appinvocation.System{JobID: model.NewJobID(), AttemptID: model.NewJobAttemptID()}
	id, err := (examSittingSystemAuditAdapter{audit: service}).Begin(context.Background(), call,
		model.ActionExamSittingManage, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()},
		model.RoleScopeAcademicUnit, unitID.String(), "advance_due", map[string]any{
			"exam_sitting_id": sittingID.String(),
		})
	require.NoError(t, err)
	require.NotNil(t, audits.saved)
	require.Equal(t, audits.saved.ID, id)
	require.True(t, audits.saved.ActorID.IsZero())
	require.True(t, audits.saved.SessionID.IsZero())
	require.Equal(t, model.RoleScopeAcademicUnit, audits.saved.ScopeType)
	require.Equal(t, unitID.String(), audits.saved.ScopeID)
	want := `{"operation":"advance_due","value":{"exam_sitting_id":"` + sittingID.String() + `","job_attempt_id":"` + call.AttemptID.String() + `","job_id":"` + call.JobID.String() + `"}}`
	require.Equal(t, want, string(audits.saved.Parameters), "parameters")
}

type systemAuditStoreFake struct {
	store.AuditStore
	saved *model.AuditEvent
}

func (f *systemAuditStoreFake) Save(_ context.Context, event *model.AuditEvent) (*model.AuditEvent, error) {
	copy := event.Clone()
	copy.PrepareCreate(model.NewAuditEventID(), time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC))
	if err := copy.Validate(); err != nil {
		return nil, err
	}
	f.saved = copy.Clone()
	return copy, nil
}
