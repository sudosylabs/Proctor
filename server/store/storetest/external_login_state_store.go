// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package storetest

import (
	"context"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func TestExternalLoginStateStore(t *testing.T, ss store.Store) {
	ctx := context.Background()
	now := model.GetMillis()
	stateToken := model.NewCredentialToken()
	bindingToken := model.NewCredentialToken()
	input := &model.ExternalLoginState{
		Provider: "campus-cas", StateHash: model.HashToken(stateToken),
		BindingHash: model.HashToken(bindingToken), ReturnTo: "/exams?active=true",
		ClientType: model.SessionClientDesktop, DeviceID: "desktop-1",
		DeviceName: "Desktop", ExpiresAt: model.TimeFromMillis(now + 60_000),
	}
	saved, err := ss.ExternalLoginState().Save(ctx, input)
	requireNoError(t, err)
	if !saved.ID.IsValid() || !input.ID.IsZero() {
		t.Fatalf("Save() saved=%#v input=%#v", saved, input)
	}
	got, err := ss.ExternalLoginState().GetByStateHash(ctx, saved.StateHash)
	requireNoError(t, err)
	if got.ID != saved.ID || got.BindingHash != saved.BindingHash {
		t.Fatalf("GetByStateHash() = %#v", got)
	}
	if _, err := ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		model.HashToken(model.NewCredentialToken()),
		now+1,
	); !store.IsNotFound(err) {
		t.Fatalf("Consume(wrong binding) error = %v", err)
	}
	consumed, err := ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		saved.BindingHash,
		now+2,
	)
	requireNoError(t, err)
	if consumed.ConsumedAt.Millis() != now+2 {
		t.Fatalf("Consume() = %#v", consumed)
	}
	if _, err := ss.ExternalLoginState().Consume(
		ctx,
		saved.Provider,
		saved.StateHash,
		saved.BindingHash,
		now+3,
	); !store.IsNotFound(err) {
		t.Fatalf("Consume(replay) error = %v", err)
	}

	expired := &model.ExternalLoginState{
		Provider:    "campus-cas",
		StateHash:   model.HashToken(model.NewCredentialToken()),
		BindingHash: model.HashToken(model.NewCredentialToken()),
		ReturnTo:    "/", ClientType: model.SessionClientWeb,
		ExpiresAt: model.TimeFromMillis(now + 120_000),
	}
	expired, err = ss.ExternalLoginState().Save(ctx, expired)
	requireNoError(t, err)
	if _, err := ss.ExternalLoginState().Consume(
		ctx,
		expired.Provider,
		expired.StateHash,
		expired.BindingHash,
		model.MillisFromTime(expired.ExpiresAt),
	); !store.IsNotFound(err) {
		t.Fatalf("Consume(expired) error = %v", err)
	}
}
