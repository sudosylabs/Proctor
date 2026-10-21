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

// TestAdaptersDoNotWrapAdapters keeps every adapter a translation of one
// concrete service, Store, or infrastructure value. An adapter is a named
// struct called adapter or *Adapter in production code; it wraps another
// adapter when a field holds one (directly, through a pointer, slice, array,
// or map, or through an interface whose only production implementation is an
// adapter) or when a method delegates through an inline composite literal of
// one. A factory method that builds and returns another adapter is not
// wrapping.
func TestAdaptersDoNotWrapAdapters(t *testing.T) {
	t.Parallel()
	for _, violation := range adapterWrappingViolations(typedServerPackages(t)) {
		assert.Fail(t, violation)
	}
}

func TestAdapterWrappingRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"app/adapters.go": `package app

type service struct{}

func (service) Do() error { return nil }

type doer interface{ Do() error }

type mailAdapter struct{ service service }

func (a mailAdapter) Do() error { return a.service.Do() }

type sender interface{ Send() error }

type senderAdapter struct{}

func (senderAdapter) Send() error { return nil }

type fieldAdapter struct{ inner *mailAdapter }

type sliceAdapter struct{ inner map[string][]mailAdapter }

type portAdapter struct{ inner sender }

type embeddedAdapter struct{ mailAdapter }

type substitutedAdapter struct{ inner doer }

type inlineAdapter struct{ service service }

func (a inlineAdapter) Do() error { return (mailAdapter{service: a.service}).Do() }

type pointerInlineAdapter struct{ service service }

func (a pointerInlineAdapter) Do() error { return (&mailAdapter{service: a.service}).Do() }

type factoryAdapter struct{ service service }

func (a factoryAdapter) Mail() mailAdapter { return mailAdapter{service: a.service} }
`,
	})
	expected := []string{
		"adapters.go:19: fixture/app.fieldAdapter holds adapter fixture/app.mailAdapter in field inner",
		"adapters.go:21: fixture/app.sliceAdapter holds adapter fixture/app.mailAdapter in field inner",
		"adapters.go:23: fixture/app.portAdapter holds fixture/app.sender, whose only production implementation is adapter fixture/app.senderAdapter, in field inner",
		"adapters.go:25: fixture/app.embeddedAdapter holds adapter fixture/app.mailAdapter in embedded field mailAdapter",
		"adapters.go:31: fixture/app.inlineAdapter.Do delegates to an inline fixture/app.mailAdapter literal",
		"adapters.go:35: fixture/app.pointerInlineAdapter.Do delegates to an inline fixture/app.mailAdapter literal",
	}
	require.Equal(t, expected, adapterWrappingViolations(loaded))
}

func adapterWrappingViolations(loaded []*packages.Package) []string {
	adapters := make(map[*types.TypeName]bool)
	var implementations []types.Type
	var interfaces []*types.Named
	for _, pkg := range loaded {
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			typeName, ok := scope.Lookup(name).(*types.TypeName)
			if !ok || typeName.IsAlias() {
				continue
			}
			named, ok := typeName.Type().(*types.Named)
			if !ok || named.TypeParams().Len() > 0 {
				continue
			}
			switch underlying := named.Underlying().(type) {
			case *types.Interface:
				if underlying.NumMethods() > 0 {
					interfaces = append(interfaces, named)
				}
			case *types.Struct:
				if isAdapterName(name) {
					adapters[typeName] = true
				}
				implementations = append(implementations, named)
			default:
				implementations = append(implementations, named)
			}
		}
	}

	// adapterPorts maps an interface to the adapter that is its only
	// production implementation.
	adapterPorts := make(map[*types.TypeName]*types.TypeName)
	for _, port := range interfaces {
		iface, _ := port.Underlying().(*types.Interface)
		var implementers []*types.TypeName
		for _, candidate := range implementations {
			if types.Implements(candidate, iface) || types.Implements(types.NewPointer(candidate), iface) {
				implementers = append(implementers, candidate.(*types.Named).Obj())
				if len(implementers) > 1 {
					break
				}
			}
		}
		if len(implementers) == 1 && adapters[implementers[0]] {
			adapterPorts[port.Obj()] = implementers[0]
		}
	}

	var violations []string
	for _, pkg := range loaded {
		for _, file := range pkg.Syntax {
			for _, declaration := range file.Decls {
				switch typed := declaration.(type) {
				case *ast.GenDecl:
					violations = append(violations, adapterFieldViolations(pkg, typed, adapters, adapterPorts)...)
				case *ast.FuncDecl:
					violations = append(violations, adapterInlineViolations(pkg, typed, adapters)...)
				}
			}
		}
	}
	sort.Strings(violations)
	return violations
}

func isAdapterName(name string) bool {
	return name == "adapter" || strings.HasSuffix(name, "Adapter")
}

func adapterFieldViolations(pkg *packages.Package, declaration *ast.GenDecl, adapters map[*types.TypeName]bool, adapterPorts map[*types.TypeName]*types.TypeName) []string {
	var violations []string
	for _, spec := range declaration.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		owner, ok := pkg.TypesInfo.Defs[typeSpec.Name].(*types.TypeName)
		if !ok || !adapters[owner] {
			continue
		}
		structure, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			continue
		}
		for _, field := range structure.Fields.List {
			fieldName := "embedded field "
			if len(field.Names) > 0 {
				fieldName = "field " + field.Names[0].Name
			}
			for _, held := range heldNamedTypes(pkg.TypesInfo.TypeOf(field.Type)) {
				switch {
				case held == owner:
				case adapters[held]:
					if len(field.Names) == 0 {
						fieldName += held.Name()
					}
					violations = append(violations, fmt.Sprintf("%s: %s holds adapter %s in %s",
						sourcePosition(pkg, field.Pos()), qualifiedTypeName(owner), qualifiedTypeName(held), fieldName))
				case adapterPorts[held] != nil && adapterPorts[held] != owner:
					if len(field.Names) == 0 {
						fieldName += held.Name()
					}
					violations = append(violations, fmt.Sprintf("%s: %s holds %s, whose only production implementation is adapter %s, in %s",
						sourcePosition(pkg, field.Pos()), qualifiedTypeName(owner), qualifiedTypeName(held), qualifiedTypeName(adapterPorts[held]), fieldName))
				}
			}
		}
	}
	return violations
}

// heldNamedTypes returns the named types a field holds once pointers, slices,
// arrays, maps, and channels are stripped.
func heldNamedTypes(goType types.Type) []*types.TypeName {
	if goType == nil {
		return nil
	}
	switch typed := types.Unalias(goType).(type) {
	case *types.Named:
		return []*types.TypeName{typed.Origin().Obj()}
	case *types.Pointer:
		return heldNamedTypes(typed.Elem())
	case *types.Slice:
		return heldNamedTypes(typed.Elem())
	case *types.Array:
		return heldNamedTypes(typed.Elem())
	case *types.Chan:
		return heldNamedTypes(typed.Elem())
	case *types.Map:
		return append(heldNamedTypes(typed.Key()), heldNamedTypes(typed.Elem())...)
	}
	return nil
}

func adapterInlineViolations(pkg *packages.Package, function *ast.FuncDecl, adapters map[*types.TypeName]bool) []string {
	if function.Recv == nil || len(function.Recv.List) == 0 || function.Body == nil {
		return nil
	}
	held := heldNamedTypes(pkg.TypesInfo.TypeOf(function.Recv.List[0].Type))
	if len(held) != 1 || !adapters[held[0]] {
		return nil
	}
	owner := held[0]
	var violations []string
	ast.Inspect(function.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		operand := unparen(selector.X)
		if unary, isUnary := operand.(*ast.UnaryExpr); isUnary {
			operand = unparen(unary.X)
		}
		literal, ok := operand.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, delegate := range heldNamedTypes(pkg.TypesInfo.TypeOf(literal)) {
			if delegate != owner && adapters[delegate] {
				violations = append(violations, fmt.Sprintf("%s: %s.%s delegates to an inline %s literal",
					sourcePosition(pkg, selector.Pos()), qualifiedTypeName(owner), function.Name.Name, qualifiedTypeName(delegate)))
			}
		}
		return true
	})
	return violations
}

func qualifiedTypeName(typeName *types.TypeName) string {
	if typeName.Pkg() == nil {
		return typeName.Name()
	}
	return typeName.Pkg().Path() + "." + typeName.Name()
}
