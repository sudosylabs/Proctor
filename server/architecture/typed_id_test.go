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

// typedIDNames names the identifier concepts that keep a plain string
// wherever they appear in store and app, with the reason. Each is a reference
// whose entity type is chosen at runtime, a runtime token, or an identifier
// another party defines; none is a Proctor entity with its own typed model ID.
var typedIDNames = map[string]string{
	"ScopeID":                   "Role scope: RoleScopeType chooses the entity (Institution, Academic Unit, Class)",
	"scopeID":                   "Role scope: RoleScopeType chooses the entity (Institution, Academic Unit, Class)",
	"BatchScopeID":              "Role scope of the batch: RoleScopeType chooses the entity",
	"ResourceID":                "onboarding or batch row target: the row operation chooses the entity",
	"RelationshipID":            "batch or onboarding relationship: Affiliation, Academic Unit member or Class member by operation",
	"DestinationRelationshipID": "onboarding transfer destination: membership kind chosen by the row operation",
	"OwnerID":                   "Academic Period owner: Institution or Academic Unit by owner type",
	"TargetID":                  "Invitation target filter: matches the invitation target of any purpose",
	"RecordID":                  "retention record: RetentionExpiryKind chooses the entity",
	"SourceID":                  "delivery expiry source: Browser source session or native stream by family",
	"NodeID":                    "runtime cluster node token",
	"nodeID":                    "runtime cluster node token",
	"LeaseID":                   "runtime serving lease token",
	"leaseID":                   "runtime serving lease token",
	"KeyID":                     "configured encryption key identifier",
	"PrimaryKeyID":              "configured mail encryption key identifier",
	"RetiringKeyID":             "configured mail encryption key identifier",
	"retiringKeyID":             "configured mail encryption key identifier",
	"RequiredPrimaryKeyID":      "configured mail encryption key identifier",
	"ExpectedKeyID":             "configured mail encryption key identifier",
	"ProviderID":                "operator-configured identity provider (model.IsValidIdentityProviderID)",
	"providerID":                "operator-configured identity provider (model.IsValidIdentityProviderID)",
	"AuthenticationProviderID":  "operator-configured identity provider (model.IsValidIdentityProviderID)",
	"DeviceID":                  "Desktop-reported device identifier",
	"DesktopBuildID":            "Desktop build identifier defined by the compatibility policy",
	"ReleaseID":                 "Desktop native delivery release identifier",
	"MatrixID":                  "Desktop native delivery build matrix identifier",
	"PreflightID":               "Desktop agreement identifier (model.IsValidAgreementID)",
	"StreamID":                  "Desktop agreement identifier (model.IsValidAgreementID)",
	"LoginID":                   "the username or email typed at login, not a stored identifier",
	"MessageID":                 "RFC 5322 Message-ID header",
	"ContentID":                 "MIME Content-ID of an inline asset",
	"InstallationID":            "public installation identity token",
}

// typedIDAllowlist names individual sites that keep a plain string identifier,
// keyed by qualified declaration and name, with the reason.
var typedIDAllowlist = map[string]string{
	"store.ErrNotFound ID":                                      "generic not-found error over any resource",
	"store.NewErrNotFound id":                                   "generic not-found error over any resource",
	"store.FilePurgeClaim ID":                                   "purge tombstone identity, not a model entity",
	"store.MailRekeyTarget ID":                                  "mail rekey target: delivery or occurrence by target kind",
	"store.MailRekeyReplacement ID":                             "mail rekey target: delivery or occurrence by target kind",
	"store.MailRekeyTargetPageRequest AfterID":                  "mail rekey target cursor: delivery or occurrence",
	"app/jobhandler.MailRekeyCheckpoint AfterID":                "mail rekey target cursor: delivery or occurrence",
	"store.AcademicNameListOptions AfterID":                     "cursor shared by every academic name listing",
	"app.NamePageQuery AfterID":                                 "cursor shared by every academic name listing",
	"store.RetentionExpiryListOptions AfterID":                  "retention record cursor: RetentionExpiryKind chooses the entity",
	"app/jobhandler.RetentionExpiryCommand AfterID":             "retention record cursor: RetentionExpiryKind chooses the entity",
	"app/jobhandler.RetentionExpiryCheckpoint AfterID":          "retention record cursor: RetentionExpiryKind chooses the entity",
	"app.retentionJobAudit.BeginRetentionExpiry id":             "retention record: RetentionExpiryKind chooses the entity",
	"store.ExamAttemptStore.ResolveSecurityPreflightSitting id": "Desktop agreement identifier (model.IsValidAgreementID)",
	"app.externalProviderSource.Provider id":                    "operator-configured identity provider",
	"app/realtime.RealtimeEvent ID":                             "runtime event identity for peer deduplication",
}

// TestEntityIdentifiersAreTyped keeps entity identifiers typed at the Store
// and application boundary: in package store and in app and its child
// packages, an exported struct field, a parameter or named result of an
// exported function or of an exported method of any type, or a parameter or
// result of any
// interface method (unnamed ones take the implementation's name) whose name
// ends in ID or IDs (or is id or ids) is a typed
// model ID, never string, *string, []string, or a map keyed by string.
func TestEntityIdentifiersAreTyped(t *testing.T) {
	t.Parallel()
	for _, violation := range typedIDViolations(typedServerPackages(t), serverModule, typedIDNames, typedIDAllowlist) {
		assert.Fail(t, violation)
	}
}

func typedIDViolations(loaded []*packages.Package, module string, names, allowed map[string]string) []string {
	var violations []string
	report := func(pkg *packages.Package, object types.Object, qualified, name string, value types.Type) {
		if _, ok := names[name]; ok || !identifierName(name) || !plainStringIdentifier(value) {
			return
		}
		key := strings.TrimPrefix(qualified, module+"/") + " " + name
		if _, ok := allowed[key]; ok {
			return
		}
		violations = append(violations, fmt.Sprintf("%s: %s %s is %s; use a typed model ID",
			sourcePosition(pkg, object.Pos()), qualified, name, types.TypeString(value, types.RelativeTo(pkg.Types))))
	}
	signature := func(pkg *packages.Package, function *types.Func, qualified string, implementation *types.Signature) {
		value := function.Type().(*types.Signature)
		for index, tuple := range []*types.Tuple{value.Params(), value.Results()} {
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
				report(pkg, function, qualified, name, variable.Type())
			}
		}
	}
	storeImplementations := sqlstoreImplementations(loaded, module)
	for _, pkg := range loaded {
		if pkg.PkgPath != module+"/store" && pkg.PkgPath != module+"/app" && !strings.HasPrefix(pkg.PkgPath, module+"/app/") {
			continue
		}
		scope := pkg.Types.Scope()
		candidates := packageImplementations(pkg)
		if pkg.PkgPath == module+"/store" {
			candidates = storeImplementations
		}
		for _, name := range scope.Names() {
			switch object := scope.Lookup(name).(type) {
			case *types.TypeName:
				if object.IsAlias() {
					continue
				}
				switch underlying := object.Type().Underlying().(type) {
				case *types.Struct:
					if !object.Exported() {
						break
					}
					for field := range underlying.Fields() {
						if field.Exported() {
							report(pkg, field, pkg.PkgPath+"."+name, field.Name(), field.Type())
						}
					}
				case *types.Interface:
					for method := range underlying.ExplicitMethods() {
						signature(pkg, method, pkg.PkgPath+"."+name+"."+method.Name(),
							implementationSignature(candidates, underlying, method.Name()))
					}
				}
				if named, ok := object.Type().(*types.Named); ok {
					if _, isInterface := named.Underlying().(*types.Interface); !isInterface {
						for method := range named.Methods() {
							if method.Exported() {
								signature(pkg, method, pkg.PkgPath+"."+name+"."+method.Name(), nil)
							}
						}
					}
				}
			case *types.Func:
				if object.Exported() {
					signature(pkg, object, pkg.PkgPath+"."+name, nil)
				}
			}
		}
	}
	sort.Strings(violations)
	return violations
}

// packageImplementations returns the named non-interface types declared in
// pkg, the candidates that name its interfaces' unnamed parameters.
func packageImplementations(pkg *packages.Package) []types.Type {
	var found []types.Type
	scope := pkg.Types.Scope()
	for _, name := range scope.Names() {
		if typeName, ok := scope.Lookup(name).(*types.TypeName); ok && !typeName.IsAlias() {
			if _, isInterface := typeName.Type().Underlying().(*types.Interface); !isInterface {
				found = append(found, typeName.Type())
			}
		}
	}
	return found
}

// identifierName reports a name that denotes an identifier: id or ids, or a
// name ending in ID or IDs.
func identifierName(name string) bool {
	switch name {
	case "id", "ids", "ID", "IDs":
		return true
	}
	return strings.HasSuffix(name, "ID") || strings.HasSuffix(name, "IDs")
}

// plainStringIdentifier reports string, *string, []string, an array of
// strings, or a map keyed by string: an identifier without its entity type.
func plainStringIdentifier(value types.Type) bool {
	for {
		switch typed := types.Unalias(value).(type) {
		case *types.Pointer:
			value = typed.Elem()
		case *types.Slice:
			value = typed.Elem()
		case *types.Array:
			value = typed.Elem()
		case *types.Map:
			value = typed.Key()
		case *types.Basic:
			return typed.Kind() == types.String
		default:
			return false
		}
	}
}

func TestTypedIDRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"model/id.go": `package model

type UserID string

type SessionID string
`,
		"store/store.go": `package store

import (
	"context"

	"fixture/model"
)

type RoleBindingCreation struct {
	UserID  string
	RoleID  *string
	ScopeID string
	Typed   model.UserID
	Name    string
}

type RoleBindingStore interface {
	Create(context.Context, *RoleBindingCreation) error
	ListByUser(ctx context.Context, userID string) error
	Revoke(context.Context, []string) error
}

func NewErrNotFound(resource, id string) error { return nil }
`,
		"store/sqlstore/role_binding.go": `package sqlstore

import (
	"context"

	"fixture/store"
)

type SQLRoleBindingStore struct{}

func (SQLRoleBindingStore) Create(context.Context, *store.RoleBindingCreation) error { return nil }

func (SQLRoleBindingStore) ListByUser(context.Context, string) error { return nil }

func (SQLRoleBindingStore) Revoke(_ context.Context, sessionIDs []string) error { return nil }
`,
		"app/role_binding.go": `package app

import (
	"context"

	"fixture/model"
)

type CreateRoleBindingCommand struct {
	UserID   model.UserID
	IDs      map[string]bool
	internal string
}

type effects interface {
	SessionsRevoked(context.Context, model.UserID, []string)
}

type authorizer struct{}

func (authorizer) AuthorizeUserRead(_ context.Context, userID string) error { return nil }

func (authorizer) helper(userID string) {}

type recorder struct{}

func (recorder) SessionsRevoked(_ context.Context, userID model.UserID, sessionIDs []string) {}
`,
	})
	require.Equal(t, []string{
		"role_binding.go:11: fixture/app.CreateRoleBindingCommand IDs is map[string]bool; use a typed model ID",
		"role_binding.go:16: fixture/app.effects.SessionsRevoked sessionIDs is []string; use a typed model ID",
		"role_binding.go:21: fixture/app.authorizer.AuthorizeUserRead userID is string; use a typed model ID",
		"role_binding.go:27: fixture/app.recorder.SessionsRevoked sessionIDs is []string; use a typed model ID",
		"store.go:10: fixture/store.RoleBindingCreation UserID is string; use a typed model ID",
		"store.go:11: fixture/store.RoleBindingCreation RoleID is *string; use a typed model ID",
		"store.go:19: fixture/store.RoleBindingStore.ListByUser userID is string; use a typed model ID",
		"store.go:20: fixture/store.RoleBindingStore.Revoke sessionIDs is []string; use a typed model ID",
	}, typedIDViolations(loaded, "fixture", map[string]string{"ScopeID": "Role scope"},
		map[string]string{"store.NewErrNotFound id": "generic error"}))
}
