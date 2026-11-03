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
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// maxProductionFunctionLines bounds one production function declaration, from
// its func keyword to its closing brace, function literals included. A longer
// use case or Store operation reads as a list of named steps instead.
const maxProductionFunctionLines = 120

// longFunctionAllowlist names the production functions still over the bound,
// keyed by module-relative path and receiver-qualified name, each with the
// reason it has not been split. The list only shrinks: an entry whose function
// is back within the bound, or gone, fails the test until it is removed.
var longFunctionAllowlist = map[string]string{
	"platform/externalauth/oidc/provider.go:Provider.Complete":                  "platform adapter outside the function-length pass; split it with its next change",
	"platform/externalauth/cas/provider.go:Provider.Complete":                   "platform adapter outside the function-length pass; split it with its next change",
	"store/sqlstore/user_token_store.go:SQLUserTokenStore.ConsumePasswordReset": "grew with the password history and policy work; split it with the User token store's next change",
	"store/sqlstore/user_token_store.go:SQLUserTokenStore.ChangeEmail":          "grew with the email-change undo work; split it with the User token store's next change",
	"cmd/ptool/devseed/dataset.go:runner.populate":                              "developer seeding command outside the function-length pass",
	"cmd/ptool/devseed/seed.go:Run":                                             "developer seeding command outside the function-length pass",
	"cmd/mailpreview/main.go:run":                                               "developer mail preview command outside the function-length pass",
}

// TestProductionFunctionsStayShort keeps every production function declaration
// within maxProductionFunctionLines, apart from the explicit allowlist.
func TestProductionFunctionsStayShort(t *testing.T) {
	t.Parallel()

	lengths := productionFunctionLengths(t, "..")
	violations := longFunctionViolations(lengths, maxProductionFunctionLines, longFunctionAllowlist)
	require.Empty(t, violations, "split each function into named private steps of at most %d lines", maxProductionFunctionLines)
}

func TestFunctionLengthRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()

	source := "package sample\n\nfunc short() {}\n\nfunc (s *service) long() {\n" + strings.Repeat("\t_ = 1\n", 5) + "}\n"
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "sample.go", source, parser.SkipObjectResolution)
	require.NoError(t, err)
	lengths := map[string]int{}
	collectFunctionLengths(fileSet, "sample.go", file, lengths)
	require.Equal(t, map[string]int{"sample.go:short": 1, "sample.go:service.long": 7}, lengths)

	require.Equal(t, []string{"sample.go:service.long has 7 lines"},
		longFunctionViolations(lengths, 6, map[string]string{}))
	require.Empty(t, longFunctionViolations(lengths, 6, map[string]string{"sample.go:service.long": "reason"}))
	require.Equal(t, []string{"sample.go:service.long is allowlisted but has 7 lines; remove the entry"},
		longFunctionViolations(lengths, 7, map[string]string{"sample.go:service.long": "reason"}))
	require.Equal(t, []string{"sample.go:gone is allowlisted but does not exist; remove the entry"},
		longFunctionViolations(lengths, 7, map[string]string{"sample.go:gone": "reason"}))
}

// productionFunctionLengths measures every function declaration in the
// module's non-test, non-generated Go files outside test-support packages.
func productionFunctionLengths(t *testing.T, root string) map[string]int {
	t.Helper()
	lengths := map[string]int{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" ||
				relative == "testlib" || relative == "store/storetest") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if ast.IsGenerated(file) {
			return nil
		}
		collectFunctionLengths(fileSet, relative, file, lengths)
		return nil
	})
	require.NoError(t, err)
	return lengths
}

// collectFunctionLengths records each function declaration's line span under
// its path and receiver-qualified name.
func collectFunctionLengths(fileSet *token.FileSet, path string, file *ast.File, lengths map[string]int) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		name := function.Name.Name
		if function.Recv != nil && len(function.Recv.List) > 0 {
			name = receiverTypeName(function.Recv.List[0].Type) + "." + name
		}
		lines := fileSet.Position(function.End()).Line - fileSet.Position(function.Pos()).Line + 1
		lengths[path+":"+name] = lines
	}
}

// longFunctionViolations reports functions over the bound that the allowlist
// does not name, and allowlist entries that no longer need to exist.
func longFunctionViolations(lengths map[string]int, bound int, allowlist map[string]string) []string {
	var violations []string
	for key, lines := range lengths {
		if _, allowed := allowlist[key]; lines > bound && !allowed {
			violations = append(violations, fmt.Sprintf("%s has %d lines", key, lines))
		}
	}
	for key := range allowlist {
		lines, exists := lengths[key]
		switch {
		case !exists:
			violations = append(violations, key+" is allowlisted but does not exist; remove the entry")
		case lines <= bound:
			violations = append(violations, fmt.Sprintf("%s is allowlisted but has %d lines; remove the entry", key, lines))
		}
	}
	sort.Strings(violations)
	return violations
}
