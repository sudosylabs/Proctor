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

	examsitting "github.com/sudosylabs/proctor/server/app/exam/sitting"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestSystemCriticalAuditHasNoFabricatedActor(t *testing.T) {
	t.Parallel()
	audits := &systemAuditStoreFake{}
	service, err := newAuditService(audits, securityInstitutionStore{}, "node-system")
	if err != nil {
		t.Fatal(err)
	}
	sittingID, unitID := model.NewExamSittingID(), model.NewAcademicUnitID()
	jobID, attemptID := model.NewJobID(), model.NewJobAttemptID()
	event, err := service.BeginSystemCriticalActionAtScope(context.Background(), model.ActionExamSittingManage,
		model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}, model.RoleScopeAcademicUnit, unitID.String(),
		map[string]any{"job_id": jobID.String(), "job_attempt_id": attemptID.String(), "trigger": "deadline"})
	if err != nil {
		t.Fatal(err)
	}
	if event.ActorID != "" || event.SessionID != "" || event.NodeID != "node-system" || event.Status != model.AuditStatusAttempt {
		t.Fatalf("system audit identity = %#v", event)
	}
	if event.Resource != (model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()}) || event.ScopeID != unitID.String() {
		t.Fatalf("system audit target = %#v", event)
	}
}

func TestExamSittingSystemAuditAdapterRetainsJobIdentityAtAcademicUnitScope(t *testing.T) {
	t.Parallel()
	audits := &systemAuditStoreFake{}
	service, err := newAuditService(audits, securityInstitutionStore{}, "node-system")
	if err != nil {
		t.Fatal(err)
	}
	sittingID, unitID := model.NewExamSittingID(), model.NewAcademicUnitID()
	call := examsitting.SystemCall{JobID: model.NewJobID(), AttemptID: model.NewJobAttemptID()}
	id, err := (examSittingSystemAuditAdapter{audit: service}).Begin(context.Background(), call,
		model.ActionExamSittingManage, model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()},
		model.RoleScopeAcademicUnit, unitID.String(), "advance_due", map[string]any{
			"exam_sitting_id": sittingID.String(),
		})
	if err != nil {
		t.Fatal(err)
	}
	if audits.saved == nil || id != audits.saved.ID.String() || !audits.saved.ActorID.IsZero() || !audits.saved.SessionID.IsZero() ||
		audits.saved.ScopeType != model.RoleScopeAcademicUnit || audits.saved.ScopeID != unitID.String() {
		t.Fatalf("system audit = %#v", audits.saved)
	}
	want := `{"operation":"advance_due","value":{"exam_sitting_id":"` + sittingID.String() + `","job_attempt_id":"` + call.AttemptID.String() + `","job_id":"` + call.JobID.String() + `"}}`
	if string(audits.saved.Parameters) != want {
		t.Fatalf("parameters = %s, want %s", audits.saved.Parameters, want)
	}
}

type systemAuditStoreFake struct {
	store.AuditStore
	saved *model.AuditEvent
}

func (fake *systemAuditStoreFake) Save(_ context.Context, event *model.AuditEvent) (*model.AuditEvent, error) {
	copy := event.Clone()
	copy.PrepareCreate(model.NewAuditEventID(), time.Date(2026, time.August, 16, 9, 0, 0, 0, time.UTC))
	if err := copy.Validate(); err != nil {
		return nil, err
	}
	fake.saved = copy.Clone()
	return copy, nil
}
