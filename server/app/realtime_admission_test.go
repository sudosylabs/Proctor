// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/model"
)

func TestAuthorizeWebSocketExamSubscriptionUsesExamRelationshipGate(t *testing.T) {
	t.Parallel()
	exams := &realtimeExamViewFake{}
	admission := &realtimeAdmissionService{exams: exams}
	principal := testExamPrincipal(model.NewUserID())
	examID := model.NewExamID()
	err := admission.AuthorizeWebSocketSubscription(context.Background(), NewInvocation(principal, model.RequestMetadata{RequestID: "subscribe"}),
		model.ActionExamView, model.Resource{Type: model.ResourceExam, ID: examID.String()})
	require.NoError(t, err)
	require.Equal(t, examID, exams.examID, "authorization = %s %#v", exams.examID, exams.invocation)
	require.Equal(t, principal.UserID, exams.invocation.Principal().UserID, "authorization = %s %#v", exams.examID, exams.invocation)
}

func TestAuthorizeWebSocketExamSubscriptionConcealsMissingAndDeniedExams(t *testing.T) {
	t.Parallel()
	principal := testExamPrincipal(model.NewUserID())
	resource := model.Resource{Type: model.ResourceExam, ID: model.NewExamID().String()}
	for _, failure := range []error{apperror.New("exam.not_found"), apperror.New("authorization.denied")} {
		admission := &realtimeAdmissionService{exams: &realtimeExamViewFake{err: failure}}
		err := admission.AuthorizeWebSocketSubscription(context.Background(), NewInvocation(principal, model.RequestMetadata{}), model.ActionExamView, resource)
		require.True(t, apperror.Is(err, "resource.not_found"), "error = %v, want concealed resource.not_found", err)
	}
}

func TestAuthorizeWebSocketSittingSubscriptionUsesRelationshipGate(t *testing.T) {
	t.Parallel()
	sittings := &realtimeSittingViewFake{}
	admission := &realtimeAdmissionService{sittings: sittings}
	principal := testExamPrincipal(model.NewUserID())
	sittingID := model.NewExamSittingID()
	err := admission.AuthorizeWebSocketSubscription(context.Background(),
		NewInvocation(principal, model.RequestMetadata{RequestID: "sitting-subscribe"}), model.ActionExamSittingView,
		model.Resource{Type: model.ResourceExamSitting, ID: sittingID.String()})
	require.NoError(t, err)
	require.Equal(t, sittingID, sittings.sittingID, "authorization = %s %#v", sittings.sittingID, sittings.invocation)
	require.Equal(t, principal.UserID, sittings.invocation.Principal().UserID, "authorization = %s %#v", sittings.sittingID, sittings.invocation)
}

func TestAuthorizeWebSocketSittingSubscriptionConcealsMissingAndDeniedTargets(t *testing.T) {
	t.Parallel()
	principal := testExamPrincipal(model.NewUserID())
	resource := model.Resource{Type: model.ResourceExamSitting, ID: model.NewExamSittingID().String()}
	for _, failure := range []error{apperror.New("exam.sitting.not_found"), apperror.New("authorization.denied")} {
		admission := &realtimeAdmissionService{sittings: &realtimeSittingViewFake{err: failure}}
		err := admission.AuthorizeWebSocketSubscription(context.Background(), NewInvocation(principal, model.RequestMetadata{}),
			model.ActionExamSittingView, resource)
		require.True(t, apperror.Is(err, "resource.not_found"), "error = %v, want concealed resource.not_found", err)
	}
}

type realtimeExamViewFake struct {
	invocation Invocation
	examID     model.ExamID
	err        error
}

func (f *realtimeExamViewFake) AuthorizeView(_ context.Context, invocation Invocation, examID model.ExamID) error {
	f.invocation, f.examID = invocation, examID
	return f.err
}

type realtimeSittingViewFake struct {
	invocation Invocation
	sittingID  model.ExamSittingID
	err        error
}

func (f *realtimeSittingViewFake) AuthorizeView(_ context.Context, invocation Invocation, sittingID model.ExamSittingID) error {
	f.invocation, f.sittingID = invocation, sittingID
	return f.err
}

func testExamPrincipal(userID model.UserID) model.Principal {
	return model.Principal{
		UserID: userID, SessionID: model.NewSessionID(), CredentialID: model.PrincipalCredentialID(model.NewID()),
		CredentialType: model.CredentialSessionAccess, AuthenticationMethod: "password",
		AuthenticationStrength: model.AuthenticationSingleFactor, ClientType: model.SessionClientWeb,
		AuthenticatedAt: time.Now().UTC(),
	}
}
