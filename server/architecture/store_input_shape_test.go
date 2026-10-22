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

// storeReadPrefixes name Store methods that only read. A read keyed by more
// than one value (Get(examID, attemptID)) is not a named operation.
var storeReadPrefixes = []string{"Get", "List", "Resolve", "Search", "Access", "Match", "Count", "Has", "Find"}

// storeReadMethods are reads whose names do not start with a read verb.
var storeReadMethods = map[string]string{
	"NativeDeliveryReceipt":   "reads one retained Desktop batch receipt",
	"NativeDeliveryStatus":    "reads the Desktop stream status",
	"BrowserDeliveryReceipts": "pages retained browser receipts",
	"CancellationRequested":   "observes the durable cancellation bit",
	"RecoverSecurityPolicy":   "reads the frozen security policy for recovery",
	"DeliveryBudget":          "reads the delivery budget snapshot",
}

// TestStoreMutationsTakeOneInput holds named Store operations to one input
// shape: a mutation takes the context, at most one input value (normally a
// pointer to a struct named for its entity and operation), and optionally a
// trailing *CommandIdempotency. Positional arguments let two values of one type
// swap silently and make every new field a signature change.
func TestStoreMutationsTakeOneInput(t *testing.T) {
	t.Parallel()
	for _, violation := range storeInputShapeViolations(typedServerPackages(t), serverModule) {
		assert.Fail(t, violation)
	}
}

func TestStoreInputShapeRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"store/store.go": `package store

import (
	"context"
	"time"
)

type CommandIdempotency struct{}
type SessionWithdrawal struct{}

type SessionStore interface {
	Withdraw(context.Context, string, string, time.Time) error
	Revoke(context.Context, *SessionWithdrawal, *CommandIdempotency) error
	Get(context.Context, string, string) (string, error)
	Archive(context.Context, string) error
}
`,
	})
	require.Equal(t, []string{
		`store.go:12: fixture/store.SessionStore.Withdraw takes 3 positional inputs; pass one input struct`,
	}, storeInputShapeViolations(loaded, "fixture"))
}

func storeInputShapeViolations(loaded []*packages.Package, module string) []string {
	var storePkg *packages.Package
	for _, pkg := range loaded {
		if pkg.PkgPath == module+"/store" {
			storePkg = pkg
		}
	}
	if storePkg == nil {
		return []string{"store package not loaded"}
	}
	isRead := func(name string) bool {
		if _, ok := storeReadMethods[name]; ok {
			return true
		}
		for _, prefix := range storeReadPrefixes {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return false
	}
	var violations []string
	scope := storePkg.Types.Scope()
	for _, name := range scope.Names() {
		object, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || !strings.HasSuffix(name, "Store") {
			continue
		}
		contract, ok := object.Type().Underlying().(*types.Interface)
		if !ok {
			continue
		}
		for index := range contract.NumExplicitMethods() {
			method := contract.ExplicitMethod(index)
			if isRead(method.Name()) {
				continue
			}
			params := method.Type().(*types.Signature).Params()
			inputs := 0
			for position := range params.Len() {
				value := types.TypeString(params.At(position).Type(), nil)
				if position == 0 && value == "context.Context" {
					continue
				}
				if position == params.Len()-1 && strings.HasSuffix(value, "/store.CommandIdempotency") {
					continue
				}
				inputs++
			}
			if inputs > 1 {
				violations = append(violations, fmt.Sprintf("%s: %s/store.%s.%s takes %d positional inputs; pass one input struct",
					sourcePosition(storePkg, method.Pos()), module, name, method.Name(), inputs))
			}
		}
	}
	sort.Strings(violations)
	return violations
}
