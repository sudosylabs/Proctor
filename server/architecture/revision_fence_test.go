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
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

var revisionFenceField = regexp.MustCompile(`^Expected([A-Z][A-Za-z]*)?Revision$`)

// TestStoreRevisionFencesNameTheirResource holds Store inputs to one revision
// vocabulary: ExpectedRevision fences the entity the operation acts on, and
// Expected<Resource>Revision names a fence on a different resource. The
// operation's own entity is the fenced resource named last in the input's type
// name (ExamIntegrityReviewDecisionMutation acts on a Decision), so a struct
// without ExpectedRevision whose type name contains a fence's resource spells
// the unqualified fence another way; so does an "ExpectedCurrentRevision".
func TestStoreRevisionFencesNameTheirResource(t *testing.T) {
	t.Parallel()
	for _, violation := range revisionFenceViolations(typedServerPackages(t), serverModule) {
		assert.Fail(t, violation)
	}
}

func TestRevisionFenceRuleRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	loaded := loadFixturePackages(t, map[string]string{
		"store/store.go": `package store

type ExamAttemptReallow struct{ ExpectedAttemptRevision int64 }
type ExamIntegrityReviewDecisionMutation struct{ ExpectedReviewRevision, ExpectedDecisionRevision int64 }
type InvitationReplacement struct{ ExpectedCurrentRevision int64 }
type ProfilePicturePublication struct{ ExpectedUserRevision int64 }
type ExamRecordsReviewWaiver struct{ ExpectedRevision, ExpectedReviewRevision int64 }
type ExamAttemptManualSuspension struct{ ExpectedRevision int64 }
`,
	})
	require.Equal(t, []string{
		`store.go:3: fixture/store.ExamAttemptReallow.ExpectedAttemptRevision fences the input's own Attempt; name it ExpectedRevision`,
		`store.go:4: fixture/store.ExamIntegrityReviewDecisionMutation.ExpectedDecisionRevision fences the input's own Decision; name it ExpectedRevision`,
		`store.go:5: fixture/store.InvitationReplacement.ExpectedCurrentRevision fences the input's own entity; name it ExpectedRevision`,
	}, revisionFenceViolations(loaded, "fixture"))
}

func revisionFenceViolations(loaded []*packages.Package, module string) []string {
	var storePkg *packages.Package
	for _, pkg := range loaded {
		if pkg.PkgPath == module+"/store" {
			storePkg = pkg
		}
	}
	if storePkg == nil {
		return []string{"store package not loaded"}
	}
	var violations []string
	scope := storePkg.Types.Scope()
	for _, name := range scope.Names() {
		object, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		structure, ok := object.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		hasUnqualified := false
		own, ownIndex := (*types.Var)(nil), -1
		for index := range structure.NumFields() {
			field := structure.Field(index)
			match := revisionFenceField.FindStringSubmatch(field.Name())
			if match == nil {
				continue
			}
			qualifier := match[1]
			where := fmt.Sprintf("%s: %s/store.%s.%s", sourcePosition(storePkg, field.Pos()), module, name, field.Name())
			switch {
			case qualifier == "":
				hasUnqualified = true
			case qualifier == "Current":
				violations = append(violations, where+" fences the input's own entity; name it ExpectedRevision")
			default:
				if position := strings.LastIndex(name, qualifier); position > ownIndex {
					own, ownIndex = field, position
				}
			}
		}
		if own != nil && !hasUnqualified {
			qualifier := revisionFenceField.FindStringSubmatch(own.Name())[1]
			violations = append(violations, fmt.Sprintf("%s: %s/store.%s.%s fences the input's own %s; name it ExpectedRevision",
				sourcePosition(storePkg, own.Pos()), module, name, own.Name(), qualifier))
		}
	}
	sort.Strings(violations)
	return violations
}
