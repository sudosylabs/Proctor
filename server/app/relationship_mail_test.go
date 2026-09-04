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
)

type relationshipUserStoreTestFake struct{}

func (relationshipUserStoreTestFake) Get(_ context.Context, id string) (*model.User, error) {
	parsed, err := model.ParseUserID(id)
	if err != nil {
		return nil, err
	}
	return &model.User{ID: parsed, Revision: 1}, nil
}

type relationshipMailPreparerTestFake struct {
	requests []relationshipTransitionMailPreparation
}

func (f *relationshipMailPreparerTestFake) PrepareRelationshipTransition(request relationshipTransitionMailPreparation) (*store.PreparedMail, error) {
	f.requests = append(f.requests, request)
	return &store.PreparedMail{}, nil
}
