// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sudosylabs/proctor/server/internal/errorcatalog"
)

const (
	serverModulePath  = "github.com/sudosylabs/proctor/server"
	appErrorPackage   = serverModulePath + "/app/apperror"
	appErrorCodeType  = "Code"
	httpTransportPath = serverModulePath + "/httpapi"
	webSocketPath     = serverModulePath + "/websocket"
)

type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	Imports    []string
	Module     *struct{ Path string }
}

// scanMintedErrorCodes type-checks every production package that uses the
// application error vocabulary and returns each constant of type
// apperror.Code. Because codes are typed, every code reaching an
// application error is such a constant unless it is converted from a
// non-constant string, which is reported as an error so the inventory stays
// complete.
func scanMintedErrorCodes(ctx context.Context, moduleDir string) ([]errorcatalog.Mint, error) {
	command := exec.CommandContext(ctx, "go", "list", "-export", "-deps", "-json=ImportPath,Dir,GoFiles,Export,Imports,Module", "./...")
	command.Dir = moduleDir
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	exports := make(map[string]string)
	var targets []listedPackage
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var listed listedPackage
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		exports[listed.ImportPath] = listed.Export
		if listed.Module != nil && listed.Module.Path == serverModulePath && usesApplicationErrors(listed) {
			targets = append(targets, listed)
		}
	}
	fset := token.NewFileSet()
	imports := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file := exports[path]
		if file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
	var mints []errorcatalog.Mint
	var problems []string
	for _, target := range targets {
		found, invalid, err := scanPackage(fset, imports, target)
		if err != nil {
			return nil, err
		}
		mints = append(mints, found...)
		problems = append(problems, invalid...)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("application error codes must be constants:\n  %s", strings.Join(problems, "\n  "))
	}
	return mints, nil
}

func usesApplicationErrors(listed listedPackage) bool {
	if listed.ImportPath == appErrorPackage {
		return true
	}
	for _, imported := range listed.Imports {
		if imported == appErrorPackage {
			return true
		}
	}
	return false
}

func scanPackage(fset *token.FileSet, imports types.Importer, target listedPackage) ([]errorcatalog.Mint, []string, error) {
	files := make([]*ast.File, 0, len(target.GoFiles))
	for _, name := range target.GoFiles {
		file, err := parser.ParseFile(fset, filepath.Join(target.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, file)
	}
	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
	config := types.Config{Importer: imports}
	if _, err := config.Check(target.ImportPath, fset, files, info); err != nil {
		return nil, nil, fmt.Errorf("type-check %s: %w", target.ImportPath, err)
	}
	transport := target.ImportPath == httpTransportPath || strings.HasPrefix(target.ImportPath, httpTransportPath+"/") ||
		target.ImportPath == webSocketPath || strings.HasPrefix(target.ImportPath, webSocketPath+"/")
	compared := comparedExpressions(files)
	var mints []errorcatalog.Mint
	var problems []string
	for expression, value := range info.Types {
		if !isApplicationErrorCode(value.Type) || compared[expression] {
			continue
		}
		position := fset.Position(expression.Pos())
		location := fmt.Sprintf("%s:%d", relativeSourcePath(position.Filename), position.Line)
		if value.Value != nil && value.Value.Kind() == constant.String {
			mints = append(mints, errorcatalog.Mint{Code: constant.StringVal(value.Value), Package: target.ImportPath,
				Position: location, Transport: transport})
			continue
		}
		if call, ok := expression.(*ast.CallExpr); ok && len(call.Args) == 1 && info.Types[call.Fun].IsType() {
			problems = append(problems, location+": conversion of a non-constant value to apperror.Code")
		}
	}
	return mints, problems, nil
}

// comparedExpressions returns the operands of equality comparisons and the
// expressions of switch cases. Matching a code against a constant inspects a
// failure; it never mints one.
func comparedExpressions(files []*ast.File) map[ast.Expr]bool {
	compared := make(map[ast.Expr]bool)
	for _, file := range files {
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.BinaryExpr:
				if node.Op == token.EQL || node.Op == token.NEQ {
					compared[node.X] = true
					compared[node.Y] = true
				}
			case *ast.CaseClause:
				for _, expression := range node.List {
					compared[expression] = true
				}
			}
			return true
		})
	}
	return compared
}

func isApplicationErrorCode(value types.Type) bool {
	named, ok := value.(*types.Named)
	if !ok {
		return false
	}
	object := named.Obj()
	return object.Pkg() != nil && object.Pkg().Path() == appErrorPackage && object.Name() == appErrorCodeType
}

func relativeSourcePath(path string) string {
	if index := strings.LastIndex(path, "/server/"); index >= 0 {
		return path[index+len("/server/"):]
	}
	return path
}
