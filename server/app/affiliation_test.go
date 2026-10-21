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
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"github.com/sudosylabs/proctor/server/testlib/ordertest"
)

type discardAffiliationEffects struct{}

func (discardAffiliationEffects) Changed(context.Context, model.UserID) error { return nil }
func (discardAffiliationEffects) Report(context.Context, string, error)       {}

type affiliationStoreFake struct {
	events      *[]string
	current     *model.Affiliation
	values      []*model.Affiliation
	createInput *store.AffiliationCreation
	endInput    *store.AffiliationEnd
	endErr      error
}

func (s *affiliationStoreFake) Get(context.Context, model.AffiliationID) (*model.Affiliation, error) {
	*s.events = append(*s.events, "get-affiliation")
	return s.current, nil
}
func (s *affiliationStoreFake) ListByUser(context.Context, model.UserID) ([]*model.Affiliation, error) {
	*s.events = append(*s.events, "list-affiliations")
	return s.values, nil
}
func (s *affiliationStoreFake) Create(_ context.Context, input *store.AffiliationCreation) (*model.Affiliation, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return input.Affiliation, nil
}
func (s *affiliationStoreFake) End(_ context.Context, input *store.AffiliationEnd) (*model.Affiliation, error) {
	*s.events = append(*s.events, "store-end")
	s.endInput = input
	if s.endErr != nil {
		return nil, s.endErr
	}
	ended := *s.current
	ended.EndsAt = model.OptionalTimeFromMillis(input.EndAt)
	ended.Revision = input.ExpectedRevision + 1
	return &ended, nil
}

func TestAffiliationEndFutureRangePreservesRejection(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.Affiliation{
		ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 2, UserID: model.UserID(model.NewID()), Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(1_000),
	}
	persistence := &affiliationStoreFake{events: &events, current: current, endErr: store.NewErrConflict("affiliation", "affiliation_end_time", nil)}
	service := newAffiliationService(persistence, &affiliationEnrollmentFake{events: &events}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewID()}, discardAffiliationEffects{}, func() time.Time { return time.UnixMilli(500) }, model.NewID)
	_, err := service.EndAffiliation(context.Background(), Invocation{}, EndAffiliationCommand{ID: current.ID})
	failure, ok := apperror.As(err)
	require.True(t, ok)
	require.Equal(t, "resource.not_found", failure.Code())
	require.Equal(t, "affiliation", failure.Fields()["resource"])
	require.Equal(t, int64(500), persistence.endInput.EndAt)
}

type affiliationEnrollmentFake struct {
	events *[]string
	values []*model.ClassMember
}

func (s *affiliationEnrollmentFake) ListByUser(context.Context, model.UserID) ([]*model.ClassMember, error) {
	*s.events = append(*s.events, "list-enrollments")
	return s.values, nil
}

func TestAffiliationCreateKeepsKindsNonExclusiveAndAudited(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewID()
	persistence := &affiliationStoreFake{events: &events}
	clockCalls := 0
	service := newAffiliationService(persistence, &affiliationEnrollmentFake{events: &events}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewID()}, discardAffiliationEffects{}, func() time.Time {
		clockCalls++
		return time.UnixMilli(int64(500 + clockCalls))
	}, model.NewID)
	for _, kind := range []model.AffiliationKind{model.AffiliationStudent, model.AffiliationTeacher} {
		created, err := service.CreateAffiliation(context.Background(), Invocation{}, CreateAffiliationCommand{UserID: model.UserID(userID), Kind: kind, StartAt: 100})
		require.NoError(t, err)
		require.Equal(t, kind, created.Kind)
		require.Equal(t, userID, persistence.createInput.Affiliation.UserID.String())
		require.Equal(t, model.MillisFromTime(created.CreatedAt), persistence.createInput.AuditAt, "creation/audit time")
	}
	require.Equal(t, 2, clockCalls, "clock calls")
	// Contract (guardrail 8): authorization precedes the audit, and the audit
	// begins before the durable write. Lookups may move freely.
	ordertest.RequireInOrder(t, events, "authorize", "audit-begin", "store-create", "authorize", "audit-begin", "store-create")
}

func TestAffiliationEndRejectsActiveStudentEnrollment(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.Affiliation{
		ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 3, UserID: model.UserID(model.NewID()), Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(100),
	}
	service := newAffiliationService(
		&affiliationStoreFake{events: &events, current: current},
		&affiliationEnrollmentFake{events: &events, values: []*model.ClassMember{{}}}, // open-ended EndsAt
		&programmeAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events},
		discardAffiliationEffects{},
		time.Now,
		model.NewID,
	)
	_, err := service.EndAffiliation(context.Background(), Invocation{}, EndAffiliationCommand{ID: current.ID})
	require.True(t, apperror.Is(err, "affiliation.student_has_active_enrollment"), "End() error = %v", err)
	require.NotContains(t, events, "audit-begin", "the rejected command was audited")
}

func TestAffiliationRetainedEndBypassesLaterStudentEnrollment(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.Affiliation{ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(200),
		Revision: 3, UserID: model.NewUserID(), Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(100)}
	persistence := &affiliationStoreFake{events: &events, current: current}
	service := newAffiliationService(persistence,
		&affiliationEnrollmentFake{events: &events, values: []*model.ClassMember{{ID: model.NewClassMemberID()}}},
		&programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewID()},
		discardAffiliationEffects{},
		func() time.Time { return time.UnixMilli(500) }, model.NewID)
	result, err := service.EndAffiliation(context.Background(), NewInvocation(model.Principal{UserID: model.NewUserID()}, model.RequestMetadata{}),
		EndAffiliationCommand{ID: current.ID, IdempotencyKey: "row", batchRetainedOutcome: true})
	require.NoError(t, err, "retained end")
	require.NotNil(t, result, "retained end")
	require.NotNil(t, persistence.endInput, "retained end")
	for _, event := range events {
		require.NotEqual(t, "list-enrollments", event, "retained outcome inspected later enrollment state: %v", events)
	}
}

func TestAffiliationEndCarriesExpectedRevision(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.Affiliation{
		ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 4, UserID: model.UserID(model.NewID()), Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(100),
	}
	persistence := &affiliationStoreFake{events: &events, current: current}
	service := newAffiliationService(persistence, &affiliationEnrollmentFake{events: &events}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewID()}, discardAffiliationEffects{}, func() time.Time { return time.UnixMilli(500) }, model.NewID)
	ended, err := service.EndAffiliation(context.Background(), Invocation{}, EndAffiliationCommand{ID: current.ID})
	require.NoError(t, err)
	require.Equal(t, current.Revision, persistence.endInput.ExpectedRevision, "end input/result = %#v / %#v", persistence.endInput, ended)
	require.Equal(t, current.Revision+1, ended.Revision, "end input/result = %#v / %#v", persistence.endInput, ended)
}
