// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAcademicPeriodAndClassTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	institutionID, err := ParseInstitutionID(NewID())
	require.NoError(t, err)
	periodID, err := ParseAcademicPeriodID(NewID())
	require.NoError(t, err)
	period, err := NewAcademicPeriod(
		periodID, NewInstitutionAcademicPeriodOwner(institutionID), "2026-2027", "2026–2027", "",
		time.UnixMilli(1788213600000).UTC(), time.UnixMilli(1819749600000).UTC(), at,
	)
	require.NoError(t, err)
	require.NoError(t, period.Validate())
	audit := period.Auditable()
	require.Equal(t, period.ID.String(), audit["id"])
	require.Equal(t, string(ResourceInstitution), audit["owner_type"])
	require.Equal(t, institutionID.String(), audit["owner_id"])
	require.Equal(t, MillisFromTime(at), audit["created_at"])
	require.Equal(t, MillisFromTime(period.StartsAt), audit["start_at"])
	require.Equal(t, MillisFromTime(period.EndsAt), audit["end_at"])
	_, exposed := audit["description"]
	require.False(t, exposed, "academic period audit exposes description: %#v", audit)
	period.PrepareUpdate(at.Add(time.Second))
	require.NoError(t, period.Validate())

	levelID, err := ParseProgrammeLevelID(NewID())
	require.NoError(t, err)
	classID, err := ParseClassID(NewID())
	require.NoError(t, err)
	class, err := NewClass(classID, levelID, periodID, "year-1-a", "Year 1 - Class A", "", at)
	require.NoError(t, err)
	require.NoError(t, class.Validate())
	classAudit := class.Auditable()
	require.Equal(t, class.ID.String(), classAudit["id"])
	require.Equal(t, levelID.String(), classAudit["programme_level_id"])
	require.Equal(t, periodID.String(), classAudit["academic_period_id"])
	require.Equal(t, MillisFromTime(at), classAudit["created_at"])
	_, exposed = classAudit["description"]
	require.False(t, exposed, "class audit exposes description: %#v", classAudit)
	class.PrepareUpdate(at.Add(time.Second))
	require.NoError(t, class.Validate())
}

func TestAcademicPeriodRequiresExactlyOneOwner(t *testing.T) {
	t.Parallel()
	at := time.UnixMilli(1_700_000_000_000).UTC()
	base := AcademicPeriod{
		ID: NewAcademicPeriodID(), CreatedAt: at, UpdatedAt: at, Revision: 1,
		Name: "2026", DisplayName: "2026", StartsAt: at, EndsAt: at.Add(time.Hour),
	}
	for name, owner := range map[string]AcademicPeriodOwner{
		"missing": {},
		"both": {
			InstitutionID: NewInstitutionID(), AcademicUnitID: NewAcademicUnitID(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Owner = owner
			require.Error(t, candidate.Validate(), "Validate() accepted an ambiguous owner")
		})
	}
	for name, owner := range map[string]AcademicPeriodOwner{
		"institution":   NewInstitutionAcademicPeriodOwner(NewInstitutionID()),
		"academic unit": NewAcademicUnitAcademicPeriodOwner(NewAcademicUnitID()),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Owner = owner
			require.NoError(t, candidate.Validate())
		})
	}
}

func TestProgrammeAndProgrammeLevelTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	unitID, err := ParseAcademicUnitID(NewID())
	require.NoError(t, err)
	programmeID, err := ParseProgrammeID(NewID())
	require.NoError(t, err)
	programme, err := NewProgramme(
		programmeID, unitID, "computer-science", "Bachelor of Computer Science", "", at,
	)
	require.NoError(t, err)
	require.NoError(t, programme.Validate())
	audit := programme.Auditable()
	require.Equal(t, programme.ID.String(), audit["id"])
	require.Equal(t, unitID.String(), audit["academic_unit_id"])
	require.Equal(t, MillisFromTime(at), audit["created_at"])
	_, exposed := audit["description"]
	require.False(t, exposed, "programme audit exposes description: %#v", audit)
	programme.PrepareUpdate(at.Add(time.Second))
	require.NoError(t, programme.Validate())

	levelID, err := ParseProgrammeLevelID(NewID())
	require.NoError(t, err)
	level, err := NewProgrammeLevel(
		levelID, programmeID, "year-1", "Year 1", "", at,
	)
	require.NoError(t, err)
	require.NoError(t, level.Validate())
	levelAudit := level.Auditable()
	require.Equal(t, level.ID.String(), levelAudit["id"])
	require.Equal(t, programmeID.String(), levelAudit["programme_id"])
	require.Equal(t, MillisFromTime(at), levelAudit["created_at"])
	level.PrepareUpdate(at.Add(time.Second))
	require.NoError(t, level.Validate())
}

func TestInstitutionAndAcademicUnitTypedLifecycle(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	institutionID, err := ParseInstitutionID(NewID())
	require.NoError(t, err)
	institution, err := NewInstitution(
		institutionID, "northbridge", "Northbridge University", "Institution", at,
	)
	require.NoError(t, err)
	require.NoError(t, institution.Validate())
	audit := institution.Auditable()
	require.Equal(t, institution.ID.String(), audit["id"])
	require.Equal(t, MillisFromTime(at), audit["created_at"])
	require.Equal(t, MillisFromTime(at), audit["updated_at"])
	_, exposed := audit["description"]
	require.False(t, exposed, "institution audit exposes description: %#v", audit)
	institution.PrepareUpdate(at.Add(time.Second))
	require.NoError(t, institution.Validate())

	unitID, err := ParseAcademicUnitID(NewID())
	require.NoError(t, err)
	unit, err := NewAcademicUnit(
		unitID, institutionID, "", "engineering", "College of Engineering", "", at,
	)
	require.NoError(t, err)
	require.NoError(t, unit.Validate())
	unitAudit := unit.Auditable()
	require.Equal(t, unit.ID.String(), unitAudit["id"])
	require.Equal(t, institutionID.String(), unitAudit["institution_id"])
	require.Equal(t, MillisFromTime(at), unitAudit["created_at"])
	unit.PrepareUpdate(at.Add(time.Second))
	require.NoError(t, unit.Validate())
}

func TestAcademicModelValidationReturnsPreciseTranslationIDs(t *testing.T) {
	t.Parallel()

	selfParentID, err := ParseAcademicUnitID(NewID())
	require.NoError(t, err)
	institutionID, err := ParseInstitutionID(NewID())
	require.NoError(t, err)
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
				ID:          AcademicPeriodID(NewID()),
				CreatedAt:   at,
				UpdatedAt:   at,
				Revision:    1,
				Owner:       NewInstitutionAcademicPeriodOwner(InstitutionID(NewID())),
				Name:        "2026-2027",
				DisplayName: "2026–2027",
				StartsAt:    time.UnixMilli(100).UTC(),
				EndsAt:      time.UnixMilli(100).UTC(),
			}).Validate(),
			code: "model.academic_period.is_valid.end_at.app_error",
		},
		{
			name: "class academic period",
			err: (&Class{
				ID:               ClassID(NewID()),
				CreatedAt:        at,
				UpdatedAt:        at,
				Revision:         1,
				ProgrammeLevelID: ProgrammeLevelID(NewID()),
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
			require.Error(t, test.err, "validation returned nil")
			validation, ok := test.err.(*ValidationError)
			require.True(t, ok, "error type = %T", test.err)
			require.Equal(t, test.code, validation.Code, "error code")
			require.NotEmpty(t, validation.SafeFields()["field"])
		})
	}
}

func TestInstitutionSanitizesUnsafeUnicode(t *testing.T) {
	t.Parallel()

	id, err := ParseInstitutionID(NewID())
	require.NoError(t, err)
	institution, err := NewInstitution(
		id,
		"northbridge",
		"North\u202Ebridge",
		"safe\u2066text",
		time.UnixMilli(1_700_000_000_000).UTC(),
	)
	require.NoError(t, err)
	require.Equal(t, "Northbridge", institution.DisplayName)
	require.Equal(t, "safetext", institution.Description)
}
