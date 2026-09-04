// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExamAuthoringRowFailsClosed(t *testing.T) {
	t.Parallel()
	valid := validExamAuthoringRow(t)
	tests := []struct {
		name   string
		mutate func(*examAuthoringRow)
		entity string
		field  string
	}{
		{name: "exam id", mutate: func(row *examAuthoringRow) { row.ID = "bad" }, entity: "exam", field: "id"},
		{name: "owner id", mutate: func(row *examAuthoringRow) { row.OwnerUserID = "bad" }, entity: "exam", field: "owner_user_id"},
		{name: "policy", mutate: func(row *examAuthoringRow) { row.Policy = jsonValue(`{"schema_version":2}`) }, entity: "exam_draft", field: "policy"},
		{name: "zero managers", mutate: func(row *examAuthoringRow) { row.ManagerCount = 0 }, entity: "exam", field: "aggregate"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			row := valid
			test.mutate(&row)
			_, err := row.model()
			var persisted *persistedStateError
			if !errors.As(err, &persisted) || persisted.Entity != test.entity || persisted.Field != test.field {
				t.Fatalf("model error = %v, want %s.%s persisted-state error", err, test.entity, test.field)
			}
		})
	}
}

func TestExamAccessRowFailsClosed(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	row := examAccessRow{
		ID: model.NewExamID().String(), AcademicUnitID: model.NewAcademicUnitID().String(),
		CreatorUserID: model.NewUserID().String(), OwnerUserID: model.NewUserID().String(),
		CreatedAt: at, UpdatedAt: at, Revision: 1,
	}
	row.AcademicUnitID = "bad"
	_, err := row.model()
	var persisted *persistedStateError
	if !errors.As(err, &persisted) || persisted.Entity != "exam" || persisted.Field != "academic_unit_id" {
		t.Fatalf("model error = %v, want exam.academic_unit_id persisted-state error", err)
	}
}

func TestPrepareExamAuthoringCreationRejectsBrokenAggregate(t *testing.T) {
	t.Parallel()
	at := time.Now().UTC()
	exam, err := model.NewExam(model.NewExamID(), model.NewAcademicUnitID(), model.NewUserID(), at)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := model.NewExamDraft(exam.ID, "Test", "", model.DefaultExamPolicySet(), at)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := model.NewExamManager(exam.ID, model.NewUserID(), exam.CreatorUserID, at)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = prepareExamAuthoringCreation(&store.ExamAuthoringCreation{
		Exam: exam, Draft: draft, Manager: manager, AuditEventID: model.NewId(), AuditAt: model.GetMillis(),
	})
	var invalid *store.ErrInvalidInput
	if !errors.As(err, &invalid) || invalid.Field != "aggregate" {
		t.Fatalf("error = %v, want aggregate invalid input", err)
	}
}

func validExamAuthoringRow(t *testing.T) examAuthoringRow {
	t.Helper()
	at := time.Now().UTC()
	policy, err := model.EncodeExamPolicySet(model.DefaultExamPolicySet())
	if err != nil {
		t.Fatal(err)
	}
	profile, err := model.EncodeExecutionProfile(model.DefaultExecutionProfile())
	if err != nil {
		t.Fatal(err)
	}
	browserPolicy, err := model.EncodeBrowserPolicy(model.DisabledBrowserPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return examAuthoringRow{
		ID: model.NewId(), AcademicUnitID: model.NewId(), CreatorUserID: model.NewId(), OwnerUserID: model.NewId(),
		DefaultRevisionID: sql.NullString{}, CreatedAt: at, UpdatedAt: at, ExamRevision: 1,
		DraftTitle: "Test", Policy: jsonValue(policy), ExecutionProfile: jsonValue(profile), BrowserPolicy: jsonValue(browserPolicy),
		BaseRevisionID: sql.NullString{}, DraftUpdatedAt: at,
		DraftRevision: 1, ManagerCount: 1, ActorIsManager: true,
		OwnerIsManager: true,
	}
}
