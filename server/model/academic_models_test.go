// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"testing"
	"time"
)

// persistentModel is the legacy PreSave/IsValid contract still used by
// identity membership models until those aggregates are migrated.
type persistentModel interface {
	PreSave()
	PreUpdate()
	IsValid() error
	Auditable() map[string]any
}

func TestAcademicPeriodAndClassTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	institutionID, err := ParseInstitutionID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	periodID, err := ParseAcademicPeriodID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	period, err := NewAcademicPeriod(
		periodID, institutionID, "2026-2027", "2026–2027", "",
		time.UnixMilli(1788213600000).UTC(), time.UnixMilli(1819749600000).UTC(), at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := period.Validate(); err != nil {
		t.Fatalf("AcademicPeriod.Validate() = %v", err)
	}
	audit := period.Auditable()
	if audit["id"] != period.ID.String() ||
		audit["institution_id"] != institutionID.String() ||
		audit["created_at"] != MillisFromTime(at) ||
		audit["start_at"] != MillisFromTime(period.StartsAt) ||
		audit["end_at"] != MillisFromTime(period.EndsAt) {
		t.Fatalf("academic period audit = %#v", audit)
	}
	if _, exposed := audit["description"]; exposed {
		t.Fatalf("academic period audit exposes description: %#v", audit)
	}
	period.PrepareUpdate(at.Add(time.Second))
	if err := period.Validate(); err != nil {
		t.Fatalf("AcademicPeriod after PrepareUpdate: %v", err)
	}

	levelID, err := ParseProgrammeLevelID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	classID, err := ParseClassID(NewId())
	if err != nil {
		t.Fatal(err)
	}
	class, err := NewClass(classID, levelID, periodID, "year-1-a", "Year 1 - Class A", "", at)
	if err != nil {
		t.Fatal(err)
	}
	if err := class.Validate(); err != nil {
		t.Fatalf("Class.Validate() = %v", err)
	}
	classAudit := class.Auditable()
	if classAudit["id"] != class.ID.String() ||
		classAudit["programme_level_id"] != levelID.String() ||
		classAudit["academic_period_id"] != periodID.String() ||
		classAudit["created_at"] != MillisFromTime(at) {
		t.Fatalf("class audit = %#v", classAudit)
	}
	if _, exposed := classAudit["description"]; exposed {
		t.Fatalf("class audit exposes description: %#v", classAudit)
	}
	class.PrepareUpdate(at.Add(time.Second))
	if err := class.Validate(); err != nil {
		t.Fatalf("Class after PrepareUpdate: %v", err)
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
				ID:            AcademicPeriodID(NewId()),
				CreatedAt:     at,
				UpdatedAt:     at,
				Revision:      1,
				InstitutionID: InstitutionID(NewId()),
				Name:          "2026-2027",
				DisplayName:   "2026–2027",
				StartsAt:      time.UnixMilli(100).UTC(),
				EndsAt:        time.UnixMilli(100).UTC(),
			}).Validate(),
			code: "model.academic_period.is_valid.end_at.app_error",
		},
		{
			name: "class academic period",
			err: (&Class{
				ID:               ClassID(NewId()),
				CreatedAt:        at,
				UpdatedAt:        at,
				Revision:         1,
				ProgrammeLevelID: ProgrammeLevelID(NewId()),
				Name:             "year-1-a",
				DisplayName:      "Year 1 - Class A",
			}).Validate(),
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
