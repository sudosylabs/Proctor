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
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testifyPackages maps the testify import paths to the only names tests use
// for them.
var testifyPackages = map[string]string{
	"github.com/stretchr/testify/require": "require",
	"github.com/stretchr/testify/assert":  "assert",
}

// TestTestsAssertThroughTestify keeps one test idiom in the testify modules:
// failures go through testify require (stop the test) or assert (continue),
// never through the testing.T failure methods.
func TestTestsAssertThroughTestify(t *testing.T) {
	t.Parallel()

	inspectRepositoryTestCode(t, func(fset *token.FileSet, path string, file *ast.File) {
		forEachTestingFunction(file, func(testing map[string]bool, body *ast.BlockStmt) {
			ast.Inspect(body, func(node ast.Node) bool {
				if _, nested := node.(*ast.FuncLit); nested {
					return false // visited with its own scope
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				receiver, ok := selector.X.(*ast.Ident)
				if !ok || !testing[receiver.Name] {
					return true
				}
				switch selector.Sel.Name {
				case "Fatal", "Fatalf", "Error", "Errorf", "Fail", "FailNow":
					assert.Fail(t, fmt.Sprintf("%s calls %s.%s; use require (stop) or assert (continue)",
						position(fset, path, call), receiver.Name, selector.Sel.Name))
				}
				return true
			})
		})
	})
}

// TestAssertionsAreNotHiddenInConditionals rejects an if statement whose only
// purpose is to fail the test, and an assertion over a compound condition
// that can be split. Each checked property gets its own assertion so a failure
// names the clause that broke.
func TestAssertionsAreNotHiddenInConditionals(t *testing.T) {
	t.Parallel()

	inspectRepositoryTestCode(t, func(fset *token.FileSet, path string, file *ast.File) {
		names := testifyNames(file)
		if len(names) == 0 {
			return
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch statement := node.(type) {
			case *ast.IfStmt:
				hidden := statement.Else == nil && onlyFails(statement.Body, names)
				assert.False(t, hidden, "%s only fails the test inside an if; write the condition as require/assert assertions",
					position(fset, path, statement))
			case *ast.CallExpr:
				function, condition := testifyCall(statement, names)
				if condition == nil {
					return true
				}
				binary, ok := unparen(condition).(*ast.BinaryExpr)
				if !ok {
					return true
				}
				splittable := function == "True" && binary.Op == token.LAND || function == "False" && binary.Op == token.LOR
				assert.False(t, splittable, "%s asserts %s over a compound condition; assert each clause separately",
					position(fset, path, statement), function)
			}
			return true
		})
	})
}

// TestTestHelpersAreNotAssertionAliases rejects helpers that only forward
// their parameters to one testify assertion, such as requireNoError; call the
// assertion directly instead.
func TestTestHelpersAreNotAssertionAliases(t *testing.T) {
	t.Parallel()

	inspectRepositoryTestCode(t, func(fset *token.FileSet, path string, file *ast.File) {
		names := testifyNames(file)
		if len(names) == 0 {
			return
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil || len(testingParameters(function.Type)) == 0 {
				continue
			}
			var statements []ast.Stmt
			for _, statement := range function.Body.List {
				if !isHelperCall(statement) {
					statements = append(statements, statement)
				}
			}
			if len(statements) != 1 {
				continue
			}
			expression, ok := statements[0].(*ast.ExprStmt)
			if !ok {
				continue
			}
			call, ok := expression.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			if name, _ := testifyCall(call, names); name == "" || !forwardsParameters(call, function.Type) {
				continue
			}
			assert.Fail(t, fmt.Sprintf("%s: %s only forwards to one assertion; call it directly",
				position(fset, path, function), function.Name.Name))
		}
	})
}

// TestRecordedCallLogsAreAssertedWhole keeps a fake's recorded call log
// (a variable or field named events, order, or calls) asserted as one exact
// sequence. A membership or subsequence check lets an extra write, effect, or
// audit failure completion after a commit pass unnoticed.
func TestRecordedCallLogsAreAssertedWhole(t *testing.T) {
	t.Parallel()

	inspectRepositoryTestCode(t, func(fset *token.FileSet, path string, file *ast.File) {
		for _, violation := range recordedCallLogViolations(fset, path, file) {
			assert.Fail(t, violation)
		}
	})
}

func TestRecordedCallLogRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()

	const source = `package fixture

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

type fake struct{ order *[]string; calls []string }

func TestFixture(t *testing.T) {
	events := []string{"authorize", "audit-begin", "store-create"}
	f := fake{order: &events}
	require.Contains(t, events, "store-create")
	require.NotContains(t, *f.order, "audit-fail")
	require.Subset(t, f.calls, []string{"store-create"})
	require.ElementsMatch(t, []string{"store-create"}, f.calls)
	require.True(t, slices.Contains(events, "store-create"))
	require.Equal(t, []string{"authorize", "audit-begin", "store-create"}, events)
	require.Contains(t, f.calls[0], "store")
	require.Len(t, f.calls, 1)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture_test.go", source, parser.SkipObjectResolution)
	require.NoError(t, err)
	require.Equal(t, []string{
		"fixture_test.go:15 asserts recorded call log events with require.Contains; assert the whole sequence with require.Equal",
		"fixture_test.go:16 asserts recorded call log f.order with require.NotContains; assert the whole sequence with require.Equal",
		"fixture_test.go:17 asserts recorded call log f.calls with require.Subset; assert the whole sequence with require.Equal",
		"fixture_test.go:18 asserts recorded call log f.calls with require.ElementsMatch; assert the whole sequence with require.Equal",
		"fixture_test.go:19 asserts recorded call log events with slices.Contains; assert the whole sequence with require.Equal",
	}, recordedCallLogViolations(fset, "fixture_test.go", file))
}

// recordedCallLogNames are the names fakes give the log of calls they record.
var recordedCallLogNames = map[string]bool{"events": true, "order": true, "calls": true}

// partialLogAssertions are the testify assertions that inspect only part of a
// slice: membership, subset, or content regardless of order.
var partialLogAssertions = map[string]bool{
	"Contains": true, "NotContains": true, "Subset": true, "NotSubset": true, "ElementsMatch": true,
}

func recordedCallLogViolations(fset *token.FileSet, path string, file *ast.File) []string {
	names := testifyNames(file)
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		function := strings.TrimSuffix(selector.Sel.Name, "f")
		var candidates []ast.Expr
		switch {
		case names[pkg.Name] && function == "ElementsMatch" && len(call.Args) > 2:
			candidates = call.Args[1:3]
		case names[pkg.Name] && partialLogAssertions[function] && len(call.Args) > 1:
			candidates = call.Args[1:2]
		case pkg.Name == "slices" && (function == "Contains" || function == "Index") && len(call.Args) > 0:
			candidates = call.Args[:1]
		}
		for _, candidate := range candidates {
			if name := recordedCallLogName(candidate); name != "" {
				violations = append(violations, fmt.Sprintf("%s asserts recorded call log %s with %s.%s; assert the whole sequence with require.Equal",
					position(fset, path, call), name, pkg.Name, selector.Sel.Name))
				break
			}
		}
		return true
	})
	return violations
}

// recordedCallLogName returns the spelled name of a recorded call log
// expression, or "" when the expression is something else, such as one
// element of the log.
func recordedCallLogName(expression ast.Expr) string {
	expression = unparen(expression)
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
	}
	if call, ok := expression.(*ast.CallExpr); ok && len(call.Args) == 0 {
		if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector && selector.Sel.Name == "snapshot" {
			expression = selector.X
		}
	}
	var last string
	switch typed := expression.(type) {
	case *ast.Ident:
		last = typed.Name
	case *ast.SelectorExpr:
		last = typed.Sel.Name
	default:
		return ""
	}
	if !recordedCallLogNames[last] {
		return ""
	}
	return types.ExprString(expression)
}

// TestNamedCaseTablesRunAsSubtests keeps a table of named cases running each
// case as its own subtest, so a failure names its case and one case can run
// alone. A named case table is a slice or map literal (ranged directly or
// through a local variable) of struct cases with a name, desc, description,
// title, label, or scenario field, or a map from a string to a struct or a
// function. The loop body must call Run.
func TestNamedCaseTablesRunAsSubtests(t *testing.T) {
	t.Parallel()

	inspectRepositoryTestCode(t, func(fset *token.FileSet, path string, file *ast.File) {
		for _, violation := range namedCaseTableViolations(fset, path, file) {
			assert.Fail(t, violation)
		}
	})
}

func TestNamedCaseTableRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()

	const source = `package fixture

import "testing"

func TestFixture(t *testing.T) {
	tests := []struct {
		name string
		in   int
	}{{name: "one", in: 1}}
	for _, test := range tests {
		_ = test.in
	}
	for name, run := range map[string]func() error{"run": nil} {
		_, _ = name, run
	}
	for _, test := range map[string]struct{ in int }{"one": {in: 1}} {
		_ = test
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { _ = test.in })
	}
	for _, value := range []struct{ in, out int }{{1, 2}} {
		_ = value
	}
	for key, value := range map[string]string{"a": "b"} {
		_, _ = key, value
	}
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture_test.go", source, parser.SkipObjectResolution)
	require.NoError(t, err)
	require.Equal(t, []string{
		"fixture_test.go:10 ranges over named cases (field name) without t.Run; run each case as a named subtest",
		"fixture_test.go:13 ranges over named cases (map key) without t.Run; run each case as a named subtest",
		"fixture_test.go:16 ranges over named cases (map key) without t.Run; run each case as a named subtest",
	}, namedCaseTableViolations(fset, "fixture_test.go", file))
}

// caseNameFields are the struct fields that name a table case.
var caseNameFields = map[string]bool{
	"name": true, "desc": true, "description": true, "title": true, "label": true, "scenario": true,
}

func namedCaseTableViolations(fset *token.FileSet, path string, file *ast.File) []string {
	var violations []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		tables := make(map[string]*ast.CompositeLit)
		ast.Inspect(function.Body, func(node ast.Node) bool {
			switch statement := node.(type) {
			case *ast.AssignStmt:
				for index, left := range statement.Lhs {
					identifier, isIdent := left.(*ast.Ident)
					if !isIdent || index >= len(statement.Rhs) {
						continue
					}
					if literal, isLiteral := statement.Rhs[index].(*ast.CompositeLit); isLiteral {
						tables[identifier.Name] = literal
					}
				}
			case *ast.ValueSpec:
				for index, identifier := range statement.Names {
					if index >= len(statement.Values) {
						continue
					}
					if literal, isLiteral := statement.Values[index].(*ast.CompositeLit); isLiteral {
						tables[identifier.Name] = literal
					}
				}
			case *ast.RangeStmt:
				var table *ast.CompositeLit
				switch ranged := unparen(statement.X).(type) {
				case *ast.CompositeLit:
					table = ranged
				case *ast.Ident:
					table = tables[ranged.Name]
				}
				if table == nil {
					return true
				}
				naming := caseTableNaming(table)
				if naming == "" || callsRun(statement.Body) {
					return true
				}
				violations = append(violations, fmt.Sprintf("%s ranges over named cases (%s) without t.Run; run each case as a named subtest",
					position(fset, path, statement), naming))
			}
			return true
		})
	}
	return violations
}

// caseTableNaming describes how a table literal names its cases, or returns
// "" when it is not a table of named cases.
func caseTableNaming(table *ast.CompositeLit) string {
	var element ast.Expr
	stringKeys := false
	switch typed := table.Type.(type) {
	case *ast.ArrayType:
		element = typed.Elt
	case *ast.MapType:
		element = typed.Value
		key, isIdent := typed.Key.(*ast.Ident)
		stringKeys = isIdent && key.Name == "string"
	default:
		return ""
	}
	if star, isStar := element.(*ast.StarExpr); isStar {
		element = star.X
	}
	switch typed := element.(type) {
	case *ast.StructType:
		for _, field := range typed.Fields.List {
			for _, name := range field.Names {
				if caseNameFields[strings.ToLower(name.Name)] {
					return "field " + name.Name
				}
			}
		}
		if stringKeys {
			return "map key"
		}
	case *ast.FuncType:
		if stringKeys {
			return "map key"
		}
	}
	return ""
}

// callsRun reports whether a loop body starts a subtest or sub-benchmark.
func callsRun(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return !found
		}
		if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector && selector.Sel.Name == "Run" && len(call.Args) == 2 {
			found = true
		}
		return !found
	})
	return found
}

// testifyModules are the product modules whose tests use testify.
// packages/mail is excluded on purpose: it keeps no third-party Go
// dependencies at all, so its tests use the testing package alone.
var testifyModules = []string{"server", "packages/cache", "packages/vfs"}

// inspectRepositoryTestCode parses every Go file of the testify modules that
// can hold test code: _test.go files and files that import "testing".
func inspectRepositoryTestCode(t *testing.T, inspect func(fset *token.FileSet, path string, file *ast.File)) {
	t.Helper()
	repositoryRoot := filepath.Clean("../..")
	for _, module := range testifyModules {
		root := filepath.Join(repositoryRoot, filepath.FromSlash(module))
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata" || entry.Name() == "node_modules") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			if !strings.HasSuffix(path, "_test.go") && !importsPath(file, "testing") {
				return nil
			}
			relative, err := filepath.Rel(repositoryRoot, path)
			if err != nil {
				return err
			}
			inspect(fset, filepath.ToSlash(relative), file)
			return nil
		})
		require.NoError(t, err)
	}
}

func position(fset *token.FileSet, path string, node ast.Node) string {
	return path + ":" + strconv.Itoa(fset.Position(node.Pos()).Line)
}

func importsPath(file *ast.File, importPath string) bool {
	for _, spec := range file.Imports {
		if path, err := strconv.Unquote(spec.Path.Value); err == nil && path == importPath {
			return true
		}
	}
	return false
}

// testifyNames returns the local names of the imported testify packages.
func testifyNames(file *ast.File) map[string]bool {
	names := make(map[string]bool)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name, ok := testifyPackages[path]
		if !ok {
			continue
		}
		if spec.Name != nil {
			name = spec.Name.Name
		}
		names[name] = true
	}
	return names
}

// testifyCall returns the assertion name and, for True and False, the asserted
// condition.
func testifyCall(call *ast.CallExpr, names map[string]bool) (string, ast.Expr) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", nil
	}
	pkg, ok := selector.X.(*ast.Ident)
	if !ok || !names[pkg.Name] {
		return "", nil
	}
	name := strings.TrimSuffix(selector.Sel.Name, "f")
	if (name == "True" || name == "False") && len(call.Args) >= 2 {
		return name, call.Args[1]
	}
	return name, nil
}

// onlyFails reports whether a block does nothing but fail the test, optionally
// leaving the enclosing function or loop afterwards.
func onlyFails(body *ast.BlockStmt, names map[string]bool) bool {
	statements := body.List
	if n := len(statements); n == 2 {
		switch last := statements[1].(type) {
		case *ast.ReturnStmt:
			if len(last.Results) != 0 {
				return false
			}
		case *ast.BranchStmt:
			if last.Tok != token.CONTINUE {
				return false
			}
		default:
			return false
		}
		statements = statements[:1]
	}
	if len(statements) != 1 {
		return false
	}
	expression, ok := statements[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expression.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	name, _ := testifyCall(call, names)
	return name == "Fail" || name == "FailNow"
}

func unparen(expression ast.Expr) ast.Expr {
	for {
		paren, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = paren.X
	}
}

func isHelperCall(statement ast.Stmt) bool {
	expression, ok := statement.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expression.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Helper" && len(call.Args) == 0
}

// forwardsParameters reports whether every argument of call is a parameter of
// the function, unchanged.
func forwardsParameters(call *ast.CallExpr, function *ast.FuncType) bool {
	parameters := make(map[string]bool)
	for _, field := range function.Params.List {
		for _, name := range field.Names {
			parameters[name.Name] = true
		}
	}
	for _, argument := range call.Args {
		identifier, ok := argument.(*ast.Ident)
		if !ok || !parameters[identifier.Name] {
			return false
		}
	}
	return true
}

// isTestingType reports whether a parameter type expression names a testing
// handle: *testing.T, *testing.B, *testing.F, or testing.TB.
func isTestingType(expression ast.Expr) bool {
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
		selector, ok := expression.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := selector.X.(*ast.Ident)
		return ok && pkg.Name == "testing" && (selector.Sel.Name == "T" || selector.Sel.Name == "B" || selector.Sel.Name == "F")
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && selector.Sel.Name == "TB"
}

func testingParameters(function *ast.FuncType) []string {
	var names []string
	if function.Params == nil {
		return nil
	}
	for _, field := range function.Params.List {
		if !isTestingType(field.Type) {
			continue
		}
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

// forEachTestingFunction visits every function body together with the names
// that refer to a testing handle inside it, including captured handles. Nested
// function literals are visited separately with their own scope.
func forEachTestingFunction(file *ast.File, visit func(testing map[string]bool, body *ast.BlockStmt)) {
	var walk func(node ast.Node, inherited map[string]bool)
	walk = func(node ast.Node, inherited map[string]bool) {
		ast.Inspect(node, func(child ast.Node) bool {
			var functionType *ast.FuncType
			var body *ast.BlockStmt
			switch function := child.(type) {
			case *ast.FuncDecl:
				functionType, body = function.Type, function.Body
			case *ast.FuncLit:
				functionType, body = function.Type, function.Body
			default:
				return true
			}
			if body == nil {
				return false
			}
			scope := make(map[string]bool, len(inherited))
			for name := range inherited {
				scope[name] = true
			}
			for _, field := range functionType.Params.List {
				for _, name := range field.Names {
					delete(scope, name.Name)
				}
			}
			for _, name := range testingParameters(functionType) {
				scope[name] = true
			}
			if len(scope) > 0 {
				visit(scope, body)
			}
			for _, statement := range body.List {
				walk(statement, scope)
			}
			return false
		})
	}
	walk(file, nil)
}
