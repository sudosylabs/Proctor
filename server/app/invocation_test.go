// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app_test

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

func TestInvocationCarriesPrincipalAndSafeMetadataImmutably(t *testing.T) {
	t.Parallel()

	principal := model.Principal{
		UserId:           model.NewId(),
		SessionId:        model.NewId(),
		CredentialId:     model.NewId(),
		CredentialType:   model.CredentialSessionAccess,
		CredentialScopes: []string{"scope-a"},
	}
	metadata := model.RequestMetadata{
		RequestId: "req-1",
		IPAddress: "127.0.0.1",
		UserAgent: "proctor-test",
	}

	invocation := app.NewInvocation(principal, metadata)
	if !reflect.DeepEqual(invocation.Principal(), principal) {
		t.Fatalf("Principal() = %#v", invocation.Principal())
	}
	got := invocation.RequestMetadata()
	if got != metadata {
		t.Fatalf("RequestMetadata() = %#v, want %#v", got, metadata)
	}

	// Mutating the source metadata after construction must not affect the
	// invocation; the value is a snapshot.
	metadata.RequestId = "mutated"
	principal.CredentialScopes[0] = "mutated"
	if invocation.RequestMetadata().RequestId != "req-1" {
		t.Fatal("Invocation retained a live reference to caller metadata")
	}
	gotPrincipal := invocation.Principal()
	gotPrincipal.CredentialScopes[0] = "returned-value-mutated"
	if invocation.Principal().CredentialScopes[0] != "scope-a" {
		t.Fatal("Invocation exposed mutable principal scope storage")
	}
}
