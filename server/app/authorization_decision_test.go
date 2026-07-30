// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestAuthorizationDecisionIsSealedBoundAndOneUse(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	service := &AuthorizationService{
		now: nowFunc(now), decisionKey: []byte("test-authorization-decision-key"),
	}
	principal := model.Principal{
		UserId: model.NewId(), SessionId: model.NewId(), CredentialId: model.NewId(),
		CredentialType: model.CredentialSessionAccess,
	}
	resource := model.Resource{Type: model.ResourceInstitution, Id: model.NewId()}
	metadata := model.RequestMetadata{RequestId: "request-1"}
	decision := authorizationDecision{
		userID: principal.UserId, sessionID: principal.SessionId,
		credentialID: principal.CredentialId, action: model.ActionRoleManage,
		resource: resource, requestID: metadata.RequestId,
		authorizedAt: now.UnixMilli(),
		expiresAt:    now.Add(authorizationDecisionTTL).UnixMilli(),
	}
	decision.proof = service.signDecision(decision)

	ctx := contextWithAuthorizationDecision(context.Background(), decision)
	actual, ok := service.consumePreauthorizedResource(
		ctx,
		principal,
		model.ActionRoleManage,
		model.ResourceInstitution,
		metadata,
	)
	if !ok || actual != resource {
		t.Fatalf("valid authorization decision was rejected: %#v, %v", actual, ok)
	}
	if _, ok := service.consumePreauthorizedResource(
		ctx,
		principal,
		model.ActionRoleManage,
		model.ResourceInstitution,
		metadata,
	); ok {
		t.Fatal("authorization decision was consumed more than once")
	}

	tampered := decision
	tampered.resource.Id = model.NewId()
	if _, ok := service.consumePreauthorizedResource(
		contextWithAuthorizationDecision(context.Background(), tampered),
		principal,
		model.ActionRoleManage,
		model.ResourceInstitution,
		metadata,
	); ok {
		t.Fatal("tampered authorization decision was accepted")
	}

	wrongRequest := metadata
	wrongRequest.RequestId = "request-2"
	if _, ok := service.consumePreauthorizedResource(
		contextWithAuthorizationDecision(context.Background(), decision),
		principal,
		model.ActionRoleManage,
		model.ResourceInstitution,
		wrongRequest,
	); ok {
		t.Fatal("authorization decision was accepted for another request")
	}

	service.now = nowFunc(now.Add(authorizationDecisionTTL))
	if _, ok := service.consumePreauthorizedResource(
		contextWithAuthorizationDecision(context.Background(), decision),
		principal,
		model.ActionRoleManage,
		model.ResourceInstitution,
		metadata,
	); ok {
		t.Fatal("expired authorization decision was accepted")
	}
}

func nowFunc(now time.Time) func() time.Time {
	return func() time.Time { return now }
}
