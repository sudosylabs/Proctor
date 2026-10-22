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

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type ownRetentionNoticesFake struct {
	store.RetentionStore
	requestedUser model.UserID
	limit         int
	items         []model.RetentionNotice
}

func (f *ownRetentionNoticesFake) ListNotices(_ context.Context, user model.UserID, _ model.RetentionRetirementID, limit int) ([]model.RetentionNotice, error) {
	f.requestedUser = user
	f.limit = limit
	return f.items, nil
}
func TestRetentionNoticesUseExactSessionRecipientAndRejectForeignProjection(t *testing.T) {
	t.Parallel()
	fixture := newRetentionPolicyFixture(t)
	user := fixture.invocation.Principal().UserID
	persistence := &ownRetentionNoticesFake{items: []model.RetentionNotice{{RecipientUserID: user}, {RecipientUserID: user}}}
	fixture.service.lifecycle = persistence
	app := &App{retentionPolicyService: fixture.service}
	page, err := app.ListRetentionNotices(t.Context(), fixture.invocation, "", 1)
	require.NoError(t, err, "page=%#v err", page)
	require.True(t, page.HasMore, "page=%#v err", page)
	require.Len(t, page.Items, 1, "page=%#v err", page)
	require.Equal(t, user, persistence.requestedUser, "page=%#v err", page)
	require.Equal(t, 2, persistence.limit, "page=%#v err", page)
	persistence.items = []model.RetentionNotice{{RecipientUserID: model.NewUserID()}}
	page, err = app.ListRetentionNotices(t.Context(), fixture.invocation, "", 1)
	require.True(t, apperror.Is(err, "retention.unavailable"), "foreign recipient escaped")
	require.Nil(t, page, "foreign recipient escaped")
	for _, kind := range []string{"PAT", "restricted"} {
		p := fixture.invocation.Principal()
		if kind == "PAT" {
			p.CredentialType = model.CredentialPersonalAccessToken
			p.SessionID = ""
		} else {
			p.MFARecoveryRequired = true
		}
		persistence.requestedUser = ""
		page, err = app.ListRetentionNotices(t.Context(), invocation.New(p, fixture.invocation.RequestMetadata()), "", 1)
		require.True(t, apperror.Is(err, "authentication.required"), "%s accessed notices", kind)
		require.Nil(t, page, "%s accessed notices", kind)
		require.True(t, persistence.requestedUser.IsZero(), "%s accessed notices", kind)
	}
}
