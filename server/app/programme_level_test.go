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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type programmeLevelStoreFake struct {
	events       *[]string
	current      *model.ProgrammeLevel
	created      *model.ProgrammeLevel
	createInput  *store.ProgrammeLevelCreation
	updateInput  *store.ProgrammeLevelUpdate
	archiveInput *store.ProgrammeLevelArchive
	createErr    error
	listed       []*model.ProgrammeLevel
	searchTerm   string
	searchLimit  int
}

func (s *programmeLevelStoreFake) Get(context.Context, model.ProgrammeLevelID) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "get-level")
	return s.current, nil
}
func (s *programmeLevelStoreFake) ListByProgramme(context.Context, string) ([]*model.ProgrammeLevel, error) {
	return s.listed, nil
}
func (s *programmeLevelStoreFake) SearchByProgramme(_ context.Context, _ model.ProgrammeID, options store.AcademicNameListOptions) ([]*model.ProgrammeLevel, error) {
	s.searchTerm, s.searchLimit = options.Term, options.Limit
	return s.listed[:min(options.Limit, len(s.listed))], nil
}
func (s *programmeLevelStoreFake) Create(_ context.Context, input *store.ProgrammeLevelCreation) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return s.created, s.createErr
}
func (s *programmeLevelStoreFake) Update(_ context.Context, input *store.ProgrammeLevelUpdate) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "store-update")
	s.updateInput = input
	return input.Level, nil
}
func (s *programmeLevelStoreFake) Archive(_ context.Context, input *store.ProgrammeLevelArchive) (*model.ProgrammeLevel, error) {
	*s.events = append(*s.events, "store-archive")
	s.archiveInput = input
	return s.current, nil
}

func TestProgrammeLevelCreatePreservesMissingProgrammeErrorContract(t *testing.T) {
	t.Parallel()
	events := []string{}
	programmeID := model.NewID()
	service := newProgrammeLevelService(
		&programmeLevelStoreFake{events: &events},
		&programmeAuthorizerFake{events: &events, err: apperror.New("resource.not_found").WithField("resource", "programme")},
		&institutionAuditorFake{events: &events}, time.Now, model.NewID,
	)

	_, err := service.CreateProgrammeLevel(context.Background(), invocation.Invocation{}, CreateProgrammeLevelCommand{ProgrammeID: model.ProgrammeID(programmeID)})
	failure, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "resource.not_found", failure.Code())
	require.Equal(t, "programme", failure.Fields()["resource"])
	require.Equal(t, []string{"authorize"}, events)
}

func TestProgrammeLevelCreatePreservesProgrammeOwnershipAndAtomicAudit(t *testing.T) {
	t.Parallel()
	events := []string{}
	unitID, programmeID, levelID, auditID := model.NewID(), model.NewID(), model.NewID(), model.NewID()
	created := &model.ProgrammeLevel{ID: model.ProgrammeLevelID(levelID), ProgrammeID: model.ProgrammeID(programmeID)}
	persistence := &programmeLevelStoreFake{events: &events, created: created}
	authorizer := &programmeAuthorizerFake{events: &events, scopeType: model.RoleScopeAcademicUnit, scopeID: unitID}
	auditor := &institutionAuditorFake{events: &events, beginID: auditID}
	service := newProgrammeLevelService(
		persistence,
		authorizer, auditor,
		func() time.Time { return time.UnixMilli(500) }, func() string { return levelID },
	)
	got, err := service.CreateProgrammeLevel(context.Background(), invocation.Invocation{}, CreateProgrammeLevelCommand{
		ProgrammeID: model.ProgrammeID(programmeID), Name: "year-1", DisplayName: "Year 1",
	})
	require.NoError(t, err, "Create()")
	require.Same(t, created, got, "Create()")
	require.NotNil(t, persistence.createInput)
	require.Equal(t, programmeID, persistence.createInput.Level.ProgrammeID.String())
	require.Equal(t, levelID, persistence.createInput.Level.ID.String())
	require.Equal(t, model.AuditEventID(auditID), persistence.createInput.AuditEventID)
	wantResource := model.Resource{Type: model.ResourceProgramme, ID: programmeID}
	require.Equal(t, model.ActionProgrammeLevelManage, authorizer.action, "authorization/audit pairing")
	require.Equal(t, wantResource, authorizer.resource, "authorization/audit pairing")
	require.Equal(t, model.ActionProgrammeLevelManage, auditor.action, "authorization/audit pairing")
	require.Equal(t, wantResource, auditor.resource, "authorization/audit pairing")
	require.Equal(t, model.RoleScopeAcademicUnit, auditor.scopeType, "authorization/audit pairing")
	require.Equal(t, unitID, auditor.scopeID, "authorization/audit pairing")
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize", "audit-begin", "store-create"}, events)
}

func TestProgrammeLevelUpdateCannotMoveProgramme(t *testing.T) {
	t.Parallel()
	events := []string{}
	programmeID := model.NewID()
	current := &model.ProgrammeLevel{
		ID:        model.ProgrammeLevelID(model.NewID()),
		CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		ProgrammeID: model.ProgrammeID(programmeID), Name: "year-1", DisplayName: "Year 1",
	}
	persistence := &programmeLevelStoreFake{events: &events, current: current}
	service := newProgrammeLevelService(
		persistence,
		&programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewID()},
		func() time.Time { return time.UnixMilli(500) }, model.NewID,
	)
	name := "foundation"
	updated, err := service.UpdateProgrammeLevel(context.Background(), invocation.Invocation{}, UpdateProgrammeLevelCommand{ID: current.ID, Name: &name})
	require.NoError(t, err)
	require.Equal(t, current.ProgrammeID, updated.ProgrammeID)
	require.Equal(t, current.ProgrammeID, persistence.updateInput.Level.ProgrammeID)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize", "get-level", "audit-begin", "store-update"}, events)
}

func TestProgrammeLevelCreateAuthorizationDenialStopsBeforeAuditAndStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	programme := &model.Programme{ID: model.ProgrammeID(model.NewID())}
	service := newProgrammeLevelService(
		&programmeLevelStoreFake{events: &events},
		&programmeAuthorizerFake{events: &events, err: apperror.New("authorization.denied")},
		&institutionAuditorFake{events: &events, beginID: model.NewID()}, time.Now, model.NewID,
	)
	_, err := service.CreateProgrammeLevel(context.Background(), invocation.Invocation{}, CreateProgrammeLevelCommand{ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1"})
	require.True(t, apperror.Is(err, "authorization.denied"), "Create() error = %v", err)
	require.Equal(t, []string{"authorize"}, events)
}

func TestProgrammeLevelCreateConflictCompletesFailedAttempt(t *testing.T) {
	t.Parallel()
	events := []string{}
	programme := &model.Programme{ID: model.ProgrammeID(model.NewID())}
	auditor := &institutionAuditorFake{events: &events, beginID: model.NewID()}
	service := newProgrammeLevelService(
		&programmeLevelStoreFake{events: &events, createErr: store.NewErrConflict("programme_level", "programme_levels_programme_id_name_key", nil)},
		&programmeAuthorizerFake{events: &events},
		auditor, time.Now, model.NewID,
	)
	_, err := service.CreateProgrammeLevel(context.Background(), invocation.Invocation{}, CreateProgrammeLevelCommand{ProgrammeID: programme.ID, Name: "year-1", DisplayName: "Year 1"})
	require.True(t, apperror.Is(err, "programme_level.conflict"), "Create() error = %v, audit code", err)
	require.Equal(t, "programme_level.conflict", auditor.failCode, "Create() error = %v, audit code", err)
	// Contract (guardrail 8): authorization precedes the audit, the audit begins
	// before the durable write, and the audit outcome follows the write.
	require.Equal(t, []string{"authorize", "audit-begin", "store-create", "audit-fail"}, events)
}

func TestProgrammeLevelListPreservesLimitWithoutSearch(t *testing.T) {
	t.Parallel()
	for _, term := range []string{"", "  \t", " programme "} {
		t.Run(term, func(t *testing.T) {
			t.Parallel()
			events := []string{}
			first := &model.ProgrammeLevel{ID: model.NewProgrammeLevelID()}
			persistence := &programmeLevelStoreFake{
				events: &events,
				listed: []*model.ProgrammeLevel{first, {ID: model.NewProgrammeLevelID()}},
			}
			service := newProgrammeLevelService(
				persistence, &programmeAuthorizerFake{events: &events},
				&institutionAuditorFake{events: &events}, time.Now, model.NewID,
			)
			got, err := service.ListProgrammeLevels(context.Background(), invocation.Invocation{}, ListProgrammeLevelsQuery{
				ProgrammeID: model.NewProgrammeID(), Query: term, NamePageQuery: NamePageQuery{Limit: 1},
			})
			require.NoError(t, err)
			require.Len(t, got.Items, 1, "List(limit=1, q=%q)", term)
			require.Same(t, first, got.Items[0], "List(limit=1, q=%q)", term)
			require.Equal(t, len(persistence.listed) > 1, got.HasMore, "List(limit=1) reports a further page")
			require.Equal(t, strings.TrimSpace(term), persistence.searchTerm, "bounded query")
			require.Equal(t, 2, persistence.searchLimit, "bounded query probes one row past the page")
		})
	}
}
