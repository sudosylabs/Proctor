// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

func TestAcademicClientRevisionPreconditions(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"academic_unit", "programme", "programme_level", "academic_period", "class"} {
		for _, operation := range []string{"update", "archive"} {
			for _, scenario := range []string{"omitted", "current", "stale", "zero", "denied"} {
				t.Run(kind+"/"+operation+"/"+scenario, func(t *testing.T) {
					events := []string{}
					denied := scenario == "denied"
					update, archive, revision := academicRevisionFixture(t, kind, &events, denied)
					var expected *int64
					value := int64(7)
					if scenario != "omitted" {
						expected = &value
					}
					if scenario == "stale" || denied {
						value = 6
					}
					if scenario == "zero" {
						value = 0
					}
					call := update
					if operation == "archive" {
						call = archive
					}
					err := call(expected)
					code := ""
					switch scenario {
					case "stale":
						code = kind + ".conflict"
					case "zero":
						code = "request.invalid"
					case "denied":
						code = "authorization.denied"
					}
					if code != "" {
						require.True(t, apperror.Is(err, code), "error = %v, want %s", err, code)
						for _, event := range events {
							require.False(t, strings.HasPrefix(event, "store-"))
							require.NotEqual(t, "audit-begin", event)
						}
						require.NotEmpty(t, events)
						require.True(t, strings.HasPrefix(events[0], "authorize"))
					} else {
						require.NoError(t, err)
						want := int64(8)
						if operation == "archive" {
							want = 7
						}
						require.Equal(t, want, revision(operation), "Store revision")
					}
				})
			}
		}
	}
}

func academicRevisionFixture(t *testing.T, kind string, events *[]string, denied bool) (func(*int64) error, func(*int64) error, func(string) int64) {
	t.Helper()
	ctx, call := context.Background(), invocation.Invocation{}
	now := func() time.Time { return model.TimeFromMillis(500) }
	at := model.TimeFromMillis(100)
	var deniedErr error
	if denied {
		deniedErr = apperror.New("authorization.denied")
	}
	authorization := &programmeAuthorizerFake{events: events, err: deniedErr}
	audit := &institutionAuditorFake{events: events, beginID: model.NewID()}
	switch kind {
	case "academic_unit":
		current := &model.AcademicUnit{ID: model.NewAcademicUnitID(), CreatedAt: at, UpdatedAt: at, Revision: 7, Name: "academic", DisplayName: "Academic", InstitutionID: model.NewInstitutionID()}
		persistence := &academicUnitMutationStore{events: events, current: current, updated: current, archived: current}
		service := newAcademicUnitCommandService(persistence, academicUnitAuthorizerStub{authorize: func(context.Context, invocation.Invocation, model.Action, model.Resource) error {
			*events = append(*events, "authorize")
			return deniedErr
		}}, &academicUnitCommandAuditor{events: events, beginID: model.NewID()}, &academicUnitCommandEffectsFake{events: events}, &academicUnitEffectFailureReporterFake{events: events}, now, model.NewID)
		return func(expected *int64) error {
				_, err := service.UpdateAcademicUnit(ctx, call, UpdateAcademicUnitCommand{ID: current.ID, ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.ArchiveAcademicUnit(ctx, call, ArchiveAcademicUnitCommand{ID: current.ID, ExpectedRevision: expected})
			},
			func(operation string) int64 {
				if operation == "archive" {
					return persistence.archiveInput.ExpectedRevision
				}
				return persistence.updateInput.Unit.Revision
			}
	case "programme":
		current := &model.Programme{ID: model.NewProgrammeID(), CreatedAt: at, UpdatedAt: at, Revision: 7, Name: "academic", DisplayName: "Academic", AcademicUnitID: model.NewAcademicUnitID()}
		persistence := &programmeStoreFake{events: events, current: current}
		service := newProgrammeService(persistence, authorization, audit, now, model.NewID)
		return func(expected *int64) error {
				_, err := service.UpdateProgramme(ctx, call, UpdateProgrammeCommand{ID: current.ID, ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.ArchiveProgramme(ctx, call, ArchiveProgrammeCommand{ID: current.ID, ExpectedRevision: expected})
			},
			func(operation string) int64 {
				if operation == "archive" {
					return persistence.archiveInput.ExpectedRevision
				}
				return persistence.updateInput.Programme.Revision
			}
	case "programme_level":
		current := &model.ProgrammeLevel{ID: model.NewProgrammeLevelID(), CreatedAt: at, UpdatedAt: at, Revision: 7, Name: "academic", DisplayName: "Academic", ProgrammeID: model.NewProgrammeID()}
		persistence := &programmeLevelStoreFake{events: events, current: current}
		service := newProgrammeLevelService(persistence, authorization, audit, now, model.NewID)
		return func(expected *int64) error {
				_, err := service.UpdateProgrammeLevel(ctx, call, UpdateProgrammeLevelCommand{ID: current.ID, ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.ArchiveProgrammeLevel(ctx, call, ArchiveProgrammeLevelCommand{ID: current.ID, ExpectedRevision: expected})
			},
			func(operation string) int64 {
				if operation == "archive" {
					return persistence.archiveInput.ExpectedRevision
				}
				return persistence.updateInput.Level.Revision
			}
	case "academic_period":
		current := &model.AcademicPeriod{ID: model.NewAcademicPeriodID(), CreatedAt: at, UpdatedAt: at, Revision: 7, Name: "academic", DisplayName: "Academic", Owner: model.NewInstitutionAcademicPeriodOwner(model.NewInstitutionID()), StartsAt: at, EndsAt: at.Add(time.Hour)}
		persistence := &academicPeriodStoreFake{events: events, current: current}
		service := newAcademicPeriodService(persistence, &academicPeriodAuthorizerFake{events: events, err: deniedErr}, audit, now, model.NewID)
		return func(expected *int64) error {
				_, err := service.UpdateAcademicPeriod(ctx, call, UpdateAcademicPeriodCommand{ID: current.ID, ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.ArchiveAcademicPeriod(ctx, call, ArchiveAcademicPeriodCommand{ID: current.ID, ExpectedRevision: expected})
			},
			func(operation string) int64 {
				if operation == "archive" {
					return persistence.archiveInput.ExpectedRevision
				}
				return persistence.updateInput.Period.Revision
			}
	case "class":
		current := &model.Class{ID: model.NewClassID(), CreatedAt: at, UpdatedAt: at, Revision: 7, Name: "academic", DisplayName: "Academic", ProgrammeLevelID: model.NewProgrammeLevelID(), AcademicPeriodID: model.NewAcademicPeriodID()}
		persistence := &classStoreFake{events: events, current: current, unitID: model.NewID()}
		service := newClassService(persistence, authorization, audit, now, model.NewID)
		return func(expected *int64) error {
				_, err := service.UpdateClass(ctx, call, UpdateClassCommand{ID: current.ID, ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.ArchiveClass(ctx, call, ArchiveClassCommand{ID: current.ID, ExpectedRevision: expected})
			},
			func(operation string) int64 {
				if operation == "archive" {
					return persistence.archiveInput.ExpectedRevision
				}
				return persistence.updateInput.Class.Revision
			}
	default:
		require.FailNow(t, fmt.Sprintf("unknown academic kind %s", kind))
		return nil, nil, nil
	}
}
