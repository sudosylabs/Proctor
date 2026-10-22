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
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// outcomeCodecFunctions are the one sqlstore helper pair through which every
// persisted command outcome is encoded and decoded.
var outcomeCodecFunctions = map[string]bool{"encodeOutcome": true, "decodeOutcome": true}

// TestPersistedOutcomesAreTaggedDocuments keeps every persisted command
// outcome (the command_outcomes.outcome document an idempotent replay
// returns) spelled by explicit json names rather than Go field names, so a
// field rename cannot silently change what a stored replay decodes to.
//
// Every sqlstore type implementing outcomeDocument, and every value passed to
// encodeOutcome or decodeOutcome, is a sqlstore struct whose exported fields
// all carry an explicit json name. A struct reachable through its fields is
// either another such sqlstore struct or a model document that tags every
// field itself (model owns that canonical serialization); store contract
// types, interfaces, and untagged model types are rejected. Leaves are basic
// and named basic types (typed IDs, enums), time.Time, and types that encode
// themselves (json.Marshaler and json.Unmarshaler).
//
// An outcome path (the encode or decode function of an idempotentMutation,
// a function it calls, or any function named for an outcome) never calls
// encoding/json or canonicalPreflightValue itself, and nothing decodes a
// row's Outcome field directly: they go through encodeOutcome/decodeOutcome.
func TestPersistedOutcomesAreTaggedDocuments(t *testing.T) {
	t.Parallel()
	for _, violation := range outcomeEncodingViolations(typedServerPackages(t), serverModule) {
		assert.Fail(t, violation)
	}
}

func TestOutcomeEncodingRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"model/model.go": `package model

import "time"

type UserID string

type Receipt struct {
	Digest string    ` + "`json:\"digest\"`" + `
	At     time.Time ` + "`json:\"at\"`" + `
}

type Exam struct {
	ID    string
	Title string ` + "`json:\"title\"`" + `
}
`,
		"store/store.go": `package store

type Snapshot struct {
	Revision int64
}
`,
		"store/sqlstore/outcome.go": `package sqlstore

import (
	"encoding/json"

	"fixture/model"
	"fixture/store"
)

type outcomeDocument interface{ outcomeDocument() }

func encodeOutcome(value outcomeDocument) ([]byte, error) { return json.Marshal(value) }

func decodeOutcome(data []byte, target outcomeDocument) error { return json.Unmarshal(data, target) }

func canonicalPreflightValue(value any) ([]byte, error) { return json.Marshal(value) }

type idempotentMutation[T any] struct {
	encode func(T) ([]byte, error)
	decode func([]byte) (T, error)
}

type commandOutcomeRow struct{ Outcome []byte }

type tagged struct {
	UserID  model.UserID   ` + "`json:\"user_id\"`" + `
	Receipt *model.Receipt ` + "`json:\"receipt\"`" + `
	Items   []taggedItem   ` + "`json:\"items\"`" + `
}

type taggedItem struct {
	Name string ` + "`json:\"name\"`" + `
}

type looseRow struct{ Count int }

func (tagged) outcomeDocument() {}

type untagged struct {
	UserID  model.UserID
	Exam    model.Exam      ` + "`json:\"exam\"`" + `
	Shadow  store.Snapshot  ` + "`json:\"snapshot\"`" + `
	Any     any             ` + "`json:\"any\"`" + `
	Skipped string          ` + "`json:\"-\"`" + `
	looseRow
}

func (untagged) outcomeDocument() {}

func run() {
	_ = idempotentMutation[tagged]{
		encode: func(value tagged) ([]byte, error) { return encodeOutcome(value) },
		decode: func(data []byte) (tagged, error) {
			var value tagged
			return value, decodeOutcome(data, &value)
		},
	}
	_ = idempotentMutation[tagged]{
		encode: func(value tagged) ([]byte, error) { return json.Marshal(value) },
		decode: decodeLoose,
	}
	_ = idempotentMutation[*model.Exam]{
		encode: func(value *model.Exam) ([]byte, error) { return canonicalPreflightValue(value) },
	}
}

func decodeLoose(data []byte) (tagged, error) {
	var value tagged
	return value, json.Unmarshal(data, &value)
}

func encodeReplayOutcome(value untagged) ([]byte, error) { return encodeOutcome(value) }

func readExport(row commandOutcomeRow) string {
	var id string
	_ = json.Unmarshal(row.Outcome, &id)
	return id
}
`,
	})
	require.Equal(t, []string{
		"outcome.go:40: untagged.UserID has no explicit json name; tag every persisted outcome field",
		"model.go:13: model.Exam.ID has no explicit json name; tag every persisted outcome field",
		"outcome.go:42: untagged.Shadow has type fixture/store.Snapshot; a persisted outcome nests only sqlstore documents or tagged model documents",
		"outcome.go:43: untagged.Any has type any; a persisted outcome nests no interface",
		"outcome.go:44: untagged.Skipped is not persisted (json \"-\"); a persisted outcome holds only persisted fields",
		"outcome.go:35: looseRow.Count has no explicit json name; tag every persisted outcome field",
		"outcome.go:59: idempotentMutation encode calls encoding/json.Marshal; encode outcomes through encodeOutcome",
		"outcome.go:63: idempotentMutation encode calls canonicalPreflightValue; encode outcomes through encodeOutcome",
		"outcome.go:69: decodeLoose calls encoding/json.Unmarshal; decode outcomes through decodeOutcome",
		"outcome.go:76: readExport decodes commandOutcomeRow.Outcome with encoding/json.Unmarshal; decode outcomes through decodeOutcome",
	}, outcomeEncodingViolations(loaded, "fixture"))
}

func outcomeEncodingViolations(loaded []*packages.Package, module string) []string {
	var sqlstore *packages.Package
	for _, pkg := range loaded {
		if pkg.PkgPath == module+"/store/sqlstore" {
			sqlstore = pkg
		}
	}
	if sqlstore == nil {
		return []string{"no " + module + "/store/sqlstore package"}
	}
	checker := &outcomeDocumentChecker{
		pkg: sqlstore, sqlstorePath: sqlstore.PkgPath, modelPath: module + "/model", seen: map[*types.Named]bool{},
		codecs: outcomeCodecFunctions,
	}
	marker, _ := sqlstore.Types.Scope().Lookup("outcomeDocument").(*types.TypeName)
	var iface *types.Interface
	if marker != nil {
		iface, _ = marker.Type().Underlying().(*types.Interface)
	}
	if iface == nil {
		return []string{"store/sqlstore declares no outcomeDocument interface"}
	}
	scope := sqlstore.Types.Scope()
	for _, name := range scope.Names() {
		typeName, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || typeName.IsAlias() {
			continue
		}
		named, ok := typeName.Type().(*types.Named)
		if !ok || types.IsInterface(named) || named.TypeParams().Len() > 0 {
			continue
		}
		if types.Implements(named, iface) || types.Implements(types.NewPointer(named), iface) {
			checker.document(named)
		}
	}
	checker.paths()
	return checker.violations
}

type outcomeDocumentChecker struct {
	pkg          *packages.Package
	sqlstorePath string
	modelPath    string
	seen         map[*types.Named]bool
	codecs       map[string]bool
	violations   []string
}

func (c *outcomeDocumentChecker) report(pos token.Pos, format string, args ...any) {
	c.violations = append(c.violations, sourcePosition(c.pkg, pos)+": "+fmt.Sprintf(format, args...))
}

// document checks one outcome document type and every struct it nests.
func (c *outcomeDocumentChecker) document(named *types.Named) {
	if c.seen[named] {
		return
	}
	c.seen[named] = true
	structure, ok := named.Underlying().(*types.Struct)
	if !ok {
		c.report(named.Obj().Pos(), "%s is not a struct; a persisted outcome is a tagged struct", c.name(named))
		return
	}
	c.fields(c.name(named), structure)
}

func (c *outcomeDocumentChecker) fields(owner string, structure *types.Struct) {
	for i := range structure.NumFields() {
		field := structure.Field(i)
		name := strings.Split(reflect.StructTag(structure.Tag(i)).Get("json"), ",")[0]
		if !field.Exported() && !field.Embedded() {
			continue
		}
		if field.Embedded() && name == "" {
			// An untagged embedded struct contributes its own tagged fields.
			c.fieldType(owner+"."+field.Name(), field.Pos(), field.Type())
			continue
		}
		switch name {
		case "-":
			c.report(field.Pos(), "%s.%s is not persisted (json \"-\"); a persisted outcome holds only persisted fields", owner, field.Name())
			continue
		case "":
			c.report(field.Pos(), "%s.%s has no explicit json name; tag every persisted outcome field", owner, field.Name())
		}
		c.fieldType(owner+"."+field.Name(), field.Pos(), field.Type())
	}
}

func (c *outcomeDocumentChecker) fieldType(field string, pos token.Pos, value types.Type) {
	value = types.Unalias(value)
	if encodesItself(value) {
		return
	}
	switch typed := value.(type) {
	case *types.Basic:
		return
	case *types.Pointer:
		c.fieldType(field, pos, typed.Elem())
	case *types.Slice:
		c.fieldType(field, pos, typed.Elem())
	case *types.Array:
		c.fieldType(field, pos, typed.Elem())
	case *types.Map:
		c.fieldType(field, pos, typed.Key())
		c.fieldType(field, pos, typed.Elem())
	case *types.Struct:
		c.fields(field, typed)
	case *types.Interface:
		c.report(pos, "%s has type %s; a persisted outcome nests no interface", field, types.TypeString(value, c.qualifier))
	case *types.Named:
		object := typed.Obj()
		if object.Pkg() != nil && object.Pkg().Path() == "time" && object.Name() == "Time" {
			return
		}
		switch underlying := typed.Underlying().(type) {
		case *types.Struct:
			path := ""
			if object.Pkg() != nil {
				path = object.Pkg().Path()
			}
			if path != c.sqlstorePath && path != c.modelPath {
				c.report(pos, "%s has type %s; a persisted outcome nests only sqlstore documents or tagged model documents",
					field, types.TypeString(value, nil))
				return
			}
			c.document(typed)
		case *types.Interface:
			c.report(pos, "%s has type %s; a persisted outcome nests no interface", field, types.TypeString(value, c.qualifier))
		default:
			c.fieldType(field, pos, underlying)
		}
	}
}

func (c *outcomeDocumentChecker) qualifier(pkg *types.Package) string {
	if pkg.Path() == c.sqlstorePath {
		return ""
	}
	return pkg.Name()
}

func (c *outcomeDocumentChecker) name(named *types.Named) string {
	return types.TypeString(named, c.qualifier)
}

// encodesItself reports whether a type spells its own JSON through
// MarshalJSON and UnmarshalJSON.
func encodesItself(value types.Type) bool {
	if _, ok := value.(*types.Named); !ok {
		return false
	}
	pointer := types.NewMethodSet(types.NewPointer(value))
	return pointer.Lookup(nil, "MarshalJSON") != nil && pointer.Lookup(nil, "UnmarshalJSON") != nil
}

// paths reports encoding/json calls in outcome paths and direct decodes of
// a row's Outcome field.
func (c *outcomeDocumentChecker) paths() {
	info := c.pkg.TypesInfo
	declarations := map[*types.Func]*ast.FuncDecl{}
	for _, file := range c.pkg.Syntax {
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				if object, ok := info.Defs[function.Name].(*types.Func); ok {
					declarations[object] = function
				}
			}
		}
	}
	type path struct {
		label string
		body  *ast.BlockStmt
	}
	var paths []path
	visited := map[*ast.BlockStmt]bool{}
	add := func(label string, body *ast.BlockStmt) {
		if body != nil && !visited[body] {
			visited[body] = true
			paths = append(paths, path{label: label, body: body})
		}
	}
	callee := func(call *ast.CallExpr) *types.Func {
		var identifier *ast.Ident
		switch function := ast.Unparen(call.Fun).(type) {
		case *ast.Ident:
			identifier = function
		case *ast.SelectorExpr:
			identifier = function.Sel
		case *ast.IndexExpr:
			if inner, ok := function.X.(*ast.Ident); ok {
				identifier = inner
			}
		}
		if identifier == nil {
			return nil
		}
		object, _ := info.Uses[identifier].(*types.Func)
		return object
	}
	local := func(function *types.Func) *ast.FuncDecl {
		if function == nil || function.Pkg() != c.pkg.Types || outcomeCodecFunctions[function.Name()] {
			return nil
		}
		if _, direct := directOutcomeCodec(function, c.pkg.Types); direct {
			return nil
		}
		return declarations[function]
	}
	for _, file := range c.pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isIdempotentMutation(info.TypeOf(literal), c.pkg.Types) {
				return true
			}
			for _, element := range literal.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := pair.Key.(*ast.Ident)
				if !ok || (key.Name != "encode" && key.Name != "decode") {
					continue
				}
				switch value := ast.Unparen(pair.Value).(type) {
				case *ast.FuncLit:
					add("idempotentMutation "+key.Name, value.Body)
					ast.Inspect(value.Body, func(inner ast.Node) bool {
						if call, ok := inner.(*ast.CallExpr); ok {
							if declaration := local(callee(call)); declaration != nil {
								add(declaration.Name.Name, declaration.Body)
							}
						}
						return true
					})
				case *ast.Ident, *ast.SelectorExpr:
					var identifier *ast.Ident
					if selector, ok := value.(*ast.SelectorExpr); ok {
						identifier = selector.Sel
					} else {
						identifier = value.(*ast.Ident)
					}
					if function, ok := info.Uses[identifier].(*types.Func); ok {
						if declaration := local(function); declaration != nil {
							add(declaration.Name.Name, declaration.Body)
						}
					}
				}
			}
			return true
		})
	}
	for function, declaration := range declarations {
		if strings.Contains(strings.ToLower(function.Name()), "outcome") && !outcomeCodecFunctions[function.Name()] {
			add(declaration.Name.Name, declaration.Body)
		}
	}
	sort.SliceStable(paths, func(i, j int) bool { return paths[i].body.Pos() < paths[j].body.Pos() })
	for _, current := range paths {
		ast.Inspect(current.body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if name, direct := directOutcomeCodec(callee(call), c.pkg.Types); direct {
				verb := "encode"
				if strings.Contains(name, "Unmarshal") || strings.Contains(name, "Decoder") {
					verb = "decode"
				}
				c.report(call.Pos(), "%s calls %s; %s outcomes through %sOutcome", current.label, name, verb, verb)
			}
			return true
		})
	}
	for _, file := range c.pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			function := callee(call)
			if function == nil || function.Pkg() != c.pkg.Types || !c.codecs[function.Name()] || len(call.Args) == 0 {
				return true
			}
			c.argument(function.Name(), call.Args[len(call.Args)-1])
			return true
		})
	}
	for _, file := range c.pkg.Syntax {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, direct := directOutcomeCodec(callee(call), c.pkg.Types)
			if !direct {
				return true
			}
			for _, argument := range call.Args {
				for _, read := range outcomeFieldReads(argument, info, c.pkg.Types) {
					c.report(call.Pos(), "%s decodes %s with %s; decode outcomes through decodeOutcome",
						enclosingFunction(file, call.Pos()), read, name)
				}
			}
			return true
		})
	}
}

// argument checks the document a codec call encodes or decodes into.
func (c *outcomeDocumentChecker) argument(codec string, argument ast.Expr) {
	value := types.Unalias(c.pkg.TypesInfo.TypeOf(argument))
	if pointer, ok := value.(*types.Pointer); ok {
		value = types.Unalias(pointer.Elem())
	}
	if types.IsInterface(value) {
		return
	}
	named, ok := value.(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != c.sqlstorePath {
		c.report(argument.Pos(), "%s persists %s; a persisted outcome is a dedicated sqlstore document",
			codec, types.TypeString(value, c.qualifier))
		return
	}
	c.document(named)
}

func isIdempotentMutation(value types.Type, owner *types.Package) bool {
	named, ok := value.(*types.Named)
	return ok && named.Origin().Obj().Name() == "idempotentMutation" && named.Origin().Obj().Pkg() == owner
}

// directOutcomeCodec reports whether a callee encodes or decodes JSON
// outside the outcome codec pair.
func directOutcomeCodec(function *types.Func, owner *types.Package) (string, bool) {
	if function == nil || function.Pkg() == nil {
		return "", false
	}
	if function.Pkg().Path() == "encoding/json" {
		switch function.Name() {
		case "Marshal", "MarshalIndent", "Unmarshal", "NewDecoder", "NewEncoder":
			return "encoding/json." + function.Name(), true
		}
	}
	if function.Pkg() == owner && function.Name() == "canonicalPreflightValue" {
		return function.Name(), true
	}
	return "", false
}

// outcomeFieldReads names every read of a sqlstore struct field named
// Outcome inside an expression.
func outcomeFieldReads(expression ast.Expr, info *types.Info, owner *types.Package) []string {
	var reads []string
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Outcome" {
			return true
		}
		selection := info.Selections[selector]
		if selection == nil || selection.Kind() != types.FieldVal {
			return true
		}
		receiver := selection.Recv()
		if pointer, ok := receiver.(*types.Pointer); ok {
			receiver = pointer.Elem()
		}
		if named, ok := receiver.(*types.Named); ok && named.Obj().Pkg() == owner {
			reads = append(reads, named.Obj().Name()+".Outcome")
		}
		return true
	})
	return reads
}

func enclosingFunction(file *ast.File, pos token.Pos) string {
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Pos() <= pos && pos <= function.End() {
			return function.Name.Name
		}
	}
	return "package scope"
}
