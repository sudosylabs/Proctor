// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type affiliationStoreFake struct {
	events      *[]string
	current     *model.Affiliation
	values      []*model.Affiliation
	createInput *store.AffiliationCreation
	endInput    *store.AffiliationEnd
	endErr      error
}

func (s *affiliationStoreFake) Get(context.Context, string) (*model.Affiliation, error) {
	*s.events = append(*s.events, "get-affiliation")
	return s.current, nil
}
func (s *affiliationStoreFake) ListByUser(context.Context, string) ([]*model.Affiliation, error) {
	*s.events = append(*s.events, "list-affiliations")
	return s.values, nil
}
func (s *affiliationStoreFake) Create(_ context.Context, input *store.AffiliationCreation) (*model.Affiliation, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return input.Affiliation, nil
}
func (s *affiliationStoreFake) EndWithAudit(_ context.Context, input *store.AffiliationEnd) (*model.Affiliation, error) {
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
		Revision: 2, UserID: model.UserID(model.NewId()), Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(1_000),
	}
	persistence := &affiliationStoreFake{events: &events, current: current, endErr: store.NewErrConflict("affiliation", "affiliation_end_time", nil)}
	service := newAffiliationService(persistence, &affiliationEnrollmentFake{events: &events}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	_, err := service.End(context.Background(), Invocation{}, EndAffiliationCommand{ID: current.ID.String()})
	failure, ok := As(err)
	if !ok || failure.Code() != "resource.not_found" || failure.Fields()["resource"] != "affiliation" {
		t.Fatalf("End() error = %#v", err)
	}
	if persistence.endInput.EndAt != 500 {
		t.Fatalf("end time was coerced: %#v", persistence.endInput)
	}
}

type affiliationEnrollmentFake struct {
	events *[]string
	values []*model.ClassMember
}

func (s *affiliationEnrollmentFake) ListByUser(context.Context, string) ([]*model.ClassMember, error) {
	*s.events = append(*s.events, "list-enrollments")
	return s.values, nil
}

func TestAffiliationCreateKeepsKindsNonExclusiveAndAudited(t *testing.T) {
	t.Parallel()
	events := []string{}
	userID := model.NewId()
	persistence := &affiliationStoreFake{events: &events}
	clockCalls := 0
	service := newAffiliationService(persistence, &affiliationEnrollmentFake{events: &events}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time {
		clockCalls++
		return time.UnixMilli(int64(500 + clockCalls))
	}, model.NewId)
	for _, kind := range []model.AffiliationKind{model.AffiliationStudent, model.AffiliationTeacher} {
		created, err := service.Create(context.Background(), Invocation{}, CreateAffiliationCommand{UserID: userID, Kind: kind, StartAt: 100})
		if err != nil {
			t.Fatal(err)
		}
		if created.Kind != kind || persistence.createInput.Affiliation.UserID.String() != userID {
			t.Fatalf("created = %#v", created)
		}
		if persistence.createInput.AuditAt != model.MillisFromTime(created.CreatedAt) {
			t.Fatalf("creation/audit time = %v/%d", created.CreatedAt, persistence.createInput.AuditAt)
		}
	}
	if clockCalls != 2 {
		t.Fatalf("clock calls = %d, want one per creation", clockCalls)
	}
	want := []string{"authorize", "audit-begin", "store-create", "authorize", "audit-begin", "store-create"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAffiliationEndRejectsActiveStudentEnrollment(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.Affiliation{
		ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 3, UserID: model.UserID(model.NewId()), Kind: model.AffiliationStudent, StartsAt: model.TimeFromMillis(100),
	}
	service := newAffiliationService(
		&affiliationStoreFake{events: &events, current: current},
		&affiliationEnrollmentFake{events: &events, values: []*model.ClassMember{{}}}, // open-ended EndsAt
		&programmeAuthorizerFake{events: &events},
		&institutionAuditorFake{events: &events},
		time.Now,
		model.NewId,
	)
	_, err := service.End(context.Background(), Invocation{}, EndAffiliationCommand{ID: current.ID.String()})
	if !Is(err, "affiliation.student_has_active_enrollment") {
		t.Fatalf("End() error = %v", err)
	}
	want := []string{"get-affiliation", "authorize", "list-enrollments"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAffiliationEndCarriesExpectedRevision(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.Affiliation{
		ID: model.NewAffiliationID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 4, UserID: model.UserID(model.NewId()), Kind: model.AffiliationTeacher, StartsAt: model.TimeFromMillis(100),
	}
	persistence := &affiliationStoreFake{events: &events, current: current}
	service := newAffiliationService(persistence, &affiliationEnrollmentFake{events: &events}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	ended, err := service.End(context.Background(), Invocation{}, EndAffiliationCommand{ID: current.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.endInput.ExpectedRevision != current.Revision || ended.Revision != current.Revision+1 {
		t.Fatalf("end input/result = %#v / %#v", persistence.endInput, ended)
	}
}
