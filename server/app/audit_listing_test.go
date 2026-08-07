// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type auditListingStoreFake struct {
	events *[]string
	list   []*model.AuditEvent
	input  store.AuditListOptions
	err    error
}

func (s *auditListingStoreFake) List(_ context.Context, options store.AuditListOptions) ([]*model.AuditEvent, error) {
	*s.events = append(*s.events, "list-audits")
	s.input = options
	return s.list, s.err
}

type auditListingAuthorizerFake struct {
	events *[]string
	err    error
}

func (a *auditListingAuthorizerFake) AuthorizeView(context.Context, Invocation) error {
	*a.events = append(*a.events, "authorize-view")
	return a.err
}

func TestAuditListingAuthorizesOnceThenReads(t *testing.T) {
	t.Parallel()
	events := []string{}
	event := &model.AuditEvent{Id: model.NewId(), CreateAt: 100}
	persistence := &auditListingStoreFake{events: &events, list: []*model.AuditEvent{event}}
	service := newAuditListingService(persistence, &auditListingAuthorizerFake{events: &events})
	got, err := service.List(context.Background(), Invocation{}, ListAuditEventsQuery{Limit: 10, ActorID: model.NewId()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || persistence.input.Limit != 10 {
		t.Fatalf("result/input = %#v / %#v", got, persistence.input)
	}
	want := []string{"authorize-view", "list-audits"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAuditListingRejectsInvalidQuery(t *testing.T) {
	t.Parallel()
	events := []string{}
	service := newAuditListingService(&auditListingStoreFake{events: &events}, &auditListingAuthorizerFake{events: &events})
	_, err := service.List(context.Background(), Invocation{}, ListAuditEventsQuery{Limit: 500})
	if !Is(err, "audit.query.invalid") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"authorize-view"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
