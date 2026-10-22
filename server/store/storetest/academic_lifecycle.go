// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// TestAcademicLifecycleRevisionConformance exercises legacy and audited writes
// against the same revision fence, including concurrent editors and rollback.
func TestAcademicLifecycleRevisionConformance(t *testing.T, ss store.Store) {
	for _, kind := range []model.ResourceType{model.ResourceAcademicUnit, model.ResourceProgramme, model.ResourceProgrammeLevel, model.ResourceAcademicPeriod, model.ResourceClass} {
		t.Run(string(kind), func(t *testing.T) {
			ctx := context.Background()
			resource, _ := academicArchiveFixture(t, ctx, ss, kind, model.NowUTC())
			update, archive, get := academicLifecycleRevisionOperations(t, ctx, ss, resource)
			institution, err := ss.Institution().GetSingleton(ctx)
			require.NoError(t, err)
			action := map[model.ResourceType]model.Action{model.ResourceAcademicUnit: model.ActionAcademicUnitManage, model.ResourceProgramme: model.ActionProgrammeManage, model.ResourceProgrammeLevel: model.ActionProgrammeLevelManage, model.ResourceAcademicPeriod: model.ActionAcademicPeriodManage, model.ResourceClass: model.ActionClassManage}[kind]
			attempt := func() model.AuditEventID {
				audit, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(action), Resource: resource, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
				require.NoError(t, err)
				return audit.ID
			}
			assertAttempt := func(id model.AuditEventID, status model.AuditStatus) {
				audit, err := ss.Audit().Get(ctx, id)
				require.NoError(t, err)
				require.Equal(t, status, audit.Status, "audit status")
			}
			require.NoError(t, update(1, "legacy", ""))
			auditID := attempt()
			require.NoError(t, update(2, "audited", auditID))
			assertAttempt(auditID, model.AuditStatusSuccess)
			require.True(t, store.IsConflict(update(2, "stale-legacy", "")), "stale legacy update")
			staleID := attempt()
			require.True(t, store.IsConflict(update(2, "stale-audited", staleID)), "stale audited update")
			assertAttempt(staleID, model.AuditStatusAttempt)
			require.True(t, store.IsConflict(archive(2, staleID)), "stale archive")
			assertAttempt(staleID, model.AuditStatusAttempt)
			err = archive(3, model.NewAuditEventID())
			require.Error(t, err, "archive committed without its pending audit")
			revision, name, err := get()
			require.NoError(t, err)
			require.Equal(t, int64(3), revision, "failed mutations changed resource")
			require.Equal(t, "audited", name, "failed mutations changed resource")

			ids := []model.AuditEventID{attempt(), attempt()}
			results := make([]error, 2)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for i := range results {
				workers.Add(1)
				go func(i int) { defer workers.Done(); <-start; results[i] = update(3, "concurrent", ids[i]) }(i)
			}
			close(start)
			workers.Wait()
			successes := 0
			for i, err := range results {
				if err == nil {
					successes++
					assertAttempt(ids[i], model.AuditStatusSuccess)
				} else {
					require.True(t, store.IsConflict(err), "concurrent update")
					assertAttempt(ids[i], model.AuditStatusAttempt)
				}
			}
			require.Equal(t, 1, successes, "concurrent successes")
			revision, _, err = get()
			require.NoError(t, err)
			require.Equal(t, int64(4), revision, "concurrent revision")
			finalID := attempt()
			require.True(t, store.IsConflict(archive(3, finalID)), "archive overwrote winning editor")
			assertAttempt(finalID, model.AuditStatusAttempt)
			require.NoError(t, archive(4, finalID))
			assertAttempt(finalID, model.AuditStatusSuccess)
			_, _, err = get()
			require.True(t, store.IsNotFound(err), "archived resource is active")
		})
	}
}

func academicLifecycleRevisionOperations(t *testing.T, ctx context.Context, ss store.Store, resource model.Resource) (func(int64, string, model.AuditEventID) error, func(int64, model.AuditEventID) error, func() (int64, string, error)) {
	t.Helper()
	switch resource.Type {
	case model.ResourceAcademicUnit:
		return func(revision int64, display string, auditID model.AuditEventID) error {
				current, err := ss.AcademicUnit().Get(ctx, model.AcademicUnitID(resource.ID))
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = UpdateAcademicUnit(ctx, ss, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.AcademicUnit().Update(ctx, &store.AcademicUnitUpdate{Unit: current, AuditEventID: auditID, AuditAt: model.NowUTC()})
				return err
			}, func(revision int64, auditID model.AuditEventID) error {
				_, err := ss.AcademicUnit().Archive(ctx, &store.AcademicUnitArchive{ID: model.AcademicUnitID(resource.ID), ExpectedRevision: revision, ArchiveAt: model.NowUTC().Add(1000 * time.Millisecond), AuditEventID: auditID, AuditAt: model.NowUTC()})
				return err
			}, func() (int64, string, error) {
				current, err := ss.AcademicUnit().Get(ctx, model.AcademicUnitID(resource.ID))
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	case model.ResourceProgramme:
		return func(revision int64, display string, auditID model.AuditEventID) error {
				current, err := ss.Programme().Get(ctx, model.ProgrammeID(resource.ID))
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = UpdateProgramme(ctx, ss, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.Programme().Update(ctx, &store.ProgrammeUpdate{Programme: current, AuditEventID: auditID, AuditAt: model.NowUTC()})
				return err
			}, func(revision int64, auditID model.AuditEventID) error {
				_, err := ss.Programme().Archive(ctx, &store.ProgrammeArchive{ID: model.ProgrammeID(resource.ID), ExpectedRevision: revision, ArchiveAt: model.NowUTC().Add(1000 * time.Millisecond), AuditEventID: auditID, AuditAt: model.NowUTC()})
				return err
			}, func() (int64, string, error) {
				current, err := ss.Programme().Get(ctx, model.ProgrammeID(resource.ID))
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	case model.ResourceProgrammeLevel:
		return func(revision int64, display string, auditID model.AuditEventID) error {
				current, err := ss.ProgrammeLevel().Get(ctx, model.ProgrammeLevelID(resource.ID))
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = UpdateProgrammeLevel(ctx, ss, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.ProgrammeLevel().Update(ctx, &store.ProgrammeLevelUpdate{Level: current, AuditEventID: auditID, AuditAt: model.NowUTC()})
				return err
			}, func(revision int64, auditID model.AuditEventID) error {
				_, err := ss.ProgrammeLevel().Archive(ctx, &store.ProgrammeLevelArchive{ID: model.ProgrammeLevelID(resource.ID), ExpectedRevision: revision, ArchiveAt: model.NowUTC().Add(1000 * time.Millisecond), AuditEventID: auditID, AuditAt: model.NowUTC()})
				return err
			}, func() (int64, string, error) {
				current, err := ss.ProgrammeLevel().Get(ctx, model.ProgrammeLevelID(resource.ID))
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	case model.ResourceAcademicPeriod:
		return func(revision int64, display string, auditID model.AuditEventID) error {
				current, err := ss.AcademicPeriod().Get(ctx, model.AcademicPeriodID(resource.ID))
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = UpdateAcademicPeriod(ctx, ss, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.AcademicPeriod().Update(ctx, &store.AcademicPeriodUpdate{Period: current, AuditEventID: auditID, AuditAt: model.NowUTC()})
				return err
			}, func(revision int64, auditID model.AuditEventID) error {
				_, err := ss.AcademicPeriod().Archive(ctx, &store.AcademicPeriodArchive{ID: model.AcademicPeriodID(resource.ID), ExpectedRevision: revision, ArchiveAt: model.NowUTC().Add(1000 * time.Millisecond), AuditEventID: auditID, AuditAt: model.NowUTC()})
				return err
			}, func() (int64, string, error) {
				current, err := ss.AcademicPeriod().Get(ctx, model.AcademicPeriodID(resource.ID))
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	case model.ResourceClass:
		unitID, err := ss.Class().GetAcademicUnitID(ctx, model.ClassID(resource.ID))
		require.NoError(t, err)
		return func(revision int64, display string, auditID model.AuditEventID) error {
				current, err := ss.Class().Get(ctx, model.ClassID(resource.ID))
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = UpdateClass(ctx, ss, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.Class().Update(ctx, &store.ClassUpdate{Class: current, AuditEventID: auditID, AuditAt: model.NowUTC(), ExpectedRevision: revision, ExpectedAcademicUnitID: unitID})
				return err
			}, func(revision int64, auditID model.AuditEventID) error {
				_, err := ss.Class().Archive(ctx, &store.ClassArchive{ID: model.ClassID(resource.ID), ExpectedRevision: revision, ArchiveAt: model.NowUTC().Add(1000 * time.Millisecond), AuditEventID: auditID, AuditAt: model.NowUTC(), ExpectedAcademicUnitID: unitID})
				return err
			}, func() (int64, string, error) {
				current, err := ss.Class().Get(ctx, model.ClassID(resource.ID))
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	default:
		require.FailNow(t, fmt.Sprintf("unsupported resource %s", resource.Type))
		return nil, nil, nil
	}
}
