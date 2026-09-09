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

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type nativeReviewAuthorizer struct {
	Authorizer
	err   error
	calls int
}

func (a *nativeReviewAuthorizer) AuthorizeView(context.Context, Call, model.SubmissionID) error {
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
	query := NativeConditionListQuery{SubmissionID: f.submissionID, Limit: 100}
	if _, err := f.service.ListNativeConditions(context.Background(), f.call, query); !errors.Is(err, denied) || p.calls != 0 {
		t.Fatal("private conditions read before authorization")
	}
	auth.err = nil
	if page, err := f.service.ListNativeConditions(context.Background(), f.call, query); err != nil || page.Items == nil || p.calls != 1 || auth.calls != 2 {
		t.Fatal("authorized condition read failed")
	}
	query.Limit = 101
	if _, err := f.service.ListNativeConditions(context.Background(), f.call, query); err == nil || p.calls != 1 {
		t.Fatal("unbounded review read")
	}
}
