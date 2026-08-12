// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"reflect"
	"testing"

	"github.com/sudosylabs/proctor/server/store"
)

func TestAuthenticationDoesNotRetainRootStore(t *testing.T) {
	t.Parallel()

	rootStore := reflect.TypeOf((*store.Store)(nil)).Elem()
	service := reflect.TypeOf(authenticationService{})
	for index := 0; index < service.NumField(); index++ {
		if service.Field(index).Type == rootStore {
			t.Fatalf("authenticationService retains root store in field %q", service.Field(index).Name)
		}
	}
	selfSessions := reflect.TypeOf(selfSessionService{})
	for index := 0; index < selfSessions.NumField(); index++ {
		if selfSessions.Field(index).Type == rootStore {
			t.Fatalf("selfSessionService retains root store in field %q", selfSessions.Field(index).Name)
		}
	}
}
