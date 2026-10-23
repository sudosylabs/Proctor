// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"

	"github.com/sudosylabs/proctor/server/internal/errorflow"
)

// routeErrorFixture is a miniature application: an apperror package with the
// real API's shape, use cases that mint, translate, conceal and delegate
// codes, and transport handlers that call them through an interface.
var routeErrorFixture = map[string]string{
	"app/apperror/apperror.go": `package apperror

import "errors"

type Code string

type Error struct{ code Code }

func (e *Error) Error() string { return string(e.code) }

func (e *Error) Wrap(error) *Error { return e }

func New(code Code) *Error { return &Error{code: code} }

func Invalid(code Code, _ string) *Error { return New(code) }

func Is[C ~string](err error, code C) bool {
	var failure *Error
	return errors.As(err, &failure) && string(failure.code) == string(code)
}

type StoreTable struct{ NotFound, Conflict, Unavailable Code }

func (t StoreTable) Translate(err error) error {
	if err == nil {
		return New(t.Unavailable)
	}
	return New(t.NotFound)
}

type Concealment struct {
	Unavailable Code
	Concealed   []Code
}

func (c Concealment) Present(err error) error { return err }

func (c Concealment) Conceal(err error) error { return err }
`,
	"app/app.go": `package app

import "fixture/app/apperror"

type Store interface{ Get() error }

type App struct{ Store Store }

var thingStoreErrors = apperror.StoreTable{NotFound: "thing.not_found", Conflict: "thing.conflict", Unavailable: "thing.unavailable"}

var publicFailures = apperror.Concealment{Unavailable: "thing.unavailable", Concealed: []apperror.Code{"thing.hidden"}}

func (a *App) GetThing() error {
	if err := a.Store.Get(); err != nil {
		return thingStoreErrors.Translate(err)
	}
	if apperror.Is(a.Store.Get(), "thing.compared") {
		return nil
	}
	return apperror.New("thing.locked")
}

func (a *App) HideThing() (err error) {
	defer func() { err = publicFailures.Conceal(err) }()
	if a.Store == nil {
		return apperror.New("authorization.denied")
	}
	return apperror.New("thing.hidden")
}

func (a *App) RenameThing(name string) error {
	if name == "" {
		return reject("thing.invalid", "name")
	}
	return nil
}

func reject(code apperror.Code, field string) error { return apperror.Invalid(code, field) }
`,
	"httpapi/httpapi.go": `package httpapi

import "fixture/app"

type things interface {
	GetThing() error
	HideThing() error
	RenameThing(string) error
}

type Module struct{ things things }

func New(application *app.App) Module { return Module{things: application} }

func (m Module) Get() error { return m.things.GetThing() }

func (m Module) Hide() error { return m.things.HideThing() }

func (m Module) Rename() error { return m.things.RenameThing("") }
`,
}

func analyzeRouteErrorFixture(t *testing.T) *errorflow.Analysis {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"go.mod": "module fixture\n\ngo 1.25\n"}
	for name, contents := range routeErrorFixture {
		files[name] = contents
	}
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	}
	loaded, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Dir: root, Env: append(os.Environ(), "GOWORK=off")}, "./...")
	require.NoError(t, err)
	require.Zero(t, packages.PrintErrors(loaded))
	analysis, err := errorflow.Analyze(loaded, errorflow.Config{Module: "fixture", AppErrorPackage: "fixture/app/apperror"})
	require.NoError(t, err)
	return analysis
}

// TestRouteErrorTableCheckRejectsPlantedViolations proves the check that
// httpapi runs on every production route: an over-declared code nothing can
// return and an under-declared code the operation returns are both reported,
// through interface calls, Store translation, deferred concealment and a code
// passed as a parameter, while codes a route may legitimately omit are not.
func TestRouteErrorTableCheckRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	analysis := analyzeRouteErrorFixture(t)
	operation := func(name string) *ssa.Function {
		function := analysis.Function("(fixture/httpapi.Module)." + name)
		require.NotNil(t, function, name)
		return function
	}
	routes := []errorflow.Route{
		// Over: thing.gone. Under: thing.locked. thing.not_found and
		// thing.conflict depend on the Store failure, so leaving them out is
		// not reported, and an apperror.Is comparison never mints.
		{Key: "GET /things/{id}", Operation: operation("Get"), Declared: []string{"thing.unavailable", "thing.gone"}},
		// Concealment rewrites the denial and thing.hidden to
		// resource.not_found: declaring the denial is an over-declaration and
		// resource.not_found is required.
		{Key: "POST /things/{id}/hide", Operation: operation("Hide"), Declared: []string{"authorization.denied", "thing.unavailable"}},
		// The code reaches apperror.Invalid through reject's parameter.
		{Key: "PUT /things/{id}/name", Operation: operation("Rename"), Declared: nil},
		// A transport-implied code may be declared without being minted.
		{Key: "GET /things", Operation: operation("Get"), Declared: []string{"thing.locked", "thing.unavailable", "request.invalid"}, Implied: []string{"request.invalid"}},
	}

	require.Equal(t, []string{
		`GET /things/{id} can return "thing.locked" but does not declare it`,
		`GET /things/{id} declares "thing.gone", which nothing reachable can return`,
		`POST /things/{id}/hide can return "resource.not_found" but does not declare it`,
		`POST /things/{id}/hide declares "authorization.denied", which nothing reachable can return`,
		`PUT /things/{id}/name can return "thing.invalid" but does not declare it`,
	}, errorflow.CheckRoutes(analysis, routes, nil))

	exceptions := map[string]string{
		"GET /things/{id} thing.locked":     "planted exception",
		"GET /things/{id} thing.gone":       "planted exception",
		"PUT /things/{id}/name thing.never": "planted stale exception",
	}
	require.Equal(t, []string{
		`POST /things/{id}/hide can return "resource.not_found" but does not declare it`,
		`POST /things/{id}/hide declares "authorization.denied", which nothing reachable can return`,
		`PUT /things/{id}/name can return "thing.invalid" but does not declare it`,
		`exception "PUT /things/{id}/name thing.never" no longer suppresses anything; remove it`,
	}, errorflow.CheckRoutes(analysis, routes, exceptions))
}
