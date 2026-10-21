// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package apperror

import (
	"errors"
	"sort"

	"github.com/sudosylabs/proctor/server/store"
)

// StoreKind names one typed Store failure that a StoreRule can match.
type StoreKind uint8

const (
	// StoreNotFound matches *store.ErrNotFound; Entity matches its Resource.
	StoreNotFound StoreKind = iota + 1
	// StoreConflict matches *store.ErrConflict; Entity and Constraint match
	// its Resource and Constraint.
	StoreConflict
	// StoreReference matches *store.ErrReference; Entity and Constraint match
	// its Resource and Constraint.
	StoreReference
	// StoreInvalidInput matches *store.ErrInvalidInput; Entity and Constraint
	// match its Entity and the rejected constraint or column.
	StoreInvalidInput
	// StoreSentinel matches when errors.Is(err, rule.Sentinel).
	StoreSentinel
)

// StoreRule presents one Store failure as an application code. Empty Entity
// and Constraint match any value.
type StoreRule struct {
	Kind       StoreKind
	Entity     string
	Constraint string
	Sentinel   error
	Code       Code
}

// StoreTable is the declarative presentation of typed Store failures for one
// use-case family. Translate is the only Store-error translator: application
// code declares a table instead of writing a translation function.
//
// Translate passes an existing *Error through unchanged and presents
// idempotency conflicts and in-progress commands with the shared
// idempotency codes. It then checks, in order: Rules in declaration order;
// Constraints for a *store.ErrConflict constraint name; and the per-kind
// NotFound, Conflict, Reference, and Invalid codes. Anything else, and any
// kind whose code is empty, becomes Unavailable. Fields are attached to every
// failure the table produces, and InvalidField is the safe "field" value of
// an Invalid failure.
type StoreTable struct {
	Rules        []StoreRule
	Constraints  map[string]Code
	NotFound     Code
	Conflict     Code
	Reference    Code
	Invalid      Code
	InvalidField string
	Unavailable  Code
	Fields       map[string]string
}

// Translate presents err through the table. Callers translate only after the
// Store reported failure or returned no required result, so a nil err is
// presented as Unavailable.
func (t StoreTable) Translate(err error) error {
	if err == nil {
		return New(t.Unavailable).WithFields(t.Fields)
	}
	if _, ok := As(err); ok {
		return err
	}
	var keyConflict *store.ErrIdempotencyConflict
	if errors.As(err, &keyConflict) {
		return New("idempotency.conflict").Wrap(err)
	}
	var inProgress *store.ErrIdempotencyInProgress
	if errors.As(err, &inProgress) {
		return New("idempotency.in_progress").Wrap(err)
	}
	code, field := t.code(err)
	failure := New(code).WithFields(t.Fields)
	if field != "" {
		failure = failure.WithField("field", field)
	}
	return failure.Wrap(err)
}

func (t StoreTable) code(err error) (Code, string) {
	for _, rule := range t.Rules {
		if rule.matches(err) {
			return rule.Code, ""
		}
	}
	var conflict *store.ErrConflict
	var notFound *store.ErrNotFound
	var reference *store.ErrReference
	var invalid *store.ErrInvalidInput
	switch {
	case errors.As(err, &conflict):
		if code, ok := t.Constraints[conflict.Constraint]; ok {
			return code, ""
		}
		return t.or(t.Conflict), ""
	case errors.As(err, &notFound):
		return t.or(t.NotFound), ""
	case errors.As(err, &reference):
		return t.or(t.Reference), ""
	case errors.As(err, &invalid) && t.Invalid != "":
		return t.Invalid, t.InvalidField
	default:
		return t.Unavailable, ""
	}
}

func (t StoreTable) or(code Code) Code {
	if code == "" {
		return t.Unavailable
	}
	return code
}

// With returns a copy of the table whose additional rules are checked before
// the table's own rules.
func (t StoreTable) With(rules ...StoreRule) StoreTable {
	t.Rules = append(append([]StoreRule(nil), rules...), t.Rules...)
	return t
}

func (r StoreRule) matches(err error) bool {
	switch r.Kind {
	case StoreNotFound:
		var target *store.ErrNotFound
		return errors.As(err, &target) && r.selects(target.Resource, "")
	case StoreConflict:
		var target *store.ErrConflict
		return errors.As(err, &target) && r.selects(target.Resource, target.Constraint)
	case StoreReference:
		var target *store.ErrReference
		return errors.As(err, &target) && r.selects(target.Resource, target.Constraint)
	case StoreInvalidInput:
		var target *store.ErrInvalidInput
		return errors.As(err, &target) && r.selects(target.Entity, target.Field)
	case StoreSentinel:
		return r.Sentinel != nil && errors.Is(err, r.Sentinel)
	default:
		return false
	}
}

func (r StoreRule) selects(entity, constraint string) bool {
	return (r.Entity == "" || r.Entity == entity) &&
		(r.Constraint == "" || r.Constraint == constraint)
}

// Conflicts declares one StoreConflict rule per constraint name of one Store
// entity, for constraint names that are only meaningful on that entity.
// Rules are returned in constraint order so the table stays deterministic.
func Conflicts(entity string, codes map[string]Code) []StoreRule {
	constraints := make([]string, 0, len(codes))
	for constraint := range codes {
		constraints = append(constraints, constraint)
	}
	sort.Strings(constraints)
	rules := make([]StoreRule, 0, len(codes))
	for _, constraint := range constraints {
		rules = append(rules, StoreRule{Kind: StoreConflict, Entity: entity, Constraint: constraint, Code: codes[constraint]})
	}
	return rules
}

// AdministrationStoreTable is the shared presentation for academic and
// account administration resources: absence is the generic concealed
// resource.not_found, uniqueness and state conflicts are conflict unless a
// constraint names a more specific code, rejected values and references are
// invalid, and anything else is administration.unavailable. Every failure
// names the resource.
func AdministrationStoreTable(resource string, conflict, invalid Code, constraints map[string]Code) StoreTable {
	return StoreTable{
		Constraints: constraints,
		NotFound:    "resource.not_found",
		Conflict:    conflict,
		Reference:   invalid,
		Invalid:     invalid,
		Unavailable: "administration.unavailable",
		Fields:      map[string]string{"resource": resource},
	}
}
