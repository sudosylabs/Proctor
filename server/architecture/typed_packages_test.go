// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// typedPackageMode loads syntax and type information for the requested
// packages. Their dependencies outside the pattern come from export data, and
// every requested package is type-checked once in one universe, so a type
// seen from two server packages is the same types.Object.
const typedPackageMode = packages.NeedName | packages.NeedFiles | packages.NeedImports |
	packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo

var serverTypedPackages = sync.OnceValues(func() ([]*packages.Package, error) {
	return loadTypedPackages("..", serverModule+"/...")
})

// typedServerPackages returns every production package of the server module,
// type-checked from source without test files.
func typedServerPackages(t *testing.T) []*packages.Package {
	t.Helper()
	loaded, err := serverTypedPackages()
	require.NoError(t, err)
	return loaded
}

func loadTypedPackages(directory string, patterns ...string) ([]*packages.Package, error) {
	return loadTypedPackagesWithEnv(directory, nil, patterns...)
}

func loadTypedPackagesWithEnv(directory string, env []string, patterns ...string) ([]*packages.Package, error) {
	loaded, err := packages.Load(&packages.Config{Mode: typedPackageMode, Dir: directory, Env: env}, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load %v: %w", patterns, err)
	}
	var problems []string
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		for _, problem := range pkg.Errors {
			problems = append(problems, problem.Error())
		}
	})
	if len(problems) > 0 {
		return nil, fmt.Errorf("load %v: %s", patterns, strings.Join(problems, "; "))
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].PkgPath < loaded[j].PkgPath })
	return loaded, nil
}

// loadFixturePackages writes files into a throwaway module named fixture and
// type-checks it, so a check can prove that it rejects a planted violation.
func loadFixturePackages(t *testing.T, files map[string]string) []*packages.Package {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module fixture\n\ngo 1.25\n"
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	}
	// The fixture is its own module; a GOWORK exported by the repository
	// Makefiles would otherwise exclude it from the build list.
	loaded, err := loadTypedPackagesWithEnv(root, append(os.Environ(), "GOWORK=off"), "./...")
	require.NoError(t, err)
	return loaded
}

// sourcePosition names a source position relative to the server module
// root, which keeps violation messages stable across checkouts. Fixture
// positions keep their file name only.
func sourcePosition(pkg *packages.Package, pos token.Pos) string {
	position := pkg.Fset.Position(pos)
	name := filepath.Base(position.Filename)
	if root, err := filepath.Abs(".."); err == nil {
		if relative, err := filepath.Rel(root, position.Filename); err == nil && !strings.HasPrefix(relative, "..") {
			name = filepath.ToSlash(relative)
		}
	}
	return fmt.Sprintf("%s:%d", name, position.Line)
}
