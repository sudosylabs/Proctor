// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package model

import "testing"

type persistentModel interface {
	PreSave()
	PreUpdate()
	IsValid() *AppError
	Auditable() map[string]any
}

func TestAcademicModelsImplementLifecycleContract(t *testing.T) {
	t.Parallel()

	institutionID := NewId()
	unitID := NewId()
	programmeID := NewId()
	levelID := NewId()
	periodID := NewId()

	tests := []struct {
		name  string
		model persistentModel
	}{
		{
			name: "institution",
			model: &Institution{
				Name:        "northbridge",
				DisplayName: "Northbridge University",
				Description: "Institution",
			},
		},
		{
			name: "academic unit",
			model: &AcademicUnit{
				InstitutionId: institutionID,
				Name:          "engineering",
				DisplayName:   "College of Engineering",
			},
		},
		{
			name: "programme",
			model: &Programme{
				AcademicUnitId: unitID,
				Name:           "computer-science",
				DisplayName:    "Bachelor of Computer Science",
			},
		},
		{
			name: "programme level",
			model: &ProgrammeLevel{
				ProgrammeId: programmeID,
				Name:        "year-1",
				DisplayName: "Year 1",
			},
		},
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

func TestAcademicModelValidationReturnsPreciseTranslationIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *AppError
		code string
	}{
		{
			name: "self-parent academic unit",
			err: func() *AppError {
				id := NewId()
				return (&AcademicUnit{
					Id:            id,
					CreateAt:      1,
					UpdateAt:      1,
					InstitutionId: NewId(),
					ParentId:      id,
					Name:          "computing",
					DisplayName:   "School of Computing",
				}).IsValid()
			}(),
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
				Id:             NewId(),
				CreateAt:       1,
				UpdateAt:       1,
				AcademicUnitId: NewId(),
				Name:           "Bachelor of Computing",
				DisplayName:    "Bachelor of Computing",
			}).IsValid(),
			code: "model.programme.is_valid.name.app_error",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.err == nil {
				t.Fatal("IsValid() returned nil")
			}
			if test.err.Id != test.code {
				t.Fatalf("error id = %q, want %q", test.err.Id, test.code)
			}
			if test.err.SafeFields()["field"] == "" {
				t.Fatalf("safe fields = %#v", test.err.SafeFields())
			}
		})
	}
}

func TestPreSaveSanitizesUnsafeUnicode(t *testing.T) {
	t.Parallel()

	institution := &Institution{
		Name:        "northbridge",
		DisplayName: "North\u202Ebridge",
		Description: "safe\u2066text",
	}
	institution.PreSave()
	if institution.DisplayName != "Northbridge" || institution.Description != "safetext" {
		t.Fatalf("institution = %#v", institution)
	}
}
