// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"

	"github.com/sudosylabs/proctor/server/internal/errorflow"
	"github.com/sudosylabs/proctor/server/model"
)

const serverModulePath = "github.com/sudosylabs/proctor/server"

// TestRouteErrorTablesMatchReachableCodes holds every route's declared error
// table to what its operation can return, as computed by errorflow over the
// production program: a declared code nothing reachable can return is an
// over-declaration, and a code the operation certainly returns but does not
// declare would reach the client as an internal error.
func TestRouteErrorTablesMatchReachableCodes(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("loads and analyzes the whole server module")
	}
	loaded, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Dir: ".."}, serverModulePath+"/...")
	require.NoError(t, err)
	require.Zero(t, packages.PrintErrors(loaded))
	analysis, err := errorflow.Analyze(loaded, errorflow.Config{Module: serverModulePath,
		AppErrorPackage: serverModulePath + "/app/apperror", OpaquePackages: []string{serverModulePath + "/store"}})
	require.NoError(t, err)

	var routes []errorflow.Route
	for _, resource := range productionResources(agreementResourceApplications(), nil, BuildInfo{}, browserCookies{}, nil) {
		for _, definition := range resource.routes {
			template, _, compileErr := definition.path.compile(model.APIURLSuffix)
			require.NoError(t, compileErr)
			key := definition.method + " " + routeTemplateKey(template)
			operation := routeOperationName(definition)
			function := analysis.FunctionForRuntimeName(operation)
			require.NotNil(t, function, "%s: operation %s is not in the analyzed program", key, operation)
			routes = append(routes, errorflow.Route{Key: key, Operation: function, Declared: definition.declaredErrorCodes,
				Implied: routeImpliedErrorCodes(definition), Infeasible: routeInfeasibleErrorCodes(definition)})
		}
	}
	problems := errorflow.CheckRoutes(analysis, routes, routeErrorTableExceptions)
	require.Empty(t, problems, "route error tables disagree with what their operations can return:\n%s", strings.Join(problems, "\n"))
}

// routeErrorTableExceptions are route codes the analysis cannot see, keyed
// "<route key> <code>", each with the reason.
var routeErrorTableExceptions = map[string]string{}

func routeOperationName(definition routeDefinition) string {
	var operation any
	switch {
	case definition.operation != nil:
		operation = definition.operation
	case definition.protocolOperation != nil:
		operation = definition.protocolOperation
	default:
		operation = definition.upgradeOperation
	}
	return runtime.FuncForPC(reflect.ValueOf(operation).Pointer()).Name()
}

// routeImpliedErrorCodes are the codes the kernel adds, the idempotency codes
// the route's requirement implies, and the transport's own problem codes
// (health, say), which no application code mints; a table may name them.
func routeImpliedErrorCodes(definition routeDefinition) []string {
	implied := kernelErrorCodes(definition.auth, definition.method)
	for code := range transportProblemCodes {
		implied = append(implied, code)
	}
	if definition.idempotency != IdempotencyNone {
		implied = append(implied, "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress")
		if definition.idempotency == IdempotencyRequired {
			implied = append(implied, "idempotency.key_required")
		}
	}
	sort.Strings(implied)
	return slices.Compact(implied)
}

// routeInfeasibleErrorCodes are codes a route cannot produce whatever its
// operation contains: without an idempotency requirement the transport never
// passes a key, so no idempotency failure can occur.
func routeInfeasibleErrorCodes(definition routeDefinition) []string {
	if definition.idempotency != IdempotencyNone {
		return nil
	}
	return []string{"idempotency.conflict", "idempotency.in_progress", "idempotency.key_invalid", "idempotency.key_required"}
}

// routeTemplateKey drops the pattern of every path variable:
// /exams/{exam_id:[a-z]{26}} becomes /exams/{exam_id}.
func routeTemplateKey(template string) string {
	var key strings.Builder
	depth := 0
	skipping := false
	for _, character := range template {
		switch {
		case character == '{':
			depth++
			if depth == 1 {
				key.WriteRune(character)
				continue
			}
		case character == '}':
			depth--
			if depth == 0 {
				skipping = false
				key.WriteRune(character)
				continue
			}
		case character == ':' && depth == 1:
			skipping = true
			continue
		}
		if !skipping {
			key.WriteRune(character)
		}
	}
	return key.String()
}
