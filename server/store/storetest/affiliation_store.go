// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestAffiliationStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	user := saveUser(t, ctx, ss)
	start := model.GetMillis() + 1000
	saved, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start,
	})
	requireNoError(t, err)
	if !model.IsValidId(saved.Id) {
		t.Fatalf("Save() = %#v", saved)
	}
	got, err := ss.Affiliation().Get(ctx, saved.Id)
	requireNoError(t, err)
	if *got != *saved {
		t.Fatalf("Get() = %#v, want %#v", got, saved)
	}
	active, err := ss.Affiliation().ListActiveByUser(ctx, user.Id, start+1)
	requireNoError(t, err)
	if len(active) != 1 || active[0].Id != saved.Id {
		t.Fatalf("ListActiveByUser() = %#v", active)
	}
	_, err = ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start + 2,
	})
	var conflict *store.ErrConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("duplicate active affiliation error = %v", err)
	}
	ended, err := ss.Affiliation().End(ctx, saved.Id, start+10)
	requireNoError(t, err)
	if ended.EndAt != start+10 {
		t.Fatalf("End() = %#v", ended)
	}
	active, err = ss.Affiliation().ListActiveByUser(ctx, user.Id, start+11)
	requireNoError(t, err)
	if len(active) != 0 {
		t.Fatalf("active after end = %#v", active)
	}
	history, err := ss.Affiliation().ListByUser(ctx, user.Id)
	requireNoError(t, err)
	if len(history) != 1 || history[0].EndAt == 0 {
		t.Fatalf("ListByUser() = %#v", history)
	}
	next, err := ss.Affiliation().Save(ctx, &model.Affiliation{
		UserId: user.Id, Kind: model.AffiliationStudent, StartAt: start + 10,
	})
	requireNoError(t, err)
	if next.Id == saved.Id {
		t.Fatalf("new effective range reused the old row: %#v", next)
	}
}
