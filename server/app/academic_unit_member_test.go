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

type academicUnitMemberStoreFake struct {
	events      *[]string
	current     *model.AcademicUnitMember
	createInput *store.AcademicUnitMemberCreation
	endInput    *store.AcademicUnitMemberEnd
}

func (s *academicUnitMemberStoreFake) Get(context.Context, string) (*model.AcademicUnitMember, error) {
	*s.events = append(*s.events, "get-member")
	return s.current, nil
}
func (s *academicUnitMemberStoreFake) ListByAcademicUnit(context.Context, string, int64) ([]*model.AcademicUnitMember, error) {
	*s.events = append(*s.events, "list-members")
	return nil, nil
}
func (s *academicUnitMemberStoreFake) Create(_ context.Context, input *store.AcademicUnitMemberCreation) (*model.AcademicUnitMember, error) {
	*s.events = append(*s.events, "store-create")
	s.createInput = input
	return input.Member, nil
}
func (s *academicUnitMemberStoreFake) EndWithAudit(_ context.Context, input *store.AcademicUnitMemberEnd) (*model.AcademicUnitMember, error) {
	*s.events = append(*s.events, "store-end")
	s.endInput = input
	ended := *s.current
	ended.EndsAt = model.OptionalTimeFromMillis(input.EndAt)
	ended.Revision = input.ExpectedRevision + 1
	return &ended, nil
}

func TestAcademicUnitMemberCreateUsesAuthorizationWithoutGrantingPermission(t *testing.T) {
	t.Parallel()
	events := []string{}
	unitID, userID := model.NewId(), model.NewId()
	persistence := &academicUnitMemberStoreFake{events: &events}
	service := newAcademicUnitMemberService(persistence, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	created, err := service.Create(context.Background(), Invocation{}, CreateAcademicUnitMemberCommand{AcademicUnitID: unitID, UserID: userID, StartAt: 100})
	if err != nil {
		t.Fatal(err)
	}
	if created.AcademicUnitID.String() != unitID || created.UserID.String() != userID || created.EndsAt.Valid || persistence.createInput.Member.EndsAt.Valid {
		t.Fatalf("created = %#v", created)
	}
	want := []string{"authorize", "audit-begin", "store-create"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAcademicUnitMemberEndCarriesRevision(t *testing.T) {
	t.Parallel()
	events := []string{}
	current := &model.AcademicUnitMember{
		ID: model.NewAcademicUnitMemberID(), CreatedAt: model.TimeFromMillis(100), UpdatedAt: model.TimeFromMillis(100),
		Revision: 3, AcademicUnitID: model.AcademicUnitID(model.NewId()), UserID: model.UserID(model.NewId()),
		StartsAt: model.TimeFromMillis(100),
	}
	persistence := &academicUnitMemberStoreFake{events: &events, current: current}
	service := newAcademicUnitMemberService(persistence, &programmeAuthorizerFake{events: &events}, &institutionAuditorFake{events: &events, beginID: model.NewId()}, func() time.Time { return time.UnixMilli(500) }, model.NewId)
	ended, err := service.End(context.Background(), Invocation{}, EndAcademicUnitMemberCommand{ID: current.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if persistence.endInput.ExpectedRevision != current.Revision || ended.Revision != current.Revision+1 {
		t.Fatalf("end input/result = %#v / %#v", persistence.endInput, ended)
	}
}
