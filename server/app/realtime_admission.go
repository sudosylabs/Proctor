// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

// realtimeAdmissionService admits long-lived WebSocket clients. It
// revalidates a Session principal before the transport keeps trusting it and
// authorizes each subscription against current resource state, concealing
// Exams and Sittings the caller may not see.
type realtimeAdmissionService struct {
	principals    *authenticationService
	exams         realtimeExamViewAuthorizer
	sittings      realtimeSittingViewAuthorizer
	authorization *accessControlService
	operational   OperationalRecorder
}

// realtimeExamViewAuthorizer is the Exam relationship gate a subscription
// uses; its tests substitute a fake.
type realtimeExamViewAuthorizer interface {
	AuthorizeExamView(context.Context, invocation.Invocation, model.ExamID) error
}

// realtimeSittingViewAuthorizer is the Sitting relationship gate a
// subscription uses; its tests substitute a fake.
type realtimeSittingViewAuthorizer interface {
	AuthorizeSittingView(context.Context, invocation.Invocation, model.ExamSittingID) error
}

var (
	realtimeExamFailures = apperror.Concealment{
		Unavailable: "exam.unavailable",
		Concealed:   []apperror.Code{"exam.not_found"},
	}
	realtimeSittingFailures = apperror.Concealment{
		Unavailable: "exam.sitting.unavailable",
		Concealed:   []apperror.Code{"exam.sitting.not_found"},
	}
)

// AuthorizeWebSocketSubscription authorizes one subscription Action on its
// Resource. Exams and Sittings use their relationship gates; every other
// Resource takes the audited Access Control decision.
func (s *realtimeAdmissionService) AuthorizeWebSocketSubscription(
	ctx context.Context,
	invocation invocation.Invocation,
	action model.Action,
	resource model.Resource,
) (resultErr error) {
	defer func() { recordOperational(s.operational, "realtime", "subscription_authorize", resultErr) }()
	definition, ok := model.DefinitionForAction(action)
	if !ok || resource.Validate() != nil || definition.ResourceType != resource.Type {
		return invalidRealtimeRequest("subscription")
	}
	if action == model.ActionExamView && resource.Type == model.ResourceExam {
		examID, err := model.ParseExamID(resource.ID)
		if err != nil {
			return invalidRealtimeRequest("subscription")
		}
		return realtimeExamFailures.Conceal(s.exams.AuthorizeExamView(ctx, invocation, examID))
	}
	if action == model.ActionExamSittingView && resource.Type == model.ResourceExamSitting {
		sittingID, err := model.ParseExamSittingID(resource.ID)
		if err != nil {
			return invalidRealtimeRequest("subscription")
		}
		return realtimeSittingFailures.Conceal(s.sittings.AuthorizeSittingView(ctx, invocation, sittingID))
	}
	err := s.authorization.Authorize(ctx, invocation, action, resource)
	recordOperational(s.operational, "authorization", "decision", err)
	return err
}

// ValidateWebSocketPrincipal revalidates the authoritative Session and User
// state behind a long-lived connection's principal.
func (s *realtimeAdmissionService) ValidateWebSocketPrincipal(ctx context.Context, principal model.Principal) error {
	err := s.principals.validatePrincipal(ctx, principal)
	recordOperational(s.operational, "realtime", "principal_validate", err)
	return err
}
