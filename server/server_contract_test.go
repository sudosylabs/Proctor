// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package server_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	server "github.com/sudosylabs/proctor/server"
)

func TestFacadePublicContractExcludesLegacyImplementations(t *testing.T) {
	t.Parallel()

	var _ func(context.Context, ...server.Option) (*server.Server, error) = server.New
	serverType := reflect.TypeOf((*server.Server)(nil))
	for methodIndex := 0; methodIndex < serverType.NumMethod(); methodIndex++ {
		method := serverType.Method(methodIndex)
		for parameterIndex := 0; parameterIndex < method.Type.NumIn(); parameterIndex++ {
			assertNotLegacyType(t, method.Name, method.Type.In(parameterIndex))
		}
		for resultIndex := 0; resultIndex < method.Type.NumOut(); resultIndex++ {
			assertNotLegacyType(t, method.Name, method.Type.Out(resultIndex))
		}
	}
}

func assertNotLegacyType(t *testing.T, method string, valueType reflect.Type) {
	t.Helper()

	for valueType.Kind() == reflect.Pointer || valueType.Kind() == reflect.Slice || valueType.Kind() == reflect.Array {
		valueType = valueType.Elem()
	}
	packagePath := valueType.PkgPath()
	for _, forbiddenPackage := range []string{
		"github.com/sudosylabs/proctor/server/app",
		"github.com/sudosylabs/proctor/server/platform",
	} {
		if packagePath == forbiddenPackage || strings.HasPrefix(packagePath, forbiddenPackage+"/") {
			t.Errorf("Server.%s exposes legacy implementation type %s", method, valueType)
		}
	}
}
