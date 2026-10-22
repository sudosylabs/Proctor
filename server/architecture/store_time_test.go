// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"fmt"
	"go/types"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// TestStoreInstantsAreTimes keeps one instant type at the Store boundary: a
// struct field, function or interface method parameter, or result in package
// store that names an instant (it ends in At, such as CreatedAt, AuditAt or
// auditAt; it is now; or it ends in Millis, such as nowMillis) is a time.Time or
// model.OptionalTime, never Unix milliseconds or another integer. An unnamed
// interface parameter takes its name from the sqlstore implementation.
func TestStoreInstantsAreTimes(t *testing.T) {
	t.Parallel()
	for _, violation := range storeInstantViolations(typedServerPackages(t), serverModule) {
		assert.Fail(t, violation)
	}
}

func TestStoreInstantRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"store/store.go": `package store

import (
	"context"
	"time"
)

type Millis int64

type ClassArchive struct {
	ArchiveAt  int64
	AuditAt    time.Time
	EndAt      *Millis
	ReminderAt []uint32
	Format     int64
	Attempts   int
}

type ClassStore interface {
	Archive(ctx context.Context, input *ClassArchive) error
	Touch(ctx context.Context, touchedAt int64) (expiresAt int64, err error)
	Seen(ctx context.Context, seenAt time.Time) error
	Renew(context.Context, int64, int64) error
	Sweep(ctx context.Context, nowMillis int64, now int64, DurationMilliseconds int64) error
}

func expiry(issuedAt int64) time.Time { return time.UnixMilli(issuedAt) }
`,
		"store/sqlstore/row.go": `package sqlstore

import (
	"context"
	"time"

	"fixture/store"
)

type row struct{ CreatedAt int64 }

type SQLClassStore struct{}

func (SQLClassStore) Archive(context.Context, *store.ClassArchive) error { return nil }

func (SQLClassStore) Touch(_ context.Context, touchedAt int64) (int64, error) { return touchedAt, nil }

func (SQLClassStore) Seen(context.Context, time.Time) error { return nil }

func (SQLClassStore) Renew(_ context.Context, revision, renewedAt int64) error { return nil }

func (SQLClassStore) Sweep(context.Context, int64, int64, int64) error { return nil }
`,
	})
	require.Equal(t, []string{
		"store.go:11: fixture/store.ClassArchive.ArchiveAt is int64; Store instants are time.Time",
		"store.go:13: fixture/store.ClassArchive.EndAt is *fixture/store.Millis; Store instants are time.Time",
		"store.go:14: fixture/store.ClassArchive.ReminderAt is []uint32; Store instants are time.Time",
		"store.go:21: fixture/store.ClassStore.Touch expiresAt is int64; Store instants are time.Time",
		"store.go:21: fixture/store.ClassStore.Touch touchedAt is int64; Store instants are time.Time",
		"store.go:23: fixture/store.ClassStore.Renew renewedAt is int64; Store instants are time.Time",
		"store.go:24: fixture/store.ClassStore.Sweep now is int64; Store instants are time.Time",
		"store.go:24: fixture/store.ClassStore.Sweep nowMillis is int64; Store instants are time.Time",
		"store.go:27: fixture/store.expiry issuedAt is int64; Store instants are time.Time",
	}, storeInstantViolations(loaded, "fixture"))
}

func storeInstantViolations(loaded []*packages.Package, module string) []string {
	var violations []string
	for _, pkg := range loaded {
		if pkg.PkgPath != module+"/store" {
			continue
		}
		implementations := sqlstoreImplementations(loaded, module)
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			switch object := scope.Lookup(name).(type) {
			case *types.TypeName:
				switch underlying := object.Type().Underlying().(type) {
				case *types.Struct:
					for field := range underlying.Fields() {
						if instantName(field.Name()) && integerInstant(field.Type()) {
							violations = append(violations, fmt.Sprintf("%s: %s.%s.%s is %s; Store instants are time.Time",
								sourcePosition(pkg, field.Pos()), pkg.PkgPath, name, field.Name(), types.TypeString(field.Type(), nil)))
						}
					}
				case *types.Interface:
					for method := range underlying.ExplicitMethods() {
						violations = append(violations, signatureInstantViolations(pkg, pkg.PkgPath+"."+name+"."+method.Name(), method,
							implementationSignature(implementations, underlying, method.Name()))...)
					}
				}
			case *types.Func:
				violations = append(violations, signatureInstantViolations(pkg, pkg.PkgPath+"."+name, object, nil)...)
			}
		}
	}
	sort.Strings(violations)
	return violations
}

// signatureInstantViolations checks one signature; implementation, when
// present, names the parameters and results the declaration leaves unnamed.
func signatureInstantViolations(pkg *packages.Package, qualified string, function *types.Func, implementation *types.Signature) []string {
	signature := function.Type().(*types.Signature)
	var violations []string
	for index, tuple := range []*types.Tuple{signature.Params(), signature.Results()} {
		for position := range tuple.Len() {
			variable := tuple.At(position)
			name := variable.Name()
			if (name == "" || name == "_") && implementation != nil {
				named := implementation.Params()
				if index == 1 {
					named = implementation.Results()
				}
				if position < named.Len() {
					name = named.At(position).Name()
				}
			}
			if instantName(name) && integerInstant(variable.Type()) {
				violations = append(violations, fmt.Sprintf("%s: %s %s is %s; Store instants are time.Time",
					sourcePosition(pkg, function.Pos()), qualified, name, types.TypeString(variable.Type(), nil)))
			}
		}
	}
	return violations
}

// sqlstoreImplementations returns the named types of the sqlstore package in
// a stable order, the candidates that name a contract's unnamed parameters.
func sqlstoreImplementations(loaded []*packages.Package, module string) []types.Type {
	var found []types.Type
	for _, pkg := range loaded {
		if pkg.PkgPath != module+"/store/sqlstore" {
			continue
		}
		scope := pkg.Types.Scope()
		for _, name := range scope.Names() {
			if typeName, ok := scope.Lookup(name).(*types.TypeName); ok && !typeName.IsAlias() {
				if _, isInterface := typeName.Type().Underlying().(*types.Interface); !isInterface {
					found = append(found, typeName.Type())
				}
			}
		}
	}
	return found
}

// implementationSignature returns the signature of method on the first
// sqlstore type (by value or pointer) that implements contract.
func implementationSignature(candidates []types.Type, contract *types.Interface, method string) *types.Signature {
	for _, candidate := range candidates {
		for _, receiver := range []types.Type{candidate, types.NewPointer(candidate)} {
			if !types.Implements(receiver, contract) {
				continue
			}
			object, _, _ := types.LookupFieldOrMethod(receiver, true, nil, method)
			if function, ok := object.(*types.Func); ok {
				return function.Type().(*types.Signature)
			}
		}
	}
	return nil
}

// instantName reports a name that denotes an instant: it ends in At after a
// lower-case letter, is exactly at/At or now/Now, or ends in Millis (a Unix
// millisecond instant; a duration is spelled Milliseconds or time.Duration).
func instantName(name string) bool {
	switch name {
	case "At", "at", "Now", "now":
		return true
	}
	if strings.HasSuffix(name, "Millis") || name == "millis" {
		return true
	}
	return strings.HasSuffix(name, "At") && len(name) > 2 && name[len(name)-3] >= 'a' && name[len(name)-3] <= 'z'
}

func integerInstant(value types.Type) bool {
	for {
		switch typed := value.(type) {
		case *types.Pointer:
			value = typed.Elem()
		case *types.Slice:
			value = typed.Elem()
		case *types.Array:
			value = typed.Elem()
		default:
			basic, ok := value.Underlying().(*types.Basic)
			return ok && basic.Info()&types.IsInteger != 0
		}
	}
}
