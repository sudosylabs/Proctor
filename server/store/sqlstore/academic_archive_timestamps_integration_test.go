//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/store/storetest"
)

func TestAcademicArchiveTimestamps(t *testing.T) {
	persistence := openTestStore(t)
	adapters := []struct {
		name string
		new  func(*testing.T, *SQLStore) store.Store
	}{
		{name: "sql", new: func(_ *testing.T, s *SQLStore) store.Store { return s }},
		{name: "local-cache", new: newLocalCacheConformanceStore},
		{name: "retry", new: newRetryConformanceStore},
		{name: "timer", new: newTimerConformanceStore},
	}
	for _, adapter := range adapters {
		t.Run(adapter.name, func(t *testing.T) {
			resetTestStore(t, persistence)
			storetest.TestAcademicArchiveTimestamps(t, adapter.new(t, persistence), academicArchiveSQLProbe(persistence))
		})
	}
}

func academicArchiveSQLProbe(persistence *SQLStore) storetest.AcademicArchiveProbe {
	return func(ctx context.Context, resource model.Resource) (storetest.AcademicArchiveState, error) {
		table := map[model.ResourceType]string{
			model.ResourceAcademicUnit: "academic_units", model.ResourceAcademicPeriod: "academic_periods",
			model.ResourceClass: "classes", model.ResourceProgramme: "programmes", model.ResourceProgrammeLevel: "programme_levels",
		}[resource.Type]
		if table == "" {
			return storetest.AcademicArchiveState{}, fmt.Errorf("unsupported academic resource %q", resource.Type)
		}
		var row struct {
			CreatedAt  time.Time    `db:"created_at"`
			UpdatedAt  time.Time    `db:"updated_at"`
			ArchivedAt sql.NullTime `db:"archived_at"`
			Revision   int64        `db:"revision"`
		}
		query := "SELECT created_at, updated_at, archived_at, revision FROM " + table + " WHERE id = ?"
		if err := persistence.GetMaster().Get(ctx, &row, query, resource.ID); err != nil {
			return storetest.AcademicArchiveState{}, err
		}
		archivedAt := model.OptionalTime{}
		if row.ArchivedAt.Valid {
			archivedAt = model.OptionalTimeFrom(row.ArchivedAt.Time)
		}
		return storetest.AcademicArchiveState{
			CreatedAt: model.TimeUTC(row.CreatedAt), UpdatedAt: model.TimeUTC(row.UpdatedAt),
			ArchivedAt: archivedAt, Revision: row.Revision,
		}, nil
	}
}

func TestAcademicLifecycleRevisionConformance(t *testing.T) {
	persistence := openTestStore(t)
	for _, adapter := range []struct {
		name string
		new  func(*testing.T, *SQLStore) store.Store
	}{
		{"sql", func(_ *testing.T, s *SQLStore) store.Store { return s }},
		{"local-cache", newLocalCacheConformanceStore}, {"retry", newRetryConformanceStore}, {"timer", newTimerConformanceStore},
	} {
		t.Run(adapter.name, func(t *testing.T) {
			resetTestStore(t, persistence)
			storetest.TestAcademicLifecycleRevisionConformance(t, adapter.new(t, persistence))
		})
	}
}
