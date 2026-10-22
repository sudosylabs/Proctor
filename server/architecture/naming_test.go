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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReceiverNamesAreShortAndConsistent keeps one receiver name per type, a
// one-to-three letter abbreviation of the type, in production and test code.
func TestReceiverNamesAreShortAndConsistent(t *testing.T) {
	t.Parallel()

	type receiverUse struct {
		names map[string][]string
	}
	byType := make(map[string]*receiverUse)
	inspectServerGoFiles(t, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) == 0 {
				continue
			}
			field := function.Recv.List[0]
			if len(field.Names) == 0 || field.Names[0].Name == "_" {
				continue
			}
			name := field.Names[0].Name
			location := path + ":" + function.Name.Name
			assert.LessOrEqual(t, len(name), 3, "%s uses receiver %q; use a one-to-three letter abbreviation of the type", location, name)
			assert.NotEqual(t, "self", name, "%s uses receiver %q; use a one-to-three letter abbreviation of the type", location, name)
			assert.NotEqual(t, "this", name, "%s uses receiver %q; use a one-to-three letter abbreviation of the type", location, name)
			key := filepath.Dir(path) + "/" + file.Name.Name + "." + receiverTypeName(field.Type)
			use := byType[key]
			if use == nil {
				use = &receiverUse{names: make(map[string][]string)}
				byType[key] = use
			}
			use.names[name] = append(use.names[name], location)
		}
	})
	for key, use := range byType {
		if len(use.names) < 2 {
			continue
		}
		names := make([]string, 0, len(use.names))
		for name := range use.names {
			names = append(names, name)
		}
		sort.Strings(names)
		assert.Fail(t, fmt.Sprintf("%s mixes receiver names %v; use one name for every method", key, names))
	}
}

// idWord matches the initialism ID spelled Id inside a Go identifier, such as
// UserId, IdToken, or sessionIds.
var idWord = regexp.MustCompile(`Ids?([A-Z0-9_]|$)`)

// TestDeclaredIdentifiersSpellIDAsAnInitialism keeps the identifier initialism
// ID upper case in every declared Go name. JSON tags and SQL columns keep
// their snake_case spelling.
func TestDeclaredIdentifiersSpellIDAsAnInitialism(t *testing.T) {
	t.Parallel()

	inspectServerGoFiles(t, func(path string, file *ast.File) {
		check := func(identifier *ast.Ident) {
			if identifier != nil {
				assert.False(t, idWord.MatchString(identifier.Name), "%s declares %q; spell the initialism ID", path, identifier.Name)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch declared := node.(type) {
			case *ast.FuncDecl:
				check(declared.Name)
			case *ast.TypeSpec:
				check(declared.Name)
			case *ast.ValueSpec:
				for _, name := range declared.Names {
					check(name)
				}
			case *ast.Field:
				for _, name := range declared.Names {
					check(name)
				}
			case *ast.AssignStmt:
				if declared.Tok == token.DEFINE {
					for _, expression := range declared.Lhs {
						if name, ok := expression.(*ast.Ident); ok {
							check(name)
						}
					}
				}
			case *ast.RangeStmt:
				if declared.Tok == token.DEFINE {
					for _, expression := range []ast.Expr{declared.Key, declared.Value} {
						if name, ok := expression.(*ast.Ident); ok {
							check(name)
						}
					}
				}
			}
			return true
		})
	})
}

// TestSQLStoreAdvisoryLocksUseOneHelper keeps named advisory locks on the
// sqlstore helpers so every key shares one hash function and prefix.
func TestSQLStoreAdvisoryLocksUseOneHelper(t *testing.T) {
	t.Parallel()

	inspectProductionGoFiles(t, []string{"store/sqlstore"}, func(path string, file *ast.File) {
		if filepath.Base(path) == "advisory_lock.go" {
			return
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok {
				return true
			}
			value, isString := stringLiteral(literal)
			if isString {
				assert.NotContains(t, value, "pg_advisory", "%s spells an advisory lock query; use lockAdvisory, lockAdvisoryShared, or lockCommandIdempotency", path)
			}
			return true
		})
	})
}

// pluralEntityFile matches a file name that pluralizes a domain entity word.
var pluralEntityFile = regexp.MustCompile(`(^|_)(attempts|corrections|exams|exports|invalidations|managers|notices|resources|reviews|revisions|sittings|submissions|templates)(_|$)`)

// TestFilesAreNamedForTheSingularConcept names a file for the singular concept
// it holds, such as exam_attempt.go. Concepts whose name is plural, such as
// Exam Records or User Settings, and topic files such as metrics.go keep their
// names.
func TestFilesAreNamedForTheSingularConcept(t *testing.T) {
	t.Parallel()

	inspectServerGoFiles(t, func(path string, _ *ast.File) {
		stem := strings.TrimSuffix(filepath.Base(path), ".go")
		assert.False(t, pluralEntityFile.MatchString(stem), "%s pluralizes a domain entity; name the file for the singular concept", path)
	})
}

// TestInternalImportsUseTheirPackageName keeps one spelling for every
// repository package: an import of a github.com/sudosylabs/proctor package
// carries no alias unless the same file imports another package that declares
// the same name. A local that would shadow the package name is renamed
// instead.
func TestInternalImportsUseTheirPackageName(t *testing.T) {
	t.Parallel()

	names := repositoryPackageNames{}
	inspectServerGoFiles(t, func(path string, file *ast.File) {
		for _, violation := range importAliasViolations(path, file, names.name) {
			assert.Fail(t, violation)
		}
	})
}

func TestImportAliasRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()

	declared := map[string]string{
		"github.com/sudosylabs/proctor/server/app":       "app",
		"github.com/sudosylabs/proctor/server/app/job":   "job",
		"github.com/sudosylabs/proctor/server/app/mail":  "mail",
		"github.com/sudosylabs/proctor/packages/mail":    "mail",
		"github.com/sudosylabs/proctor/packages/vfs":     "vfs",
		"github.com/sudosylabs/proctor/server/app/exam":  "exam",
		"github.com/sudosylabs/proctor/server/app/realm": "realm",
	}
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", `package fixture

import (
	_ "github.com/sudosylabs/proctor/server/app/realm"
	jobengine "github.com/sudosylabs/proctor/server/app/job"
	app "github.com/sudosylabs/proctor/server/app"
	vfspkg "github.com/sudosylabs/proctor/packages/vfs"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	mailpkg "github.com/sudosylabs/proctor/packages/mail"
	"github.com/sudosylabs/proctor/server/app/exam"
	yaml "gopkg.in/yaml.v3"
)
`, parser.ImportsOnly)
	require.NoError(t, err)
	require.Equal(t, []string{
		`fixture.go imports github.com/sudosylabs/proctor/server/app/job as jobengine; import it as job and rename locals that would shadow it`,
		`fixture.go imports github.com/sudosylabs/proctor/server/app as app; import it as app and rename locals that would shadow it`,
		`fixture.go imports github.com/sudosylabs/proctor/packages/vfs as vfspkg; import it as vfs and rename locals that would shadow it`,
	}, importAliasViolations("fixture.go", file, func(path string) string { return declared[path] }))
}

// importAliasViolations reports each aliased repository import in file that no
// same-name import in the file forces. packageName answers an import path's
// declared package name.
func importAliasViolations(path string, file *ast.File, packageName func(string) string) []string {
	var violations []string
	imported := map[string]int{}
	for _, spec := range file.Imports {
		imported[packageName(strings.Trim(spec.Path.Value, `"`))]++
	}
	for _, spec := range file.Imports {
		importPath := strings.Trim(spec.Path.Value, `"`)
		if spec.Name == nil || spec.Name.Name == "_" || !strings.HasPrefix(importPath, repositoryModule+"/") {
			continue
		}
		name := packageName(importPath)
		if name != "" && imported[name] > 1 {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s imports %s as %s; import it as %s and rename locals that would shadow it",
			path, importPath, spec.Name.Name, name))
	}
	return violations
}

// repositoryPackageNames reads a repository package's declared name from the
// package clause of its first non-test file.
type repositoryPackageNames map[string]string

func (r repositoryPackageNames) name(importPath string) string {
	if name, ok := r[importPath]; ok {
		return name
	}
	directory := filepath.Join("..", "..", filepath.FromSlash(strings.TrimPrefix(importPath, repositoryModule+"/")))
	entries, err := os.ReadDir(directory)
	name := ""
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			file, parseErr := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, entry.Name()), nil, parser.PackageClauseOnly)
			if parseErr == nil {
				name = file.Name.Name
				break
			}
		}
	}
	r[importPath] = name
	return name
}

func receiverTypeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.StarExpr:
		return receiverTypeName(typed.X)
	case *ast.ParenExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexExpr:
		return receiverTypeName(typed.X)
	case *ast.IndexListExpr:
		return receiverTypeName(typed.X)
	case *ast.Ident:
		return typed.Name
	}
	return ""
}

// inspectServerGoFiles parses every server Go file, including tests and every
// build-tag variant.
func inspectServerGoFiles(t *testing.T, inspect func(path string, file *ast.File)) {
	t.Helper()
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != ".." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		inspect(path, file)
		return nil
	})
	require.NoError(t, err)
}
