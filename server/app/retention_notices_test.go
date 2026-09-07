// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
	"testing"
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
	app := &App{retentionPolicy: fixture.service}
	page, err := app.ListRetentionNotices(t.Context(), fixture.invocation, "", 1)
	if err != nil || !page.HasMore || len(page.Items) != 1 || persistence.requestedUser != user || persistence.limit != 2 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	persistence.items = []model.RetentionNotice{{RecipientUserID: model.NewUserID()}}
	if page, err = app.ListRetentionNotices(t.Context(), fixture.invocation, "", 1); !Is(err, "retention.unavailable") || page != nil {
		t.Fatal("foreign recipient escaped")
	}
	for _, kind := range []string{"PAT", "restricted"} {
		p := fixture.invocation.Principal()
		if kind == "PAT" {
			p.CredentialType = model.CredentialPersonalAccessToken
			p.SessionID = ""
		} else {
			p.MFARecoveryRequired = true
		}
		persistence.requestedUser = ""
		page, err = app.ListRetentionNotices(t.Context(), NewInvocation(p, fixture.invocation.RequestMetadata()), "", 1)
		if !Is(err, "authentication.required") || page != nil || !persistence.requestedUser.IsZero() {
			t.Fatalf("%s accessed notices", kind)
		}
	}
}
