// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
package review

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type nativeReviewAuthorizer struct {
	Authorizer
	err   error
	calls int
}

func (a *nativeReviewAuthorizer) AuthorizeView(context.Context, invocation.Invocation, model.SubmissionID) error {
	a.calls++
	return a.err
}

type nativeReviewStore struct {
	store.ExamIntegrityReviewStore
	calls int
}

func (s *nativeReviewStore) ListNativeConditions(context.Context, store.NativeConditionListOptions) (*store.NativeConditionPage, error) {
	s.calls++
	return &store.NativeConditionPage{Items: []model.NativeConditionEvidence{}}, nil
}
func TestNativeConditionsRequireCurrentDetailedReviewAuthority(t *testing.T) {
	f := newReviewFixture(t)
	denied := errors.New("review denied")
	auth := &nativeReviewAuthorizer{Authorizer: f.authorizer, err: denied}
	p := &nativeReviewStore{ExamIntegrityReviewStore: f.persistence}
	f.service.deps.Authorizer = auth
	f.service.deps.Persistence = p
	query := store.NativeConditionListOptions{SubmissionID: f.submissionID, Limit: 100}
	_, err := f.service.ListNativeConditions(context.Background(), f.invocation, query)
	require.ErrorIs(t, err, denied, "private conditions read before authorization")
	require.Equal(t, 0, p.calls, "private conditions read before authorization")
	auth.err = nil
	page, err := f.service.ListNativeConditions(context.Background(), f.invocation, query)
	require.NoError(t, err, "authorized condition read failed")
	require.NotNil(t, page.Items, "authorized condition read failed")
	require.Equal(t, 1, p.calls, "authorized condition read failed")
	require.Equal(t, 2, auth.calls, "authorized condition read failed")
	query.Limit = 101
	_, err = f.service.ListNativeConditions(context.Background(), f.invocation, query)
	require.Error(t, err, "unbounded review read")
	require.Equal(t, 1, p.calls, "unbounded review read")
}
