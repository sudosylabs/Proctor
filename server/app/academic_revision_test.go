// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"strings"
	"testing"
	"time"

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
						if !Is(err, code) {
							t.Fatalf("error = %v, want %s", err, code)
						}
						for _, event := range events {
							if strings.HasPrefix(event, "store-") || event == "audit-begin" {
								t.Fatalf("rejected editor reached mutation: %v", events)
							}
						}
						if len(events) == 0 || !strings.HasPrefix(events[0], "authorize") {
							t.Fatalf("authorization did not precede snapshot check: %v", events)
						}
					} else {
						if err != nil {
							t.Fatal(err)
						}
						want := int64(8)
						if operation == "archive" {
							want = 7
						}
						if got := revision(operation); got != want {
							t.Fatalf("Store revision = %d, want %d", got, want)
						}
					}
				})
			}
		}
	}
}

func academicRevisionFixture(t *testing.T, kind string, events *[]string, denied bool) (func(*int64) error, func(*int64) error, func(string) int64) {
	t.Helper()
	ctx, invocation := context.Background(), Invocation{}
	now := func() time.Time { return model.TimeFromMillis(500) }
	at := model.TimeFromMillis(100)
	var deniedErr error
	if denied {
		deniedErr = NewError("authorization.denied")
	}
	authorization := &programmeAuthorizerFake{events: events, err: deniedErr}
	audit := &institutionAuditorFake{events: events, beginID: model.NewId()}
	switch kind {
	case "academic_unit":
		current := &model.AcademicUnit{ID: model.NewAcademicUnitID(), CreatedAt: at, UpdatedAt: at, Revision: 7, Name: "academic", DisplayName: "Academic", InstitutionID: model.NewInstitutionID()}
		persistence := &academicUnitMutationStore{events: events, current: current, updated: current, archived: current}
		service := newAcademicUnitCommandService(persistence, academicUnitAuthorizerStub{authorize: func(context.Context, Invocation, model.Action, model.Resource) error {
			*events = append(*events, "authorize")
			return deniedErr
		}}, &academicUnitCommandAuditor{events: events, beginID: model.NewId()}, &academicUnitCommandEffectsFake{events: events}, &academicUnitEffectFailureReporterFake{events: events}, now, model.NewId)
		return func(expected *int64) error {
				_, err := service.Update(ctx, invocation, UpdateAcademicUnitCommand{ID: current.ID.String(), ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.Archive(ctx, invocation, ArchiveAcademicUnitCommand{ID: current.ID.String(), ExpectedRevision: expected})
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
		service := newProgrammeService(persistence, authorization, audit, now, model.NewId)
		return func(expected *int64) error {
				_, err := service.Update(ctx, invocation, UpdateProgrammeCommand{ID: current.ID.String(), ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.Archive(ctx, invocation, ArchiveProgrammeCommand{ID: current.ID.String(), ExpectedRevision: expected})
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
		service := newProgrammeLevelService(persistence, authorization, audit, now, model.NewId)
		return func(expected *int64) error {
				_, err := service.Update(ctx, invocation, UpdateProgrammeLevelCommand{ID: current.ID.String(), ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.Archive(ctx, invocation, ArchiveProgrammeLevelCommand{ID: current.ID.String(), ExpectedRevision: expected})
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
		service := newAcademicPeriodService(persistence, &academicPeriodAuthorizerFake{events: events, err: deniedErr}, audit, now, model.NewId)
		return func(expected *int64) error {
				_, err := service.Update(ctx, invocation, UpdateAcademicPeriodCommand{ID: current.ID.String(), ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.Archive(ctx, invocation, ArchiveAcademicPeriodCommand{ID: current.ID.String(), ExpectedRevision: expected})
			},
			func(operation string) int64 {
				if operation == "archive" {
					return persistence.archiveInput.ExpectedRevision
				}
				return persistence.updateInput.Period.Revision
			}
	case "class":
		current := &model.Class{ID: model.NewClassID(), CreatedAt: at, UpdatedAt: at, Revision: 7, Name: "academic", DisplayName: "Academic", ProgrammeLevelID: model.NewProgrammeLevelID(), AcademicPeriodID: model.NewAcademicPeriodID()}
		persistence := &classStoreFake{events: events, current: current, unitID: model.NewId()}
		service := newClassService(persistence, authorization, audit, now, model.NewId)
		return func(expected *int64) error {
				_, err := service.Update(ctx, invocation, UpdateClassCommand{ID: current.ID.String(), ExpectedRevision: expected})
				return err
			},
			func(expected *int64) error {
				return service.Archive(ctx, invocation, ArchiveClassCommand{ID: current.ID.String(), ExpectedRevision: expected})
			},
			func(operation string) int64 {
				if operation == "archive" {
					return persistence.archiveInput.ExpectedRevision
				}
				return persistence.updateInput.Class.Revision
			}
	default:
		t.Fatalf("unknown academic kind %s", kind)
		return nil, nil, nil
	}
}
