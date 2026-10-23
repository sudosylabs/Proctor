// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package errorflow computes, for every function of a module, which
// application error codes it can return. It exists for tests: httpapi checks
// each route's declared error table against what its operation can return,
// and the architecture tests prove the check on planted fixtures.
//
// Every code is a constant of type apperror.Code, so two sets are computed
// from SSA and a VTA call graph:
//
//   - Possible over-approximates: every code constant in the function or
//     anything it can call, and every code in a package-level table it reads.
//     A declared code outside Possible can never be returned.
//   - Definite under-approximates: codes minted by apperror.New or Invalid,
//     or reported by a callee, whose error value flows to the function's
//     return, plus the Unavailable code of a StoreTable.Translate or
//     Concealment.Present whose result is returned. A Definite code that is
//     not declared would reach the client as an internal error.
//
// The apperror API is modelled rather than analyzed: Concealment.Conceal and
// Present (also when deferred), StoreTable.Translate, and the *Error
// decorators. A Code parameter passed to New is substituted with the
// constant each caller passes.
package errorflow

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// Config names the module and its application error package.
type Config struct {
	// Module is the module path whose functions are analyzed.
	Module string
	// AppErrorPackage is the import path of the package declaring Code,
	// New, Invalid, StoreTable, and Concealment.
	AppErrorPackage string
	// OpaquePackages are package path prefixes whose functions are not
	// analyzed because they cannot mint codes (the Store, say).
	OpaquePackages []string
}

// Analysis holds the computed code sets of every analyzed function.
type Analysis struct {
	program  *ssa.Program
	config   Config
	possible map[*ssa.Function]codeSet
	definite map[*ssa.Function]codeSet
	byName   map[string]*ssa.Function
	tables   map[*types.Var]tableCodes
}

// codeSet is a set of codes; a "$param:N" member stands for the code the
// caller passes as the function's Nth parameter.
type codeSet map[string]struct{}

func (s codeSet) add(code string) bool {
	if _, ok := s[code]; ok {
		return false
	}
	s[code] = struct{}{}
	return true
}

func (s codeSet) union(other codeSet) bool {
	changed := false
	for code := range other {
		if s.add(code) {
			changed = true
		}
	}
	return changed
}

func (s codeSet) equal(other codeSet) bool {
	if len(s) != len(other) {
		return false
	}
	for code := range s {
		if _, ok := other[code]; !ok {
			return false
		}
	}
	return true
}

func (s codeSet) sorted() []string {
	result := make([]string, 0, len(s))
	for code := range s {
		if !strings.HasPrefix(code, paramMarker) {
			result = append(result, code)
		}
	}
	sort.Strings(result)
	return result
}

const paramMarker = "$param:"

// tableCodes are the code constants of one package-level StoreTable,
// Concealment, or other value, by field ("" for anything else).
type tableCodes map[string][]string

// Analyze builds SSA and a VTA call graph for packages loaded with complete
// syntax (packages.LoadAllSyntax) and computes every function's code sets.
func Analyze(loaded []*packages.Package, config Config) (*Analysis, error) {
	program, _ := ssautil.AllPackages(loaded, ssa.InstantiateGenerics)
	program.Build()
	analysis := &Analysis{program: program, config: config, possible: map[*ssa.Function]codeSet{},
		definite: map[*ssa.Function]codeSet{}, byName: map[string]*ssa.Function{}, tables: map[*types.Var]tableCodes{}}
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		if analysis.inModule(pkg.PkgPath) {
			analysis.collectTables(pkg)
		}
	})
	analysis.resolveTableBases()
	functions := map[*ssa.Function]bool{}
	propagated := map[*ssa.Function]bool{}
	for function := range ssautil.AllFunctions(program) {
		if analysis.analyzed(function) {
			functions[function] = true
			analysis.byName[function.String()] = function
		}
		if functions[function] || function.Synthetic != "" {
			propagated[function] = true
		}
	}
	if len(functions) == 0 {
		return nil, fmt.Errorf("errorflow: no function of %s was loaded", config.Module)
	}
	graph := vta.CallGraph(propagated, cha.CallGraph(program))
	graph.DeleteSyntheticNodes()
	analysis.computePossible(functions, graph)
	analysis.computeDefinite(functions, graph)
	for function := range functions {
		analysis.possible[function].union(analysis.definite[function])
	}
	return analysis, nil
}

// Function returns the analyzed function with the given SSA name, such as
// "(example.com/m/httpapi.module).list" or "example.com/m/app.New".
func (a *Analysis) Function(name string) *ssa.Function {
	return a.byName[name]
}

// FunctionForRuntimeName maps a runtime.FuncForPC name, such as
// "example.com/m/httpapi.module.list-fm", "example.com/m/httpapi.(*module).list-fm"
// or "example.com/m/httpapi.resource.func1", to the analyzed function.
func (a *Analysis) FunctionForRuntimeName(name string) *ssa.Function {
	name = strings.TrimSuffix(name, "-fm")
	slash := strings.LastIndex(name, "/")
	dot := strings.Index(name[slash+1:], ".")
	if dot < 0 {
		return nil
	}
	pkg, rest := name[:slash+1+dot], name[slash+1+dot+1:]
	parts := strings.Split(rest, ".")
	switch {
	case len(parts) == 1:
		return a.byName[pkg+"."+parts[0]]
	case strings.HasPrefix(parts[1], "func"):
		return a.closure(a.byName[pkg+"."+parts[0]], parts[1:])
	case len(parts) == 2:
		receiver := parts[0]
		if strings.HasPrefix(receiver, "(*") {
			return a.byName["(*"+pkg+"."+strings.TrimSuffix(strings.TrimPrefix(receiver, "(*"), ")")+")."+parts[1]]
		}
		return a.byName["("+pkg+"."+receiver+")."+parts[1]]
	default:
		return nil
	}
}

// closure follows runtime closure names (func1, func1.2) to SSA anonymous
// functions, which both number in source order from one.
func (a *Analysis) closure(outer *ssa.Function, path []string) *ssa.Function {
	current := outer
	for index, segment := range path {
		if current == nil {
			return nil
		}
		number := strings.TrimPrefix(segment, "func")
		if index > 0 {
			number = segment
		}
		position, err := strconv.Atoi(number)
		if err != nil || position < 1 || position > len(current.AnonFuncs) {
			return nil
		}
		current = current.AnonFuncs[position-1]
	}
	return current
}

// Possible returns the codes fn can possibly return, sorted.
func (a *Analysis) Possible(fn *ssa.Function) []string { return a.possible[fn].sorted() }

// Definite returns the codes fn certainly can return, sorted.
func (a *Analysis) Definite(fn *ssa.Function) []string { return a.definite[fn].sorted() }

func (a *Analysis) inModule(path string) bool {
	return path == a.config.Module || strings.HasPrefix(path, a.config.Module+"/")
}

func (a *Analysis) analyzed(function *ssa.Function) bool {
	pkg := function.Package()
	if pkg == nil && function.Origin() != nil {
		pkg = function.Origin().Package()
	}
	if pkg == nil {
		return false
	}
	path := pkg.Pkg.Path()
	if !a.inModule(path) || path == a.config.AppErrorPackage {
		return false
	}
	for _, prefix := range a.config.OpaquePackages {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return false
		}
	}
	return true
}

// resolveTableBases copies a base table's fields into every table derived
// from it with With, whose own codes are additional rules.
func (a *Analysis) resolveTableBases() {
	byName := make(map[string]*types.Var, len(a.tables))
	for variable := range a.tables {
		if variable.Pkg() != nil {
			byName[variable.Pkg().Path()+"."+variable.Name()] = variable
		}
	}
	// Expand references to other tables into plain codes, transitively.
	for changed := true; changed; {
		changed = false
		for _, codes := range a.tables {
			for field, values := range codes {
				var expanded []string
				for _, value := range values {
					if !strings.HasPrefix(value, referenceMarker) {
						expanded = append(expanded, value)
						continue
					}
					changed = true
					for referencedField, referencedValues := range a.tables[byName[strings.TrimPrefix(value, referenceMarker)]] {
						if referencedField != withBaseField {
							expanded = append(expanded, referencedValues...)
						}
					}
				}
				codes[field] = expanded
			}
		}
	}
	for range len(a.tables) {
		changed := false
		for _, codes := range a.tables {
			bases := codes[withBaseField]
			if len(bases) == 0 {
				continue
			}
			base := a.tables[byName[bases[0]]]
			if len(base[withBaseField]) > 0 {
				continue
			}
			delete(codes, withBaseField)
			for field, values := range base {
				codes[field] = append(codes[field], values...)
			}
			changed = true
		}
		if !changed {
			return
		}
	}
}

// collectTables records the code constants of every package-level variable
// initializer, by struct field where the initializer is a composite literal.
func (a *Analysis) collectTables(pkg *packages.Package) {
	for _, file := range pkg.Syntax {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			for _, spec := range general.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range value.Names {
					if index >= len(value.Values) {
						continue
					}
					variable, ok := pkg.TypesInfo.Defs[name].(*types.Var)
					if !ok {
						continue
					}
					a.tables[variable] = a.initializerCodes(pkg.TypesInfo, value.Values[index])
				}
			}
		}
	}
}

func (a *Analysis) initializerCodes(info *types.Info, expression ast.Expr) tableCodes {
	codes := tableCodes{}
	if call, ok := ast.Unparen(expression).(*ast.CallExpr); ok {
		if callee := a.calledAppErrorFunction(info, call); callee == "AdministrationStoreTable" && len(call.Args) == 4 {
			codes["NotFound"] = []string{"resource.not_found"}
			codes["Unavailable"] = []string{"administration.unavailable"}
			codes["Conflict"] = a.expressionCodes(info, call.Args[1])
			codes["Invalid"] = a.expressionCodes(info, call.Args[2])
			codes["Constraints"] = a.expressionCodes(info, call.Args[3])
			return codes
		} else if callee == "StoreTable.With" {
			// base.With(rules...) keeps the base table's fields.
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if base, ok := info.Uses[identifierOf(selector.X)].(*types.Var); ok {
					codes[withBaseField] = []string{base.Pkg().Path() + "." + base.Name()}
				}
			}
		}
	}
	literal, _ := ast.Unparen(expression).(*ast.CompositeLit)
	if literal == nil {
		codes[""] = append(codes[""], a.expressionCodes(info, expression)...)
		return codes
	}
	for _, element := range literal.Elts {
		field := ""
		valueExpression := element
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			if key, ok := keyed.Key.(*ast.Ident); ok {
				field = key.Name
			}
			valueExpression = keyed.Value
		}
		codes[field] = append(codes[field], a.expressionCodes(info, valueExpression)...)
	}
	return codes
}

// referenceMarker prefixes, among a table's codes, a package-level variable
// whose codes the table also holds.
const referenceMarker = "$ref:"

// withBaseField records, for a table built as base.With(...), the base
// variable whose fields the table inherits.
const withBaseField = "$with"

func identifierOf(expression ast.Expr) *ast.Ident {
	identifier, _ := ast.Unparen(expression).(*ast.Ident)
	return identifier
}

// calledAppErrorFunction names the apperror function or method a call
// expression invokes, as apperrorCallee does for SSA calls.
func (a *Analysis) calledAppErrorFunction(info *types.Info, call *ast.CallExpr) string {
	var object types.Object
	switch function := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		object = info.Uses[function]
	case *ast.SelectorExpr:
		if selection := info.Selections[function]; selection != nil {
			object = selection.Obj()
		} else {
			object = info.Uses[function.Sel]
		}
	}
	callee, ok := object.(*types.Func)
	if !ok || callee.Pkg() == nil || callee.Pkg().Path() != a.config.AppErrorPackage {
		return ""
	}
	if receiver := callee.Signature().Recv(); receiver != nil {
		receiverType := receiver.Type()
		if pointer, ok := receiverType.(*types.Pointer); ok {
			receiverType = pointer.Elem()
		}
		if named, ok := receiverType.(*types.Named); ok {
			return named.Obj().Name() + "." + callee.Name()
		}
	}
	return callee.Name()
}

func (a *Analysis) expressionCodes(info *types.Info, expression ast.Expr) []string {
	var codes []string
	ast.Inspect(expression, func(node ast.Node) bool {
		// Another package-level table the initializer reads contributes its
		// codes; resolveTableBases expands the reference.
		if identifier, ok := node.(*ast.Ident); ok {
			if variable, ok := info.Uses[identifier].(*types.Var); ok && variable.Pkg() != nil && variable.Parent() == variable.Pkg().Scope() {
				codes = append(codes, referenceMarker+variable.Pkg().Path()+"."+variable.Name())
			}
		}
		value, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		typed := info.Types[value]
		if typed.Value != nil && typed.Value.Kind() == constant.String && a.isCode(typed.Type) {
			codes = append(codes, constant.StringVal(typed.Value))
			return false
		}
		return true
	})
	return codes
}

func (a *Analysis) isCode(value types.Type) bool {
	return a.isNamed(value, "Code")
}

func (a *Analysis) isNamed(value types.Type, name string) bool {
	if pointer, ok := value.(*types.Pointer); ok {
		value = pointer.Elem()
	}
	named, ok := value.(*types.Named)
	return ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == a.config.AppErrorPackage && named.Obj().Name() == name
}

// apperrorCallee returns the name of an apperror function or method a call
// invokes ("New", "Error.Wrap", "StoreTable.Translate", ...), or "".
func (a *Analysis) apperrorCallee(common *ssa.CallCommon) string {
	callee := common.StaticCallee()
	if callee == nil {
		return ""
	}
	if callee.Origin() != nil {
		callee = callee.Origin()
	}
	if callee.Package() == nil || callee.Package().Pkg.Path() != a.config.AppErrorPackage {
		return ""
	}
	if receiver := callee.Signature.Recv(); receiver != nil {
		receiverType := receiver.Type()
		if pointer, ok := receiverType.(*types.Pointer); ok {
			receiverType = pointer.Elem()
		}
		if named, ok := receiverType.(*types.Named); ok {
			return named.Obj().Name() + "." + callee.Name()
		}
	}
	return callee.Name()
}

// computePossible gathers each function's local code constants, closes them
// over the call graph, and applies a deferred concealment, which rewrites
// what the function returns whatever its callees report.
func (a *Analysis) computePossible(functions map[*ssa.Function]bool, graph *callgraph.Graph) {
	local := make(map[*ssa.Function]codeSet, len(functions))
	for function := range functions {
		local[function] = a.localPossible(function)
		a.possible[function] = codeSet{}
	}
	for changed := true; changed; {
		changed = false
		for function := range functions {
			next := codeSet{}
			next.union(local[function])
			if node := graph.Nodes[function]; node != nil {
				for _, edge := range node.Out {
					if callee := edge.Callee.Func; functions[callee] && callee != function {
						next.union(a.possible[callee])
					}
				}
			}
			// A function value handed to code outside the analysis (a
			// sync.Once, an errgroup, a sort callback) may still run.
			for _, referenced := range referencedFunctions(function) {
				if functions[referenced] && referenced != function {
					next.union(a.possible[referenced])
				}
			}
			a.applyDeferredPresentation(function, next)
			if !next.equal(a.possible[function]) {
				a.possible[function] = next
				changed = true
			}
		}
	}
}

func referencedFunctions(function *ssa.Function) []*ssa.Function {
	var referenced []*ssa.Function
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			for _, operand := range instruction.Operands(nil) {
				if value, ok := (*operand).(*ssa.Function); ok {
					referenced = append(referenced, value)
				}
			}
		}
	}
	return referenced
}

func (a *Analysis) localPossible(function *ssa.Function) codeSet {
	codes := codeSet{}
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			if binary, ok := instruction.(*ssa.BinOp); ok && (binary.Op == token.EQL || binary.Op == token.NEQ) {
				continue
			}
			if call, ok := instruction.(ssa.CallInstruction); ok {
				switch a.apperrorCallee(call.Common()) {
				case "Is":
					continue
				case "StoreTable.Translate":
					codes.add("idempotency.conflict")
					codes.add("idempotency.in_progress")
				case "Concealment.Conceal":
					codes.add("resource.not_found")
				case "AdministrationStoreTable":
					codes.add("resource.not_found")
					codes.add("administration.unavailable")
				}
			}
			for _, operand := range instruction.Operands(nil) {
				switch value := (*operand).(type) {
				case *ssa.Const:
					if a.isCode(value.Type()) && value.Value != nil && value.Value.Kind() == constant.String {
						codes.add(constant.StringVal(value.Value))
					}
				case *ssa.Global:
					if variable, ok := value.Object().(*types.Var); ok {
						for field, fieldCodes := range a.tables[variable] {
							if field == withBaseField {
								continue
							}
							for _, code := range fieldCodes {
								codes.add(code)
							}
						}
					}
				}
			}
		}
	}
	return codes
}

// computeDefinite iterates the value-flow summary of every function until no
// summary changes.
func (a *Analysis) computeDefinite(functions map[*ssa.Function]bool, graph *callgraph.Graph) {
	for function := range functions {
		a.definite[function] = codeSet{}
	}
	for changed := true; changed; {
		changed = false
		for function := range functions {
			if a.definite[function].union(a.summarize(function, functions, graph)) {
				changed = true
			}
		}
	}
}

// summarize computes the codes carried by the error values function returns.
func (a *Analysis) summarize(function *ssa.Function, functions map[*ssa.Function]bool, graph *callgraph.Graph) codeSet {
	carried := map[ssa.Value]codeSet{}
	get := func(value ssa.Value) codeSet {
		if codes, ok := carried[value]; ok {
			return codes
		}
		return nil
	}
	for changed := true; changed; {
		changed = false
		update := func(value ssa.Value, codes codeSet) {
			if len(codes) == 0 || value == nil {
				return
			}
			current := carried[value]
			if current == nil {
				current = codeSet{}
				carried[value] = current
			}
			if current.union(codes) {
				changed = true
			}
		}
		for _, block := range function.Blocks {
			for _, instruction := range block.Instrs {
				switch value := instruction.(type) {
				case *ssa.Call:
					update(value, a.callCodes(function, value.Common(), value, get, functions, graph))
				case *ssa.MakeInterface:
					update(value, get(value.X))
				case *ssa.ChangeType:
					update(value, get(value.X))
				case *ssa.ChangeInterface:
					update(value, get(value.X))
				case *ssa.TypeAssert:
					update(value, get(value.X))
				case *ssa.Extract:
					update(value, get(value.Tuple))
				case *ssa.Phi:
					for _, edge := range value.Edges {
						update(value, get(edge))
					}
				case *ssa.UnOp:
					if value.Op == token.MUL {
						update(value, get(value.X))
					}
				case *ssa.Store:
					update(value.Addr, get(value.Val))
				}
			}
		}
	}
	result := codeSet{}
	for _, block := range function.Blocks {
		if ret, ok := block.Instrs[len(block.Instrs)-1].(*ssa.Return); ok {
			for _, value := range ret.Results {
				result.union(get(value))
			}
		}
	}
	a.applyDeferredPresentation(function, result)
	return result
}

// callCodes returns the codes a call's result carries.
func (a *Analysis) callCodes(function *ssa.Function, common *ssa.CallCommon, site ssa.CallInstruction,
	get func(ssa.Value) codeSet, functions map[*ssa.Function]bool, graph *callgraph.Graph,
) codeSet {
	switch a.apperrorCallee(common) {
	case "New", "Invalid":
		return a.argumentCodes(function, common.Args[0])
	case "Error.Wrap", "Error.WithField", "Error.WithFields":
		return get(common.Args[0])
	case "StoreTable.Translate":
		codes := codeSet{}
		codes.union(get(common.Args[1]))
		for _, code := range a.receiverField(common.Args[0], "Unavailable") {
			codes.add(code)
		}
		return codes
	case "Concealment.Present", "Concealment.Conceal":
		codes := codeSet{}
		codes.union(get(common.Args[1]))
		a.present(codes, common.Args[0], strings.HasSuffix(a.apperrorCallee(common), "Conceal"))
		return codes
	case "":
		// Not apperror: the callees' summaries below.
	default:
		// Is, Classify and the like inspect or name a code; they return no
		// application error.
		return nil
	}
	codes := codeSet{}
	node := graph.Nodes[function]
	if node == nil {
		return codes
	}
	for _, edge := range node.Out {
		if edge.Site != site || !functions[edge.Callee.Func] {
			continue
		}
		for code := range a.definite[edge.Callee.Func] {
			if strings.HasPrefix(code, paramMarker) {
				index, _ := strconv.Atoi(strings.TrimPrefix(code, paramMarker))
				// The callee's Params start with its receiver; an interface
				// invocation carries the receiver outside Args.
				if common.IsInvoke() {
					index--
				}
				if index >= 0 && index < len(common.Args) {
					codes.union(a.argumentCodes(function, common.Args[index]))
				}
				continue
			}
			codes.add(code)
		}
	}
	return codes
}

// argumentCodes returns the code a Code-typed argument stands for: its
// constant, or a parameter marker of the enclosing function.
func (a *Analysis) argumentCodes(function *ssa.Function, argument ssa.Value) codeSet {
	codes := codeSet{}
	switch value := argument.(type) {
	case *ssa.Const:
		if value.Value != nil && value.Value.Kind() == constant.String {
			codes.add(constant.StringVal(value.Value))
		}
	case *ssa.Parameter:
		for index, parameter := range function.Params {
			if parameter == value {
				codes.add(paramMarker + strconv.Itoa(index))
			}
		}
	case *ssa.ChangeType:
		return a.argumentCodes(function, value.X)
	}
	return codes
}

// receiverField resolves a StoreTable or Concealment receiver read from a
// package-level variable (or derived from one with With) to one field's codes.
func (a *Analysis) receiverField(receiver ssa.Value, field string) []string {
	switch value := receiver.(type) {
	case *ssa.UnOp:
		if global, ok := value.X.(*ssa.Global); ok && value.Op == token.MUL {
			if variable, ok := global.Object().(*types.Var); ok {
				return a.tables[variable][field]
			}
		}
	case *ssa.Call:
		switch a.apperrorCallee(value.Common()) {
		case "StoreTable.With":
			return a.receiverField(value.Common().Args[0], field)
		case "AdministrationStoreTable":
			if field == "Unavailable" {
				return []string{"administration.unavailable"}
			}
		}
	}
	return nil
}

// present applies Concealment.Present or Conceal to codes.
func (a *Analysis) present(codes codeSet, concealment ssa.Value, conceal bool) {
	for _, code := range a.receiverField(concealment, "Unavailable") {
		codes.add(code)
	}
	if !conceal {
		return
	}
	concealed := append([]string{"authorization.denied"}, a.receiverField(concealment, "Concealed")...)
	removed := false
	for _, code := range concealed {
		if _, ok := codes[code]; ok {
			delete(codes, code)
			removed = true
		}
	}
	if removed {
		codes.add("resource.not_found")
	}
}

// applyDeferredPresentation models the defer that rewrites a named error
// result through a Concealment: defer func() { err = c.Conceal(err) }().
func (a *Analysis) applyDeferredPresentation(function *ssa.Function, codes codeSet) {
	for _, block := range function.Blocks {
		for _, instruction := range block.Instrs {
			deferred, ok := instruction.(*ssa.Defer)
			if !ok {
				continue
			}
			closure, ok := deferred.Call.Value.(*ssa.MakeClosure)
			if !ok {
				continue
			}
			body, ok := closure.Fn.(*ssa.Function)
			if !ok {
				continue
			}
			for _, innerBlock := range body.Blocks {
				for _, inner := range innerBlock.Instrs {
					call, ok := inner.(*ssa.Call)
					if !ok {
						continue
					}
					switch a.apperrorCallee(call.Common()) {
					case "Concealment.Present":
						a.present(codes, call.Common().Args[0], false)
					case "Concealment.Conceal":
						a.present(codes, call.Common().Args[0], true)
					}
				}
			}
		}
	}
}

// Route is one HTTP route: its operation and the codes its error table
// declares, apart from Implied codes the transport adds on its own.
// Infeasible codes are ones the operation's code contains but the route can
// never produce, because the transport never supplies their input.
type Route struct {
	Key        string
	Operation  *ssa.Function
	Declared   []string
	Implied    []string
	Infeasible []string
}

// CheckRoutes returns one message per over- or under-declared route code.
// A Definite code whose condition is internal need not be declared: the
// client sees a server fault either way.
// An exception, keyed "<route key> <code>", suppresses that finding; an
// exception that suppresses nothing is reported as stale.
func CheckRoutes(analysis *Analysis, routes []Route, exceptions map[string]string) []string {
	var problems []string
	used := map[string]bool{}
	for _, route := range routes {
		possible := analysis.possible[route.Operation]
		definite := analysis.definite[route.Operation]
		declared := codeSet{}
		for _, code := range route.Declared {
			declared.add(code)
		}
		implied := codeSet{}
		for _, code := range route.Implied {
			implied.add(code)
		}
		infeasible := codeSet{}
		for _, code := range route.Infeasible {
			infeasible.add(code)
		}
		for _, code := range declared.sorted() {
			if _, ok := implied[code]; ok {
				continue
			}
			if _, ok := possible[code]; ok {
				continue
			}
			key := route.Key + " " + code
			if _, ok := exceptions[key]; ok {
				used[key] = true
				continue
			}
			problems = append(problems, fmt.Sprintf("%s declares %q, which nothing reachable can return", route.Key, code))
		}
		for _, code := range definite.sorted() {
			// A server fault is a 500 whether or not the route names its
			// specific *.internal code.
			if strings.HasSuffix(code, ".internal") {
				continue
			}
			if _, ok := declared[code]; ok {
				continue
			}
			if _, ok := implied[code]; ok {
				continue
			}
			if _, ok := infeasible[code]; ok {
				continue
			}
			key := route.Key + " " + code
			if _, ok := exceptions[key]; ok {
				used[key] = true
				continue
			}
			problems = append(problems, fmt.Sprintf("%s can return %q but does not declare it", route.Key, code))
		}
	}
	for key := range exceptions {
		if !used[key] {
			problems = append(problems, fmt.Sprintf("exception %q no longer suppresses anything; remove it", key))
		}
	}
	sort.Strings(problems)
	return problems
}
