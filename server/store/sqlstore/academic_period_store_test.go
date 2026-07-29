// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestAcademicPeriodStore(t *testing.T) {
	StoreTest(t, storetest.TestAcademicPeriodStore)
}

func TestAcademicPeriodRowConversion(t *testing.T) {
	period := &model.AcademicPeriod{
		Id:            model.NewId(),
		CreateAt:      1,
		UpdateAt:      2,
		DeleteAt:      3,
		InstitutionId: model.NewId(),
		Name:          "2026-2027",
		DisplayName:   "Academic Year 2026-2027",
		Description:   "Primary academic year",
		StartAt:       4,
		EndAt:         5,
	}
	row := newAcademicPeriodRow(period)
	if got := row.model(); *got != *period {
		t.Fatalf("row.model() = %#v, want %#v", got, period)
	}
}
