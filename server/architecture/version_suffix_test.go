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
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// versionedStringAllowlist names the only production string values that may
// carry a version segment, each because a party outside the server owns it.
// A path under the public HTTP API generation (/api/v1/...) is also allowed.
var versionedStringAllowlist = map[string]string{
	".v1": "Desktop native coverage keys are defined by the Desktop native agreement",
	"https://acme-v02.api.letsencrypt.org/directory": "the ACME directory URL Let's Encrypt publishes",
	"proctor-lockup-25d-v1.png":                      "a released, digest-pinned mail image file name",
}

// versionedString matches a v<digits> segment delimited by '.', '_', ':',
// '/', '-' or a NUL domain separator, such as "exam.create.v1" or
// "proctor:tag:v1\x00".
var versionedString = regexp.MustCompile(`(^|[._:/-])v[0-9]+($|[._:/\x00-])`)

// versionedIdentifier matches a declared Go name that ends in a version, such
// as ResultV1 or encode_v2.
var versionedIdentifier = regexp.MustCompile(`(V|_v)[0-9]+$`)

// httpAPIGenerationPath matches a path under the public HTTP API generation
// clients address, or a Mailpit API path the development seeder calls.
var httpAPIGenerationPath = regexp.MustCompile(`^/api/v1([/?]|$)`)

// commandOperation is the one spelling of a retryable command's operation:
// lowercase dot-separated snake_case segments, domain first, verb last.
var commandOperation = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// TestNamesCarryNoVersionSuffix keeps internal names unversioned. Before the
// first release a persisted or digested format changes outright, so no
// production identifier ends in V<n> or _v<n>, and no production string carries
// a v<n> segment unless an external party owns it. Command operations are
// spelled one way, as dot-separated snake_case: every constant passed to
// app/idempotency Require or Prepare, every store constant named *Operation,
// and every constant named idempotencyOperation*.
func TestNamesCarryNoVersionSuffix(t *testing.T) {
	t.Parallel()
	require.Empty(t, versionSuffixViolations(typedServerPackages(t), versionedStringAllowlist))
}

func TestVersionSuffixRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"app/idempotency/idempotency.go": `package idempotency

func Prepare(_ any, operation, key string, _ any) error { return nil }
`,
		"app/commands.go": `package app

import "fixture/app/idempotency"

const createOperation = "thing.create.v1"

const digestTag = "proctor:thing:v2\x00"

type ResultV1 struct{ CountV2 int }

func encode_v1() {}

const idempotencyOperationArchive = "Thing.Archive"

func run(operation string) {
	_ = idempotency.Prepare(nil, "Thing-Create", "key", nil)
	_ = idempotency.Prepare(nil, operation, "key", nil)
	_ = idempotency.Prepare(nil, "thing.update", "key", nil)
	_ = "/api/v1/things"
	_ = "/api/v2/things"
}
`,
	})
	expected := []string{
		`commands.go:11: identifier encode_v1 ends in a version`,
		`commands.go:13: operation "Thing.Archive" is not dot-separated snake_case`,
		`commands.go:16: operation "Thing-Create" is not dot-separated snake_case`,
		`commands.go:20: string "/api/v2/things" carries a version segment`,
		`commands.go:5: string "thing.create.v1" carries a version segment`,
		`commands.go:7: string "proctor:thing:v2\x00" carries a version segment`,
		`commands.go:9: identifier CountV2 ends in a version`,
		`commands.go:9: identifier ResultV1 ends in a version`,
	}
	require.Equal(t, expected, versionSuffixViolations(loaded, nil))
}

func versionSuffixViolations(loaded []*packages.Package, allowlist map[string]string) []string {
	var violations []string
	for _, pkg := range loaded {
		for _, file := range pkg.Syntax {
			imports := make(map[*ast.BasicLit]bool, len(file.Imports))
			for _, spec := range file.Imports {
				imports[spec.Path] = true
			}
			ast.Inspect(file, func(node ast.Node) bool {
				switch candidate := node.(type) {
				case *ast.Ident:
					object := pkg.TypesInfo.Defs[candidate]
					if object != nil && versionedIdentifier.MatchString(candidate.Name) {
						violations = append(violations, fmt.Sprintf("%s: identifier %s ends in a version", sourcePosition(pkg, candidate.Pos()), candidate.Name))
					}
					if constantValue, ok := object.(*types.Const); ok && isOperationConstant(pkg.PkgPath, candidate.Name) {
						violations = append(violations, commandOperationViolations(pkg, candidate.Pos(), constantValue.Val())...)
					}
				case *ast.BasicLit:
					if candidate.Kind != token.STRING || imports[candidate] {
						return true
					}
					value, err := strconv.Unquote(candidate.Value)
					if err != nil || !versionedString.MatchString(value) {
						return true
					}
					if _, allowed := allowlist[value]; !allowed && !httpAPIGenerationPath.MatchString(value) {
						violations = append(violations, fmt.Sprintf("%s: string %q carries a version segment", sourcePosition(pkg, candidate.Pos()), value))
					}
				case *ast.CallExpr:
					// A wrapper may forward a variable; its callers' constants
					// are checked where they are declared or passed.
					if operation, ok := idempotencyOperationArgument(pkg, candidate); ok && pkg.TypesInfo.Types[operation].Value != nil {
						violations = append(violations, commandOperationViolations(pkg, operation.Pos(), pkg.TypesInfo.Types[operation].Value)...)
					}
				}
				return true
			})
		}
	}
	sort.Strings(violations)
	return violations
}

// idempotencyOperationArgument returns the operation argument of a call to
// app/idempotency Require or Prepare.
func idempotencyOperationArgument(pkg *packages.Package, call *ast.CallExpr) (ast.Expr, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) < 2 {
		return nil, false
	}
	function, ok := pkg.TypesInfo.Uses[selector.Sel].(*types.Func)
	if !ok || function.Pkg() == nil || !strings.HasSuffix(function.Pkg().Path(), "/app/idempotency") ||
		(function.Name() != "Require" && function.Name() != "Prepare") {
		return nil, false
	}
	return call.Args[1], true
}

// isOperationConstant reports whether a constant names a command operation:
// a store constant named *Operation, or any constant named
// idempotencyOperation*.
func isOperationConstant(pkgPath, name string) bool {
	return strings.HasSuffix(pkgPath, "/store") && strings.HasSuffix(name, "Operation") ||
		strings.HasPrefix(name, "idempotencyOperation")
}

func commandOperationViolations(pkg *packages.Package, pos token.Pos, value constant.Value) []string {
	if value == nil || value.Kind() != constant.String {
		return []string{fmt.Sprintf("%s: operation is not a constant string", sourcePosition(pkg, pos))}
	}
	if name := constant.StringVal(value); !commandOperation.MatchString(name) {
		return []string{fmt.Sprintf("%s: operation %q is not dot-separated snake_case", sourcePosition(pkg, pos), name)}
	}
	return nil
}
