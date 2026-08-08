// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"testing"
	"time"
)

type persistentModel interface {
	PreSave()
	PreUpdate()
	IsValid() error
	Auditable() map[string]any
}

func TestAcademicModelsImplementLifecycleContract(t *testing.T) {
	t.Parallel()

	levelID := NewId()
	periodID := NewId()
	institutionID := NewId()

	tests := []struct {
		name  string
		model persistentModel
	}{
		{
			name: "academic period",
			model: &AcademicPeriod{
				InstitutionId: institutionID,
				Name:          "2026-2027",
				DisplayName:   "2026–2027",
				StartAt:       1788213600000,
				EndAt:         1819749600000,
			},
		},
		{
			name: "class",
			model: &Class{
				ProgrammeLevelId: levelID,
				AcademicPeriodId: periodID,
				Name:             "year-1-a",
				DisplayName:      "Year 1 - Class A",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.model.PreSave()
			if appErr := test.model.IsValid(); appErr != nil {
				t.Fatalf("model is invalid after PreSave: %v", appErr)
			}
			audit := test.model.Auditable()
			id, ok := audit["id"].(string)
			if !ok {
				t.Fatalf("audit id = %#v", audit["id"])
			}
			if !IsValidId(id) {
				t.Fatalf("id = %q", id)
			}
			if audit["id"] != id || audit["create_at"] == int64(0) || audit["update_at"] == int64(0) {
				t.Fatalf("audit fields = %#v", audit)
			}
			if _, exposed := audit["description"]; exposed {
				t.Fatalf("audit fields expose unbounded description: %#v", audit)
			}
			audit["id"] = "mutated"
			if test.model.Auditable()["id"] != id {
				t.Fatal("Auditable exposed mutable model state")
			}
			test.model.PreUpdate()
			if appErr := test.model.IsValid(); appErr != nil {
				t.Fatalf("model is invalid after PreUpdate: %v", appErr)
			}
		})
	}
}

func TestProgrammeAndProgrammeLevelTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	unitID, err := ParseAcademicUnitID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	programmeID, err := ParseProgrammeID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	programme, err := NewProgramme(
		programmeID, unitID, "computer-science", "Bachelor of Computer Science", "", at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := programme.Validate(); err != nil {
		t.Fatalf("Programme.Validate() = %v", err)
	}
	audit := programme.Auditable()
	if audit["id"] != programme.ID.String() ||
		audit["academic_unit_id"] != unitID.String() ||
		audit["created_at"] != MillisFromTime(at) {
		t.Fatalf("programme audit = %#v", audit)
	}
	if _, exposed := audit["description"]; exposed {
		t.Fatalf("programme audit exposes description: %#v", audit)
	}
	programme.PrepareUpdate(at.Add(time.Second))
	if err := programme.Validate(); err != nil {
		t.Fatalf("Programme after PrepareUpdate: %v", err)
	}

	levelID, err := ParseProgrammeLevelID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	level, err := NewProgrammeLevel(
		levelID, programmeID, "year-1", "Year 1", "", at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := level.Validate(); err != nil {
		t.Fatalf("ProgrammeLevel.Validate() = %v", err)
	}
	levelAudit := level.Auditable()
	if levelAudit["id"] != level.ID.String() ||
		levelAudit["programme_id"] != programmeID.String() ||
		levelAudit["created_at"] != MillisFromTime(at) {
		t.Fatalf("programme level audit = %#v", levelAudit)
	}
	level.PrepareUpdate(at.Add(time.Second))
	if err := level.Validate(); err != nil {
		t.Fatalf("ProgrammeLevel after PrepareUpdate: %v", err)
	}
}

func TestInstitutionAndAcademicUnitTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	institutionID, err := ParseInstitutionID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	institution, err := NewInstitution(
		institutionID, "northbridge", "Northbridge University", "Institution", at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := institution.Validate(); err != nil {
		t.Fatalf("Institution.Validate() = %v", err)
	}
	audit := institution.Auditable()
	if audit["id"] != institution.ID.String() ||
		audit["created_at"] != MillisFromTime(at) ||
		audit["updated_at"] != MillisFromTime(at) {
		t.Fatalf("institution audit = %#v", audit)
	}
	if _, exposed := audit["description"]; exposed {
		t.Fatalf("institution audit exposes description: %#v", audit)
	}
	institution.PrepareUpdate(at.Add(time.Second))
	if err := institution.Validate(); err != nil {
		t.Fatalf("Institution after PrepareUpdate: %v", err)
	}

	unitID, err := ParseAcademicUnitID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	unit, err := NewAcademicUnit(
		unitID, institutionID, "", "engineering", "College of Engineering", "", at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := unit.Validate(); err != nil {
		t.Fatalf("AcademicUnit.Validate() = %v", err)
	}
	unitAudit := unit.Auditable()
	if unitAudit["id"] != unit.ID.String() ||
		unitAudit["institution_id"] != institutionID.String() ||
		unitAudit["created_at"] != MillisFromTime(at) {
		t.Fatalf("academic unit audit = %#v", unitAudit)
	}
	unit.PrepareUpdate(at.Add(time.Second))
	if err := unit.Validate(); err != nil {
		t.Fatalf("AcademicUnit after PrepareUpdate: %v", err)
	}
}

func TestAcademicModelValidationReturnsPreciseTranslationIDs(t *testing.T) {
	t.Parallel()

	selfParentID, err := ParseAcademicUnitID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	institutionID, err := ParseInstitutionID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(1).UTC()

	tests := []struct {
		name string
		err  error
		code string
	}{
		{
			name: "self-parent academic unit",
			err: (&AcademicUnit{
				ID:            selfParentID,
				CreatedAt:     at,
				UpdatedAt:     at,
				Revision:      1,
				InstitutionID: institutionID,
				ParentID:      selfParentID,
				Name:          "computing",
				DisplayName:   "School of Computing",
			}).Validate(),
			code: "model.academic_unit.is_valid.parent_id.app_error",
		},
		{
			name: "academic period end",
			err: (&AcademicPeriod{
				Id:            NewId(),
				CreateAt:      1,
				UpdateAt:      1,
				InstitutionId: NewId(),
				Name:          "2026-2027",
				DisplayName:   "2026–2027",
				StartAt:       100,
				EndAt:         100,
			}).IsValid(),
			code: "model.academic_period.is_valid.end_at.app_error",
		},
		{
			name: "class academic period",
			err: (&Class{
				Id:               NewId(),
				CreateAt:         1,
				UpdateAt:         1,
				Revision:         1,
				ProgrammeLevelId: NewId(),
				Name:             "year-1-a",
				DisplayName:      "Year 1 - Class A",
			}).IsValid(),
			code: "model.class.is_valid.academic_period_id.app_error",
		},
		{
			name: "invalid programme name",
			err: (&Programme{
				ID:             NewProgrammeID(),
				CreatedAt:      at,
				UpdatedAt:      at,
				AcademicUnitID: NewAcademicUnitID(),
				Name:           "Bachelor of Computing",
				DisplayName:    "Bachelor of Computing",
			}).Validate(),
			code: "model.programme.is_valid.name.app_error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.err == nil {
				t.Fatal("validation returned nil")
			}
			validation, ok := test.err.(*ValidationError)
			if !ok {
				t.Fatalf("error type = %T", test.err)
			}
			if validation.Code != test.code {
				t.Fatalf("error code = %q, want %q", validation.Code, test.code)
			}
			if validation.SafeFields()["field"] == "" {
				t.Fatalf("safe fields = %#v", validation.SafeFields())
			}
		})
	}
}

func TestInstitutionSanitizesUnsafeUnicode(t *testing.T) {
	t.Parallel()

	id, err := ParseInstitutionID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	institution, err := NewInstitution(
		id,
		"northbridge",
		"North\u202Ebridge",
		"safe\u2066text",
		time.UnixMilli(1_700_000_000_000).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if institution.DisplayName != "Northbridge" || institution.Description != "safetext" {
		t.Fatalf("institution = %#v", institution)
	}
}
