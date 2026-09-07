// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package storetest

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// AcademicArchiveState is the exact lifecycle retained after an academic archive.
type AcademicArchiveState struct {
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ArchivedAt model.OptionalTime
	Revision   int64
}

// AcademicArchiveProbe reads retained state that ordinary active-only Get
// operations deliberately hide. It does not add an archived read to the Store.
type AcademicArchiveProbe func(context.Context, model.Resource) (AcademicArchiveState, error)

// TestAcademicArchiveTimestamps checks ordinary and audited archive outcomes
// against retained state, including the millisecond command's precision loss.
func TestAcademicArchiveTimestamps(t *testing.T, ss store.Store, readArchived AcademicArchiveProbe) {
	createdAt := time.Date(2030, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	cases := []struct {
		name string
		at   int64
	}{
		{name: "same-millisecond", at: createdAt.UnixMilli()},
		{name: "clock-rollback", at: createdAt.Add(-time.Minute).UnixMilli()},
		{name: "later", at: createdAt.Add(time.Minute).UnixMilli()},
	}
	for _, kind := range []model.ResourceType{
		model.ResourceAcademicUnit, model.ResourceAcademicPeriod, model.ResourceClass,
		model.ResourceProgramme, model.ResourceProgrammeLevel,
	} {
		t.Run(string(kind), func(t *testing.T) {
			for _, audited := range []bool{false, true} {
				operation := "Archive"
				if audited {
					operation = "ArchiveWithAudit"
				}
				t.Run(operation, func(t *testing.T) {
					for _, tc := range cases {
						t.Run(tc.name, func(t *testing.T) {
							ctx := context.Background()
							resource, archive := academicArchiveFixture(t, ctx, ss, kind, createdAt)
							auditID := ""
							if audited {
								institution, err := ss.Institution().GetSingleton(ctx)
								requireNoError(t, err)
								action := map[model.ResourceType]model.Action{
									model.ResourceAcademicUnit:   model.ActionAcademicUnitManage,
									model.ResourceAcademicPeriod: model.ActionAcademicPeriodManage,
									model.ResourceClass:          model.ActionClassManage,
									model.ResourceProgramme:      model.ActionProgrammeManage,
									model.ResourceProgrammeLevel: model.ActionProgrammeLevelManage,
								}[kind]
								audit, err := ss.Audit().Save(ctx, &model.AuditEvent{
									Action: string(action), Resource: resource,
									ScopeType: model.RoleScopeInstitution, ScopeID: institution.ID.String(),
									Status: model.AuditStatusAttempt, NodeID: "test-node",
								})
								requireNoError(t, err)
								auditID = audit.ID.String()
							}
							archived, err := archive(tc.at, auditID)
							requireNoError(t, err)
							requireNoError(t, archived.Validate())
							actual := academicArchiveState(archived)
							at := model.TimeFromMillis(tc.at)
							if at.Before(createdAt) {
								at = createdAt
							}
							want := AcademicArchiveState{
								CreatedAt: createdAt, UpdatedAt: at,
								ArchivedAt: model.OptionalTimeFrom(at), Revision: 2,
							}
							if actual != want {
								t.Fatalf("archive lifecycle = %#v, want %#v", actual, want)
							}
							stored, err := readArchived(ctx, resource)
							requireNoError(t, err)
							if stored != actual {
								t.Fatalf("stored lifecycle = %#v, returned %#v", stored, actual)
							}
							if audited {
								completed, err := ss.Audit().Get(ctx, auditID)
								requireNoError(t, err)
								if completed.Status != model.AuditStatusSuccess {
									t.Fatalf("archive audit status = %q", completed.Status)
								}
								wantJSON, err := model.EncodeAuditData(archived.Auditable())
								requireNoError(t, err)
								var wantAudit, gotAudit map[string]any
								requireNoError(t, json.Unmarshal(wantJSON, &wantAudit))
								requireNoError(t, json.Unmarshal(completed.Result, &gotAudit))
								if !reflect.DeepEqual(gotAudit, wantAudit) {
									t.Fatalf("archive audit = %#v, returned %#v", gotAudit, wantAudit)
								}
							}
						})
					}
				})
			}
		})
	}
}

type academicArchivedModel interface {
	model.Auditable
	Validate() error
}

func academicArchiveFixture(
	t *testing.T,
	ctx context.Context,
	ss store.Store,
	kind model.ResourceType,
	createdAt time.Time,
) (model.Resource, func(int64, string) (academicArchivedModel, error)) {
	t.Helper()
	parents := saveClassFixture(t, ctx, ss)
	audit := saveProgrammeAuditAttempt(t, ctx, ss, parents.programme.AcademicUnitID.String())
	resource := model.Resource{Type: kind}
	switch kind {
	case model.ResourceAcademicUnit:
		candidate := &model.AcademicUnit{
			InstitutionID: parents.institution.ID, Name: "archive-target", DisplayName: "Archive target",
		}
		candidate.PrepareCreate(model.NewAcademicUnitID(), createdAt)
		created, err := ss.AcademicUnit().Create(ctx, &store.AcademicUnitCreation{
			Unit: candidate, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		resource.ID = created.ID.String()
		return resource, func(at int64, auditID string) (academicArchivedModel, error) {
			if auditID == "" {
				return ss.AcademicUnit().Archive(ctx, resource.ID, at)
			}
			return ss.AcademicUnit().ArchiveWithAudit(ctx, &store.AcademicUnitArchive{
				ID: resource.ID, ArchiveAt: at, AuditEventID: auditID, AuditAt: model.GetMillis(),
			})
		}
	case model.ResourceAcademicPeriod:
		candidate := &model.AcademicPeriod{
			Owner: model.NewInstitutionAcademicPeriodOwner(parents.institution.ID),
			Name:  "archive-target", DisplayName: "Archive target",
			StartsAt: createdAt, EndsAt: createdAt.AddDate(1, 0, 0),
		}
		candidate.PrepareCreate(model.NewAcademicPeriodID(), createdAt)
		created, err := ss.AcademicPeriod().Create(ctx, &store.AcademicPeriodCreation{
			Period: candidate, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		resource.ID = created.ID.String()
		return resource, func(at int64, auditID string) (academicArchivedModel, error) {
			if auditID == "" {
				return ss.AcademicPeriod().Archive(ctx, resource.ID, at)
			}
			return ss.AcademicPeriod().ArchiveWithAudit(ctx, &store.AcademicPeriodArchive{
				ID: resource.ID, ArchiveAt: at, AuditEventID: auditID, AuditAt: model.GetMillis(),
			})
		}
	case model.ResourceClass:
		candidate := &model.Class{
			ProgrammeLevelID: parents.level.ID, AcademicPeriodID: parents.period.ID,
			Name: "archive-target", DisplayName: "Archive target",
		}
		candidate.PrepareCreate(model.NewClassID(), createdAt)
		created, err := ss.Class().Create(ctx, &store.ClassCreation{
			Class: candidate, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		resource.ID = created.ID.String()
		return resource, func(at int64, auditID string) (academicArchivedModel, error) {
			if auditID == "" {
				return ss.Class().Archive(ctx, resource.ID, at)
			}
			return ss.Class().ArchiveWithAudit(ctx, &store.ClassArchive{
				ID: resource.ID, ExpectedRevision: created.Revision,
				ExpectedAcademicUnitID: parents.programme.AcademicUnitID.String(),
				ArchiveAt:              at, AuditEventID: auditID, AuditAt: model.GetMillis(),
			})
		}
	case model.ResourceProgramme:
		candidate := &model.Programme{
			AcademicUnitID: parents.programme.AcademicUnitID, Name: "archive-target", DisplayName: "Archive target",
		}
		candidate.PrepareCreate(model.NewProgrammeID(), createdAt)
		created, err := ss.Programme().Create(ctx, &store.ProgrammeCreation{
			Programme: candidate, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		resource.ID = created.ID.String()
		return resource, func(at int64, auditID string) (academicArchivedModel, error) {
			if auditID == "" {
				return ss.Programme().Archive(ctx, resource.ID, at)
			}
			return ss.Programme().ArchiveWithAudit(ctx, &store.ProgrammeArchive{
				ID: resource.ID, ArchiveAt: at, AuditEventID: auditID, AuditAt: model.GetMillis(),
			})
		}
	case model.ResourceProgrammeLevel:
		candidate := &model.ProgrammeLevel{
			ProgrammeID: parents.programme.ID, Name: "archive-target", DisplayName: "Archive target",
		}
		candidate.PrepareCreate(model.NewProgrammeLevelID(), createdAt)
		created, err := ss.ProgrammeLevel().Create(ctx, &store.ProgrammeLevelCreation{
			Level: candidate, AuditEventID: audit.ID.String(), AuditAt: model.GetMillis(),
		})
		requireNoError(t, err)
		resource.ID = created.ID.String()
		return resource, func(at int64, auditID string) (academicArchivedModel, error) {
			if auditID == "" {
				return ss.ProgrammeLevel().Archive(ctx, resource.ID, at)
			}
			return ss.ProgrammeLevel().ArchiveWithAudit(ctx, &store.ProgrammeLevelArchive{
				ID: resource.ID, ArchiveAt: at, AuditEventID: auditID, AuditAt: model.GetMillis(),
			})
		}
	default:
		t.Fatalf("unsupported academic resource %q", kind)
		return model.Resource{}, nil
	}
}

func academicArchiveState(value academicArchivedModel) AcademicArchiveState {
	switch value := value.(type) {
	case *model.AcademicUnit:
		return AcademicArchiveState{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt, Revision: value.Revision}
	case *model.AcademicPeriod:
		return AcademicArchiveState{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt, Revision: value.Revision}
	case *model.Class:
		return AcademicArchiveState{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt, Revision: value.Revision}
	case *model.Programme:
		return AcademicArchiveState{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt, Revision: value.Revision}
	case *model.ProgrammeLevel:
		return AcademicArchiveState{CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt, Revision: value.Revision}
	default:
		panic("unsupported academic archive model")
	}
}
