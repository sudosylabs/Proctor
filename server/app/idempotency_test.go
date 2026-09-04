// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"bytes"
	"testing"

	"github.com/sudosylabs/proctor/server/model"
)

func TestCommandIdempotencyUsesUserOperationAndSemanticCommand(t *testing.T) {
	userID := model.NewUserID()
	invocation := NewInvocation(model.Principal{UserID: userID}, model.RequestMetadata{})
	first, err := newCommandIdempotency(invocation, "thing.create.v1", "client-key", struct {
		Name string `json:"name"`
	}{Name: "same"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := newCommandIdempotency(invocation, "thing.create.v1", "client-key", struct {
		Name string `json:"name"`
	}{Name: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if first.UserID != userID || first.Operation != "thing.create.v1" || first.KeyDigest != second.KeyDigest || first.Fingerprint != second.Fingerprint {
		t.Fatalf("unstable idempotency = %#v %#v", first, second)
	}
	different, err := newCommandIdempotency(invocation, "thing.create.v1", "client-key", struct {
		Name string `json:"name"`
	}{Name: "different"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Fingerprint[:], different.Fingerprint[:]) {
		t.Fatal("different semantic command has the same fingerprint")
	}
	if first.Retention != commandIdempotencyRetention || first.Wait != commandIdempotencyWait {
		t.Fatalf("policy = %#v", first)
	}
}
