// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// TestAuthorizationHelpersShareOneShape keeps every authorization helper in
// app and its child modules (an exported method, concrete or on a port,
// named Authorize or Authorize<Concept>) in the one shape of the canonical
// decision: it takes (context.Context, invocation.Invocation, ...) and
// returns error, or (R, error) where R is a named struct that says what the
// helper reports. A non-audited predicate named Can or Can<Concept> returns
// (bool, error). A helper that forwards to exactly one other named helper
// shares its name, so one concept has one name.
func TestAuthorizationHelpersShareOneShape(t *testing.T) {
	t.Parallel()
	for _, violation := range authorizationHelperViolations(typedServerPackages(t), serverModule) {
		assert.Fail(t, violation)
	}
}

func TestAuthorizationHelperRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"app/invocation/invocation.go": "package invocation\n\ntype Invocation struct{}\n",
		"app/app.go": `package app

import (
	"context"

	"fixture/app/invocation"
)

type Scope struct{ ID string }

type authorizer interface {
	Authorize(context.Context, invocation.Invocation, string) error
	AuthorizeScope(context.Context, invocation.Invocation, string) (Scope, error)
	AuthorizeManage(context.Context, invocation.Invocation, string) (bool, error)
	AuthorizeWithScope(context.Context, invocation.Invocation, string) (string, string, error)
	AuthorizeRead(context.Context, string) error
	CanDelegate(context.Context, invocation.Invocation, string) error
}

type sittings interface {
	AuthorizeView(context.Context, invocation.Invocation, string) error
}

type adapter struct{ sittings sittings }

func (a adapter) AuthorizeSittingView(ctx context.Context, invocation invocation.Invocation, id string) error {
	return a.sittings.AuthorizeView(ctx, invocation, id)
}

func (a adapter) authorizeLocally(context.Context, string) bool { return true }
`,
	})
	require.Equal(t, []string{
		"app.go:14: fixture/app.authorizer.AuthorizeManage returns (bool, error); return error, or (R, error) with R a named struct result",
		"app.go:15: fixture/app.authorizer.AuthorizeWithScope returns (string, string, error); return error, or (R, error) with R a named struct result",
		"app.go:16: fixture/app.authorizer.AuthorizeRead takes (context.Context, string); take (context.Context, invocation.Invocation, ...)",
		"app.go:17: fixture/app.authorizer.CanDelegate returns error; a Can predicate returns (bool, error), an error-returning check is named Authorize<Concept>",
		"app.go:26: fixture/app.adapter.AuthorizeSittingView forwards to AuthorizeView; name one concept once",
	}, authorizationHelperViolations(loaded, "fixture"))
}

func authorizationHelperViolations(loaded []*packages.Package, module string) []string {
	var violations []string
	for _, pkg := range loaded {
		if pkg.PkgPath != module+"/app" && !strings.HasPrefix(pkg.PkgPath, module+"/app/") {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			typeName, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || typeName.IsAlias() {
				continue
			}
			named, ok := typeName.Type().(*types.Named)
			if !ok {
				continue
			}
			var methods []*types.Func
			if port, ok := named.Underlying().(*types.Interface); ok {
				for index := range port.NumExplicitMethods() {
					methods = append(methods, port.ExplicitMethod(index))
				}
			} else {
				for index := range named.NumMethods() {
					methods = append(methods, named.Method(index))
				}
			}
			for _, method := range methods {
				if problem := authorizationHelperShape(method, module); problem != "" {
					violations = append(violations, fmt.Sprintf("%s: %s.%s.%s %s", sourcePosition(pkg, method.Pos()), pkg.PkgPath, name, method.Name(), problem))
				}
			}
		}
		violations = append(violations, authorizationForwardingViolations(pkg)...)
	}
	sort.Strings(violations)
	return violations
}

func authorizationHelperShape(method *types.Func, module string) string {
	name := method.Name()
	authorize := name == "Authorize" || (strings.HasPrefix(name, "Authorize") && isUpperASCII(name[len("Authorize")]))
	predicate := name == "Can" || (strings.HasPrefix(name, "Can") && isUpperASCII(name[len("Can")]))
	if !authorize && !predicate {
		return ""
	}
	signature := method.Type().(*types.Signature)
	results := signature.Results()
	qualifier := func(p *types.Package) string { return p.Name() }
	if predicate {
		if results.Len() == 2 && types.Identical(results.At(0).Type(), types.Typ[types.Bool]) && isErrorType(results.At(1).Type()) {
			return ""
		}
		return "returns " + tupleString(results, qualifier) + "; a Can predicate returns (bool, error), an error-returning check is named Authorize<Concept>"
	}
	parameters := signature.Params()
	if parameters.Len() < 2 || types.TypeString(parameters.At(0).Type(), nil) != "context.Context" || !isInvocationType(parameters.At(1).Type(), module) {
		return "takes " + tupleString(parameters, qualifier) + "; take (context.Context, invocation.Invocation, ...)"
	}
	switch {
	case results.Len() == 1 && isErrorType(results.At(0).Type()):
		return ""
	case results.Len() == 2 && isNamedStruct(results.At(0).Type()) && isErrorType(results.At(1).Type()):
		return ""
	}
	return "returns " + tupleString(results, qualifier) + "; return error, or (R, error) with R a named struct result"
}

// authorizationForwardingViolations reports a concrete Authorize<Concept>
// method whose only call to another authorization helper is to a differently
// named one other than the canonical Authorize.
func authorizationForwardingViolations(pkg *packages.Package) []string {
	var violations []string
	for _, file := range pkg.Syntax {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Body == nil || !strings.HasPrefix(function.Name.Name, "Authorize") || function.Name.Name == "Authorize" {
				continue
			}
			var called []string
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if ok && strings.HasPrefix(selector.Sel.Name, "Authorize") {
					called = append(called, selector.Sel.Name)
				}
				return true
			})
			if len(called) == 1 && called[0] != function.Name.Name && called[0] != "Authorize" {
				receiver := receiverTypeName(function.Recv.List[0].Type)
				violations = append(violations, fmt.Sprintf("%s: %s.%s.%s forwards to %s; name one concept once",
					sourcePosition(pkg, function.Name.Pos()), pkg.PkgPath, receiver, function.Name.Name, called[0]))
			}
		}
	}
	return violations
}

func isUpperASCII(character byte) bool { return character >= 'A' && character <= 'Z' }

func isErrorType(candidate types.Type) bool {
	return types.Identical(candidate, types.Universe.Lookup("error").Type())
}

func isInvocationType(candidate types.Type, module string) bool {
	named, ok := types.Unalias(candidate).(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == module+"/app/invocation" && named.Obj().Name() == "Invocation"
}

func isNamedStruct(candidate types.Type) bool {
	if pointer, ok := candidate.(*types.Pointer); ok {
		candidate = pointer.Elem()
	}
	named, ok := types.Unalias(candidate).(*types.Named)
	if !ok {
		return false
	}
	_, ok = named.Underlying().(*types.Struct)
	return ok
}

func tupleString(tuple *types.Tuple, qualifier types.Qualifier) string {
	parts := make([]string, 0, tuple.Len())
	for variable := range tuple.Variables() {
		parts = append(parts, types.TypeString(variable.Type(), qualifier))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
