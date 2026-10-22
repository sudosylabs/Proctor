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
// concrete service, Store, or infrastructure value. An adapter is recognized
// by its shape, whatever its name: a production struct that implements a port
// declared in the module and forwards most of its methods to one field it
// holds (see adapterShapes). A struct named adapter or *Adapter is held to the
// rule as well. An adapter wraps another adapter when a field holds one
// (directly, through a pointer, slice, array, map, or channel, or through an
// interface whose production implementations, other than the holder itself,
// are all adapters) or when a method delegates through an inline composite
// literal of one. A factory method that builds and returns another adapter is
// not wrapping.
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
		"app/shapes.go": `package app

type client struct{}

func (client) Transmit() error { return nil }

func (client) Ping() error { return nil }

type mailer interface{ Post() error }

type smtpSender struct{ client *client }

func (s smtpSender) Post() error { return s.client.Transmit() }

type measuredSender struct {
	next  smtpSender
	count int
}

func (m *measuredSender) Post() error {
	m.count++
	return m.next.Post()
}

type retryingSender struct{ next mailer }

func (r retryingSender) Post() error { return r.next.Post() }

type senderFactory struct{ client *client }

func (f senderFactory) Sender() smtpSender { return smtpSender{client: f.client} }

type looping struct{ next mailer }

func (l looping) Deliver() error {
	for range 3 {
		if err := l.next.Post(); err != nil {
			return err
		}
	}
	return nil
}

type notifier struct {
	mail  mailer
	ready bool
}

func (n notifier) Notify() error {
	if !n.ready {
		return nil
	}
	if err := n.mail.Post(); err != nil {
		return err
	}
	n.ready = false
	return nil
}
`,
	})
	expected := []string{
		"adapters.go:19: fixture/app.fieldAdapter holds adapter fixture/app.mailAdapter in field inner",
		"adapters.go:21: fixture/app.sliceAdapter holds adapter fixture/app.mailAdapter in field inner",
		"adapters.go:23: fixture/app.portAdapter holds fixture/app.sender, whose only production implementation is adapter fixture/app.senderAdapter, in field inner",
		"adapters.go:25: fixture/app.embeddedAdapter holds adapter fixture/app.mailAdapter in embedded field mailAdapter",
		"adapters.go:31: fixture/app.inlineAdapter.Do delegates to an inline fixture/app.mailAdapter literal",
		"adapters.go:35: fixture/app.pointerInlineAdapter.Do delegates to an inline fixture/app.mailAdapter literal",
		"shapes.go:16: fixture/app.measuredSender holds adapter fixture/app.smtpSender in field next",
		"shapes.go:25: fixture/app.retryingSender holds fixture/app.mailer, whose production implementations are all adapters (fixture/app.measuredSender, fixture/app.smtpSender), in field next",
	}
	require.Equal(t, expected, adapterWrappingViolations(loaded))
}

func adapterWrappingViolations(loaded []*packages.Package) []string {
	var structs, implementations []*types.Named
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
				structs = append(structs, named)
				implementations = append(implementations, named)
			default:
				implementations = append(implementations, named)
			}
		}
	}

	// portImplementers maps an interface to its production implementations.
	portImplementers := make(map[*types.TypeName][]*types.TypeName)
	implementsPort := make(map[*types.TypeName]bool)
	for _, port := range interfaces {
		iface, _ := port.Underlying().(*types.Interface)
		for _, candidate := range implementations {
			if types.Implements(candidate, iface) || types.Implements(types.NewPointer(candidate), iface) {
				portImplementers[port.Obj()] = append(portImplementers[port.Obj()], candidate.Obj())
				implementsPort[candidate.Obj()] = true
			}
		}
	}

	shapes := adapterShapes(loaded)
	adapters := make(map[*types.TypeName]bool)
	for _, structure := range structs {
		typeName := structure.Obj()
		if isAdapterName(typeName.Name()) || implementsPort[typeName] && shapes[typeName].forwardsToOneField() {
			adapters[typeName] = true
		}
	}

	var violations []string
	for _, pkg := range loaded {
		for _, file := range pkg.Syntax {
			for _, declaration := range file.Decls {
				switch typed := declaration.(type) {
				case *ast.GenDecl:
					violations = append(violations, adapterFieldViolations(pkg, typed, adapters, portImplementers)...)
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

// adapterShape counts a struct's methods and, for each field it holds, the
// methods that forward to that field.
type adapterShape struct {
	methods  int
	forwards map[string]int
}

// forwardsToOneField reports whether more than half of the methods forward to
// the same held field.
func (s *adapterShape) forwardsToOneField() bool {
	if s == nil {
		return false
	}
	for _, count := range s.forwards {
		if 2*count > s.methods {
			return true
		}
	}
	return false
}

// adapterShapes measures every production struct with methods. A method
// forwards to a field when it has at most three statements, none of them a
// loop, switch, or select; builds no struct value of its own package (a
// factory, not a forwarder); and calls a method on, calls, or passes on that
// field of its receiver.
func adapterShapes(loaded []*packages.Package) map[*types.TypeName]*adapterShape {
	shapes := make(map[*types.TypeName]*adapterShape)
	for _, pkg := range loaded {
		for _, file := range pkg.Syntax {
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || function.Recv == nil || len(function.Recv.List) == 0 || function.Body == nil {
					continue
				}
				held := heldNamedTypes(pkg.TypesInfo.TypeOf(function.Recv.List[0].Type))
				if len(held) != 1 {
					continue
				}
				if _, isStruct := held[0].Type().Underlying().(*types.Struct); !isStruct {
					continue
				}
				shape := shapes[held[0]]
				if shape == nil {
					shape = &adapterShape{forwards: make(map[string]int)}
					shapes[held[0]] = shape
				}
				shape.methods++
				for field := range forwardedFields(pkg, function) {
					shape.forwards[field]++
				}
			}
		}
	}
	return shapes
}

// forwardedFields returns the receiver fields a forwarding method delegates
// to, or nothing when the method is not a forwarder.
func forwardedFields(pkg *packages.Package, function *ast.FuncDecl) map[string]bool {
	receiver := function.Recv.List[0].Names
	if len(receiver) == 0 || len(function.Body.List) > 3 {
		return nil
	}
	for _, statement := range function.Body.List {
		switch statement.(type) {
		case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			return nil
		}
	}
	fields := make(map[string]bool)
	receiverField := func(expression ast.Expr) {
		selector, ok := unparen(expression).(*ast.SelectorExpr)
		if !ok {
			return
		}
		for {
			inner, isSelector := selector.X.(*ast.SelectorExpr)
			if !isSelector {
				break
			}
			selector = inner
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok || identifier.Name != receiver[0].Name {
			return
		}
		if field, isVar := pkg.TypesInfo.Uses[selector.Sel].(*types.Var); isVar && field.IsField() {
			fields[field.Name()] = true
		}
	}
	factory := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.CompositeLit:
			if built, ok := types.Unalias(pkg.TypesInfo.TypeOf(typed)).(*types.Named); ok && built.Obj().Pkg() == pkg.Types {
				if _, isStruct := built.Underlying().(*types.Struct); isStruct {
					factory = true
				}
			}
		case *ast.CallExpr:
			if selector, ok := typed.Fun.(*ast.SelectorExpr); ok {
				receiverField(selector.X)
			}
			receiverField(typed.Fun)
			for _, argument := range typed.Args {
				receiverField(argument)
			}
		}
		return true
	})
	if factory {
		return nil
	}
	return fields
}

func adapterFieldViolations(pkg *packages.Package, declaration *ast.GenDecl, adapters map[*types.TypeName]bool, portImplementers map[*types.TypeName][]*types.TypeName) []string {
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
				default:
					wrapped := adaptersBehind(portImplementers[held], owner, adapters)
					if wrapped == "" {
						continue
					}
					if len(field.Names) == 0 {
						fieldName += held.Name()
					}
					violations = append(violations, fmt.Sprintf("%s: %s holds %s, %s, in %s",
						sourcePosition(pkg, field.Pos()), qualifiedTypeName(owner), qualifiedTypeName(held), wrapped, fieldName))
				}
			}
		}
	}
	return violations
}

// adaptersBehind describes the production implementations of a held port
// when every one of them other than the holder is an adapter, and returns ""
// otherwise.
func adaptersBehind(implementers []*types.TypeName, owner *types.TypeName, adapters map[*types.TypeName]bool) string {
	var names []string
	for _, implementer := range implementers {
		if implementer == owner {
			continue
		}
		if !adapters[implementer] {
			return ""
		}
		names = append(names, qualifiedTypeName(implementer))
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "whose only production implementation is adapter " + names[0]
	}
	sort.Strings(names)
	return "whose production implementations are all adapters (" + strings.Join(names, ", ") + ")"
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
