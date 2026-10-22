// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type academicUnitMemberStoreFake struct {
	pageOptions store.AcademicUnitMemberPageOptions
	pageResult  *store.AcademicUnitMemberPage
	events      *[]string
	current     *model.AcademicUnitMember
	createInput *store.AcademicUnitMemberCreation
	endInput    *store.AcademicUnitMemberEnd
}

type academicUnitMemberEffectsFake struct {
	users []string
}

func (f *academicUnitMemberEffectsFake) InvalidateAuthorization(_ context.Context, userID string) {
	f.users = append(f.users, userID)
}

func (s *academicUnitMemberStoreFake) Get(context.Context, model.AcademicUnitMemberID) (*model.AcademicUnitMember, error) {
	*s.events = append(*s.events, "get-member")
	return s.current, nil
}
func (s *academicUnitMemberStoreFake) ListByAcademicUnit(context.Context, model.AcademicUnitID, int64) ([]*model.AcademicUnitMember, error) {
	*s.events = append(*s.events, "list-members")
	return nil, nil
}
func (s *academicUnitMemberStoreFake) Create(_ context.Context, input *store.AcademicUnitMemberCreation) (*model.AcademicUnitMember, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return input.Member, nil
}
func (s *academicUnitMemberStoreFake) End(_ context.Context, input *store.AcademicUnitMemberEnd) (*model.AcademicUnitMember, error) {
	*s.events = append(*s.events, "store-end")
	s.endInput = input
	ended := *s.current
	ended.EndsAt = model.OptionalTimeFrom(input.EndAt)
	ended.Revision = input.ExpectedRevision + 1
	return &ended, nil
}

func TestAcademicUnitMemberCreateUsesAuthorizationWithoutGrantingPermission(t *testing.T) {
	t.Parallel()
	events := []string{}
	unitID, userID := model.NewID(), model.NewID()
	persistence := &academicUnitMemberStoreFake{events: &events}
	effects := &academicUnitMemberEffectsFake{}
	mail := &relationshipMailPreparerTestFake{}
	clockCalls := 0
	service := newAcademicUnitMemberService(academicUnitMemberServiceDependencies{Store: persistence, Users: relationshipUserStoreTestFake{}, Authorization: &programmeAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mail, Effects: effects, Now: func() time.Time {
		clockCalls++
		return time.UnixMilli(500)
	}, NewID: model.NewID})
	created, err := service.CreateAcademicUnitMember(context.Background(), invocation.Invocation{}, CreateAcademicUnitMemberCommand{AcademicUnitID: model.AcademicUnitID(unitID), UserID: userID, StartAt: 100})
	require.NoError(t, err)
	require.Equal(t, unitID, created.AcademicUnitID.String())
	require.Equal(t, userID, created.UserID.String())
	require.False(t, created.EndsAt.Valid)
	require.False(t, persistence.createInput.Member.EndsAt.Valid)
	require.Equal(t, 1, clockCalls, "clock calls/creation/audit time")
	require.True(t, model.TimeUTC(created.CreatedAt).Equal(persistence.createInput.AuditAt), "clock calls/creation/audit time")
	require.Equal(t, []string{userID}, effects.users)
	require.NotNil(t, persistence.createInput.Notice, "creation mail = %#v", persistence.createInput)
	require.Equal(t, int64(1), persistence.createInput.ExpectedRecipientRevision, "creation mail = %#v", persistence.createInput)
	require.Len(t, mail.requests, 1, "creation mail = %#v", persistence.createInput)
	require.Equal(t, model.MailTemplateAcademicUnitAssigned, mail.requests[0].TemplateKey, "creation mail = %#v", persistence.createInput)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize", "audit-begin", "store-create"}, events)
}

func TestAcademicUnitMemberEndCarriesRevision(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.AcademicUnitMember{
		ID: model.NewAcademicUnitMemberID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 3, AcademicUnitID: model.AcademicUnitID(model.NewID()), UserID: model.UserID(model.NewID()),
		StartsAt: model.TimeFromMillis(100),
	}
	persistence := &academicUnitMemberStoreFake{events: &events, current: current}
	effects := &academicUnitMemberEffectsFake{}
	mail := &relationshipMailPreparerTestFake{}
	service := newAcademicUnitMemberService(academicUnitMemberServiceDependencies{Store: persistence, Users: relationshipUserStoreTestFake{}, Authorization: &programmeAuthorizerFake{events: &events}, Audit: &institutionAuditorFake{events: &events, beginID: model.NewID()}, Mail: mail, Effects: effects, Now: func() time.Time { return time.UnixMilli(500) }, NewID: model.NewID})
	ended, err := service.EndAcademicUnitMember(context.Background(), invocation.Invocation{}, EndAcademicUnitMemberCommand{ID: current.ID})
	require.NoError(t, err)
	require.Equal(t, current.Revision, persistence.endInput.ExpectedRevision, "end input/result = %#v / %#v", persistence.endInput, ended)
	require.Equal(t, current.Revision+1, ended.Revision, "end input/result = %#v / %#v", persistence.endInput, ended)
	require.Equal(t, []string{current.UserID.String()}, effects.users)
	require.NotNil(t, persistence.endInput.Notice, "end mail = %#v", persistence.endInput)
	require.Equal(t, int64(1), persistence.endInput.ExpectedRecipientRevision, "end mail = %#v", persistence.endInput)
	require.Len(t, mail.requests, 1, "end mail = %#v", persistence.endInput)
	require.Equal(t, model.MailTemplateAcademicUnitAssignmentEnded, mail.requests[0].TemplateKey, "end mail = %#v", persistence.endInput)
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write.
	require.Equal(t, []string{"authorize-preflight", "get-member", "authorize", "audit-begin", "store-end"}, events)
}

func TestAcademicUnitMemberEndDenialDoesNotInspectOpaqueMemberID(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAcademicUnitMemberService(academicUnitMemberServiceDependencies{Store: &academicUnitMemberStoreFake{events: &events}, Users: relationshipUserStoreTestFake{}, Authorization: &programmeAuthorizerFake{events: &events, preflightErr: apperror.New("authorization.denied")}, Audit: &institutionAuditorFake{events: &events}, Mail: &relationshipMailPreparerTestFake{}, Effects: &academicUnitMemberEffectsFake{}, Now: time.Now, NewID: model.NewID})
	_, err := service.EndAcademicUnitMember(context.Background(), invocation.Invocation{}, EndAcademicUnitMemberCommand{ID: model.NewAcademicUnitMemberID()})
	require.True(t, apperror.Is(err, "authorization.denied"), "End() error = %v, want authorization.denied", err)
	require.Equal(t, []string{"authorize-preflight"}, events, "events")
}

func TestAcademicUnitMemberEndConcealsCrossScopeTarget(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.AcademicUnitMember{
		ID: model.NewAcademicUnitMemberID(), AcademicUnitID: model.NewAcademicUnitID(), UserID: model.NewUserID(),
		CreatedAt: model.NowUTC(), UpdatedAt: model.NowUTC(), StartsAt: model.NowUTC(), Revision: 1,
	}
	service := newAcademicUnitMemberService(academicUnitMemberServiceDependencies{Store: &academicUnitMemberStoreFake{events: &events, current: current}, Users: relationshipUserStoreTestFake{}, Authorization: &programmeAuthorizerFake{events: &events, err: apperror.New("authorization.denied")}, Audit: &institutionAuditorFake{events: &events}, Mail: &relationshipMailPreparerTestFake{}, Effects: &academicUnitMemberEffectsFake{}, Now: time.Now, NewID: model.NewID})
	_, err := service.EndAcademicUnitMember(context.Background(), invocation.Invocation{}, EndAcademicUnitMemberCommand{ID: current.ID})
	require.True(t, apperror.Is(err, "resource.not_found"), "End() error = %v, want concealed resource.not_found", err)
}

func (s *academicUnitMemberStoreFake) ListPageByAcademicUnit(_ context.Context, options store.AcademicUnitMemberPageOptions) (*store.AcademicUnitMemberPage, error) {
	*s.events = append(*s.events, "list-page")
	s.pageOptions = options
	if s.pageResult != nil {
		return s.pageResult, nil
	}
	return &store.AcademicUnitMemberPage{}, nil
}
