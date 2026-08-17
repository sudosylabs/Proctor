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

type classMemberStoreFake struct {
	events      *[]string
	current     *model.ClassMember
	enrollInput *store.ClassMemberEnrollment
	endInput    *store.ClassMemberEnd
}

func (s *classMemberStoreFake) Get(context.Context, string) (*model.ClassMember, error) {
	*s.events = append(*s.events, "get-member")
	return s.current, nil
}
func (s *classMemberStoreFake) ListByClass(context.Context, string, int64) ([]*model.ClassMember, error) {
	*s.events = append(*s.events, "list-members")
	return nil, nil
}
func (s *classMemberStoreFake) EnrollWithAudit(_ context.Context, input *store.ClassMemberEnrollment) (*store.ClassEnrollmentResult, error) {
	*s.events = append(*s.events, "store-enroll")
	s.enrollInput = input
	return &store.ClassEnrollmentResult{Membership: input.Member}, nil
}
func (s *classMemberStoreFake) EndWithAudit(_ context.Context, input *store.ClassMemberEnd) (*model.ClassMember, error) {
	*s.events = append(*s.events, "store-end")
	s.endInput = input
	ended := *s.current
	ended.EndsAt = model.OptionalTimeFromMillis(input.EndAt)
	ended.Revision = input.ExpectedRevision + 1
	return &ended, nil
}

type classMemberClassStoreFake struct {
	events *[]string
	value  *model.Class
}

func (s *classMemberClassStoreFake) Get(context.Context, string) (*model.Class, error) {
	*s.events = append(*s.events, "get-class")
	return s.value, nil
}

func TestClassMemberEnrollUsesDestinationPeriodAndAtomicStore(t *testing.T) {
	t.Parallel()
	events := []string{}
	classID, periodID, userID := model.NewId(), model.NewId(), model.NewId()
	persistence := &classMemberStoreFake{events: &events}
	classes := &classMemberClassStoreFake{events: &events, value: &model.Class{ID: model.ClassID(classID), AcademicPeriodID: model.AcademicPeriodID(periodID)}}
	clockCalls := 0
	service := newClassMemberService(persistence, classes, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time {
		clockCalls++
		return time.UnixMilli(500)
	}, model.NewId)
	enrollment, err := service.Enroll(context.Background(), Invocation{}, EnrollClassMemberCommand{ClassID: classID, UserID: userID, StartAt: 100})
	if err != nil {
		t.Fatal(err)
	}
	if enrollment.Membership.AcademicPeriodID.String() != periodID || persistence.enrollInput.Member.UserID.String() != userID {
		t.Fatalf("enrollment/input = %#v / %#v", enrollment, persistence.enrollInput)
	}
	if clockCalls != 1 || persistence.enrollInput.AuditAt != model.MillisFromTime(enrollment.Membership.CreatedAt) {
		t.Fatalf("clock calls/creation/audit time = %d/%v/%d", clockCalls, enrollment.Membership.CreatedAt, persistence.enrollInput.AuditAt)
	}
	want := []string{"authorize", "get-class", "audit-begin", "store-enroll"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestClassMemberEndCarriesRevision(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.ClassMember{
		ID: model.NewClassMemberID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 4, ClassID: model.ClassID(model.NewId()), AcademicPeriodID: model.AcademicPeriodID(model.NewId()),
		UserID: model.UserID(model.NewId()), StartsAt: model.TimeFromMillis(100),
	}
	persistence := &classMemberStoreFake{events: &events, current: current}
	service := newClassMemberService(persistence, &classMemberClassStoreFake{events: &events}, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	ended, err := service.End(context.Background(), Invocation{}, EndClassMemberCommand{ID: current.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.endInput.ExpectedRevision != 4 || ended.Revision != 5 {
		t.Fatalf("end input/result = %#v / %#v", persistence.endInput, ended)
	}
	want := []string{"authorize-preflight", "get-member", "authorize", "audit-begin", "store-end"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestClassMemberEndDenialDoesNotInspectOpaqueMemberID(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newClassMemberService(
		&classMemberStoreFake{events: &events}, &classMemberClassStoreFake{events: &events},
		&programmeAuthorizerFake{events: &events, preflightErr: NewError("authorization.denied")},
		&institutionAuditorFake{events: &events}, time.Now, model.NewId,
	)
	if _, err := service.End(context.Background(), Invocation{}, EndClassMemberCommand{ID: model.NewId()}); !Is(err, "authorization.denied") {
		t.Fatalf("End() error = %v, want authorization.denied", err)
	}
	if want := []string{"authorize-preflight"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestClassMemberEndConcealsCrossScopeTarget(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.ClassMember{
		ID: model.NewClassMemberID(), ClassID: model.NewClassID(), AcademicPeriodID: model.NewAcademicPeriodID(), UserID: model.NewUserID(),
		CreatedAt: model.NowUTC(), UpdatedAt: model.NowUTC(), StartsAt: model.NowUTC(), Revision: 1,
	}
	service := newClassMemberService(
		&classMemberStoreFake{events: &events, current: current}, &classMemberClassStoreFake{events: &events},
		&programmeAuthorizerFake{events: &events, err: NewError("authorization.denied")},
		&institutionAuditorFake{events: &events}, time.Now, model.NewId,
	)
	if _, err := service.End(context.Background(), Invocation{}, EndClassMemberCommand{ID: current.ID.String()}); !Is(err, "resource.not_found") {
		t.Fatalf("End() error = %v, want concealed resource.not_found", err)
	}
}
