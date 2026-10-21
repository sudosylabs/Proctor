// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/store"
)

func TestAuthenticationDoesNotRetainRootStore(t *testing.T) {
	t.Parallel()

	rootStore := reflect.TypeOf((*store.Store)(nil)).Elem()
	service := reflect.TypeOf(authenticationService{})
	for index := 0; index < service.NumField(); index++ {
		require.NotEqual(t, rootStore, service.Field(index).Type, "authenticationService retains root store in field")
	}
	selfSessions := reflect.TypeOf(selfSessionService{})
	for index := 0; index < selfSessions.NumField(); index++ {
		require.NotEqual(t, rootStore, selfSessions.Field(index).Type, "selfSessionService retains root store in field")
	}
}
