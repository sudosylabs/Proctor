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
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// forwardingNameAllowlist names the only application declarations that give a
// type of another package a second name, with the reason each must exist.
var forwardingNameAllowlist = map[string]string{
	"app.examAuthoringService":        embeddedServiceName,
	"app.examPublicationService":      embeddedServiceName,
	"app.examRecordsService":          embeddedServiceName,
	"app.examExportsService":          embeddedServiceName,
	"app.examSittingService":          embeddedServiceName,
	"app.examAttemptService":          embeddedServiceName,
	"app.examReviewService":           embeddedServiceName,
	"app.examResourceService":         embeddedServiceName,
	"app.examCorrectionService":       embeddedServiceName,
	"app.examStarterWorkspaceService": embeddedServiceName,
}

const embeddedServiceName = "App embeds this Exam service; Go names an embedded field after its type, and several child services are all named Service"

// TestApplicationDeclaresNoForwardingNames keeps one name for every type and
// constant: no application package (app or a child module) declares an alias
// of another package's type, or an exported constant or variable that only
// restates another package's identifier. Consumers name the owning package
// directly.
func TestApplicationDeclaresNoForwardingNames(t *testing.T) {
	t.Parallel()
	for _, violation := range forwardingNameViolations(typedServerPackages(t), serverModule, forwardingNameAllowlist) {
		assert.Fail(t, violation)
	}
}

func TestForwardingNameRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"store/store.go": "package store\n\ntype Summary struct{}\n\nconst Limit = 3\n",
		"app/child/child.go": `package child

type Service struct{}

type View struct{}
`,
		"app/app.go": `package app

import (
	"fixture/app/child"
	"fixture/store"
)

type childService = child.Service

type ChildView = child.View

type ExamSummary = store.Summary

type localCommand struct{}

type LocalAlias = localCommand

const MaximumItems = store.Limit

const localLimit = store.Limit

var NewView = child.View{}
`,
	})
	require.Equal(t, []string{
		"app.go:10: fixture/app.ChildView is an alias of fixture/app/child.View; name fixture/app/child.View directly",
		"app.go:12: fixture/app.ExamSummary is an alias of fixture/store.Summary; name fixture/store.Summary directly",
		"app.go:18: fixture/app.MaximumItems restates store.Limit; name store.Limit directly",
	}, forwardingNameViolations(loaded, "fixture", map[string]string{"app.childService": "embedded field name"}))
}

func forwardingNameViolations(loaded []*packages.Package, module string, allowed map[string]string) []string {
	var violations []string
	for _, pkg := range loaded {
		if pkg.PkgPath != module+"/app" && !strings.HasPrefix(pkg.PkgPath, module+"/app/") {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			alias, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || !alias.IsAlias() {
				continue
			}
			named, ok := types.Unalias(alias.Type()).(*types.Named)
			if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg() == pkg.Types {
				continue
			}
			if _, ok := allowed[pkg.Types.Name()+"."+name]; ok {
				continue
			}
			target := named.Obj().Pkg().Path() + "." + named.Obj().Name()
			violations = append(violations, fmt.Sprintf("%s: %s.%s is an alias of %s; name %s directly",
				sourcePosition(pkg, alias.Pos()), pkg.PkgPath, name, target, target))
		}
		for _, file := range pkg.Syntax {
			for _, declaration := range file.Decls {
				generic, ok := declaration.(*ast.GenDecl)
				if !ok || (generic.Tok != token.CONST && generic.Tok != token.VAR) {
					continue
				}
				for _, specification := range generic.Specs {
					value := specification.(*ast.ValueSpec)
					for index, valueName := range value.Names {
						if !valueName.IsExported() || index >= len(value.Values) {
							continue
						}
						selector, ok := value.Values[index].(*ast.SelectorExpr)
						if !ok {
							continue
						}
						qualifier, ok := selector.X.(*ast.Ident)
						if !ok {
							continue
						}
						if _, ok := pkg.TypesInfo.Uses[qualifier].(*types.PkgName); !ok {
							continue
						}
						restated := qualifier.Name + "." + selector.Sel.Name
						violations = append(violations, fmt.Sprintf("%s: %s.%s restates %s; name %s directly",
							sourcePosition(pkg, valueName.Pos()), pkg.PkgPath, valueName.Name, restated, restated))
					}
				}
			}
		}
	}
	sort.Strings(violations)
	return violations
}

// TestTransportsNameOnlyStoreResultTypes lets HTTP and WebSocket name the
// query-shaped result types that use cases return from store, while keeping
// persistence out of reach: a transport never names a Store contract
// (interface), a store function, or a store variable.
func TestTransportsNameOnlyStoreResultTypes(t *testing.T) {
	t.Parallel()
	for _, violation := range transportStoreViolations(typedServerPackages(t), serverModule) {
		assert.Fail(t, violation)
	}
}

func TestTransportStoreRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"store/store.go": `package store

type Summary struct{ Count int }

func (s Summary) Empty() bool { return s.Count == 0 }

type ExamStore interface{ Summary() Summary }

const Limit = 3

var Default = Summary{}

func NewSummary() Summary { return Summary{} }
`,
		"httpapi/handler.go": `package httpapi

import "fixture/store"

func present(summary store.Summary) (bool, int) { return summary.Empty(), store.Limit }

func read(exams store.ExamStore) store.Summary { return exams.Summary() }

func fallback() store.Summary { return store.Default }

func build() store.Summary { return store.NewSummary() }
`,
	})
	require.Equal(t, []string{
		"handler.go:11: fixture/httpapi names store function fixture/store.NewSummary; transports use application use cases, never persistence",
		"handler.go:7: fixture/httpapi names store contract fixture/store.ExamStore; transports use application use cases, never persistence",
		"handler.go:7: fixture/httpapi names store contract method Summary; transports use application use cases, never persistence",
		"handler.go:9: fixture/httpapi names store variable fixture/store.Default; transports use application use cases, never persistence",
	}, transportStoreViolations(loaded, "fixture"))
}

func transportStoreViolations(loaded []*packages.Package, module string) []string {
	storePath := module + "/store"
	var violations []string
	for _, pkg := range loaded {
		if !packageInSubtree(pkg.PkgPath, module+"/httpapi") && !packageInSubtree(pkg.PkgPath, module+"/websocket") {
			continue
		}
		for identifier, object := range pkg.TypesInfo.Uses {
			if object.Pkg() == nil || object.Pkg().Path() != storePath {
				continue
			}
			kind := ""
			switch typed := object.(type) {
			case *types.TypeName:
				if types.IsInterface(typed.Type()) {
					kind = "contract " + storePath + "." + typed.Name()
				}
			case *types.Func:
				signature, _ := typed.Type().(*types.Signature)
				if signature == nil || signature.Recv() == nil {
					kind = "function " + storePath + "." + typed.Name()
				} else if types.IsInterface(signature.Recv().Type()) {
					kind = "contract method " + typed.Name()
				}
			case *types.Var:
				if !typed.IsField() {
					kind = "variable " + storePath + "." + typed.Name()
				}
			}
			if kind == "" {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s: %s names store %s; transports use application use cases, never persistence",
				sourcePosition(pkg, identifier.Pos()), pkg.PkgPath, kind))
		}
	}
	sort.Strings(violations)
	return violations
}

func packageInSubtree(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}
