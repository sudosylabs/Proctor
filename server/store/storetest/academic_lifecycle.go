// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"sync"
	"testing"

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
			requireNoError(t, err)
			action := map[model.ResourceType]model.Action{model.ResourceAcademicUnit: model.ActionAcademicUnitManage, model.ResourceProgramme: model.ActionProgrammeManage, model.ResourceProgrammeLevel: model.ActionProgrammeLevelManage, model.ResourceAcademicPeriod: model.ActionAcademicPeriodManage, model.ResourceClass: model.ActionClassManage}[kind]
			attempt := func() string {
				audit, err := ss.Audit().Save(ctx, &model.AuditEvent{Action: string(action), Resource: resource, ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(), Status: model.AuditStatusAttempt, NodeID: "test-node"})
				requireNoError(t, err)
				return audit.ID.String()
			}
			assertAttempt := func(id string, status model.AuditStatus) {
				audit, err := ss.Audit().Get(ctx, id)
				requireNoError(t, err)
				if audit.Status != status {
					t.Fatalf("audit status = %s, want %s", audit.Status, status)
				}
			}
			requireNoError(t, update(1, "legacy", ""))
			auditID := attempt()
			requireNoError(t, update(2, "audited", auditID))
			assertAttempt(auditID, model.AuditStatusSuccess)
			if err := update(2, "stale-legacy", ""); !store.IsConflict(err) {
				t.Fatalf("stale legacy update = %v", err)
			}
			staleID := attempt()
			if err := update(2, "stale-audited", staleID); !store.IsConflict(err) {
				t.Fatalf("stale audited update = %v", err)
			}
			assertAttempt(staleID, model.AuditStatusAttempt)
			if err := archive(2, staleID); !store.IsConflict(err) {
				t.Fatalf("stale archive = %v", err)
			}
			assertAttempt(staleID, model.AuditStatusAttempt)
			if err := archive(3, model.NewId()); err == nil {
				t.Fatal("archive committed without its pending audit")
			}
			revision, name, err := get()
			requireNoError(t, err)
			if revision != 3 || name != "audited" {
				t.Fatalf("failed mutations changed resource: %d %s", revision, name)
			}

			ids := []string{attempt(), attempt()}
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
					if !store.IsConflict(err) {
						t.Fatalf("concurrent update = %v", err)
					}
					assertAttempt(ids[i], model.AuditStatusAttempt)
				}
			}
			if successes != 1 {
				t.Fatalf("concurrent successes = %d", successes)
			}
			revision, _, err = get()
			requireNoError(t, err)
			if revision != 4 {
				t.Fatalf("concurrent revision = %d", revision)
			}
			finalID := attempt()
			if err := archive(3, finalID); !store.IsConflict(err) {
				t.Fatalf("archive overwrote winning editor: %v", err)
			}
			assertAttempt(finalID, model.AuditStatusAttempt)
			requireNoError(t, archive(4, finalID))
			assertAttempt(finalID, model.AuditStatusSuccess)
			if _, _, err := get(); !store.IsNotFound(err) {
				t.Fatalf("archived resource is active: %v", err)
			}
		})
	}
}

func academicLifecycleRevisionOperations(t *testing.T, ctx context.Context, ss store.Store, resource model.Resource) (func(int64, string, string) error, func(int64, string) error, func() (int64, string, error)) {
	t.Helper()
	switch resource.Type {
	case model.ResourceAcademicUnit:
		return func(revision int64, display, auditID string) error {
				current, err := ss.AcademicUnit().Get(ctx, resource.ID)
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = ss.AcademicUnit().Update(ctx, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.AcademicUnit().UpdateWithAudit(ctx, &store.AcademicUnitUpdate{Unit: current, AuditEventID: auditID, AuditAt: model.GetMillis()})
				return err
			}, func(revision int64, auditID string) error {
				_, err := ss.AcademicUnit().ArchiveWithAudit(ctx, &store.AcademicUnitArchive{ID: resource.ID, ExpectedRevision: revision, ArchiveAt: model.GetMillis() + 1000, AuditEventID: auditID, AuditAt: model.GetMillis()})
				return err
			}, func() (int64, string, error) {
				current, err := ss.AcademicUnit().Get(ctx, resource.ID)
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	case model.ResourceProgramme:
		return func(revision int64, display, auditID string) error {
				current, err := ss.Programme().Get(ctx, resource.ID)
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = ss.Programme().Update(ctx, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.Programme().UpdateWithAudit(ctx, &store.ProgrammeUpdate{Programme: current, AuditEventID: auditID, AuditAt: model.GetMillis()})
				return err
			}, func(revision int64, auditID string) error {
				_, err := ss.Programme().ArchiveWithAudit(ctx, &store.ProgrammeArchive{ID: resource.ID, ExpectedRevision: revision, ArchiveAt: model.GetMillis() + 1000, AuditEventID: auditID, AuditAt: model.GetMillis()})
				return err
			}, func() (int64, string, error) {
				current, err := ss.Programme().Get(ctx, resource.ID)
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	case model.ResourceProgrammeLevel:
		return func(revision int64, display, auditID string) error {
				current, err := ss.ProgrammeLevel().Get(ctx, resource.ID)
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = ss.ProgrammeLevel().Update(ctx, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.ProgrammeLevel().UpdateWithAudit(ctx, &store.ProgrammeLevelUpdate{Level: current, AuditEventID: auditID, AuditAt: model.GetMillis()})
				return err
			}, func(revision int64, auditID string) error {
				_, err := ss.ProgrammeLevel().ArchiveWithAudit(ctx, &store.ProgrammeLevelArchive{ID: resource.ID, ExpectedRevision: revision, ArchiveAt: model.GetMillis() + 1000, AuditEventID: auditID, AuditAt: model.GetMillis()})
				return err
			}, func() (int64, string, error) {
				current, err := ss.ProgrammeLevel().Get(ctx, resource.ID)
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	case model.ResourceAcademicPeriod:
		return func(revision int64, display, auditID string) error {
				current, err := ss.AcademicPeriod().Get(ctx, resource.ID)
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = ss.AcademicPeriod().Update(ctx, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.AcademicPeriod().UpdateWithAudit(ctx, &store.AcademicPeriodUpdate{Period: current, AuditEventID: auditID, AuditAt: model.GetMillis()})
				return err
			}, func(revision int64, auditID string) error {
				_, err := ss.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{ID: resource.ID, ExpectedRevision: revision, ArchiveAt: model.GetMillis() + 1000, AuditEventID: auditID, AuditAt: model.GetMillis()})
				return err
			}, func() (int64, string, error) {
				current, err := ss.AcademicPeriod().Get(ctx, resource.ID)
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	case model.ResourceClass:
		unitID, err := ss.Class().GetAcademicUnitId(ctx, resource.ID)
		requireNoError(t, err)
		return func(revision int64, display, auditID string) error {
				current, err := ss.Class().Get(ctx, resource.ID)
				if err != nil {
					return err
				}
				current.Revision, current.DisplayName = revision, display
				if auditID == "" {
					_, err = ss.Class().Update(ctx, current)
					return err
				}
				current.PrepareUpdate(model.NowUTC())
				_, err = ss.Class().UpdateWithAudit(ctx, &store.ClassUpdate{Class: current, AuditEventID: auditID, AuditAt: model.GetMillis(), ExpectedRevision: revision, ExpectedAcademicUnitID: unitID})
				return err
			}, func(revision int64, auditID string) error {
				_, err := ss.Class().ArchiveWithAudit(ctx, &store.ClassArchive{ID: resource.ID, ExpectedRevision: revision, ArchiveAt: model.GetMillis() + 1000, AuditEventID: auditID, AuditAt: model.GetMillis(), ExpectedAcademicUnitID: unitID})
				return err
			}, func() (int64, string, error) {
				current, err := ss.Class().Get(ctx, resource.ID)
				if err != nil {
					return 0, "", err
				}
				return current.Revision, current.DisplayName, nil
			}
	default:
		t.Fatalf("unsupported resource %s", resource.Type)
		return nil, nil, nil
	}
}
