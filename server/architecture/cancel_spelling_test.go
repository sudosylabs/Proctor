// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Proctor spells cancellation's inflections with one l: canceled, canceling.
// The noun "cancellation" keeps its double l, as in every English dictionary.
// The rule covers identifiers, SQL, persisted values, wire strings, mail
// templates, localized copy, and documentation alike, so a value never needs
// a second spelling where it crosses a boundary.
var doubleLCancelSpelling = regexp.MustCompile(`(?i)cancell(ed|ing)`)

// cancelSpellingExempt lists paths whose spelling Proctor does not own: the
// release history records names as they were, the independently versioned
// reusable modules are pinned by content, and CI calls GitHub's cancelled()
// function.
var cancelSpellingExempt = []string{
	"CHANGELOG.md",
	".github/",
	"packages/",
	"docs/api/reference/",
}

func TestCancellationIsSpelledWithOneL(t *testing.T) {
	root, ok := documentationRepositoryRoot(t)
	if !ok {
		t.Skip("requires the repository checkout")
	}
	names := make([]string, 0)
	for name := range documentationCandidates(t, root) {
		names = append(names, name)
	}
	sort.Strings(names)
	var violations []string
	for _, name := range names {
		if cancelSpellingIsExempt(name) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		contents, err := os.ReadFile(path)
		require.NoError(t, err)
		violations = append(violations, cancelSpellingViolations(name, contents)...)
	}
	require.Empty(t, violations, "spell cancellation's inflections canceled and canceling")
}

func TestCancelSpellingCheckRejectsPlantedViolations(t *testing.T) {
	t.Parallel()
	got := cancelSpellingViolations("server/model/sample.go",
		[]byte("const StateCanceled = \"canceled\"\n// A cancellation is fine.\nstate IN ('cancelled')\nCANCELLING\n"))
	require.Equal(t, []string{
		"server/model/sample.go:3: state IN ('cancelled')",
		"server/model/sample.go:4: CANCELLING",
	}, got)
	require.Empty(t, cancelSpellingViolations("server/templates/exam.sitting_canceled.html", []byte("Cancellation notice")))
	require.Equal(t, []string{"server/templates/exam.sitting_cancelled.mjml: file name"},
		cancelSpellingViolations("server/templates/exam.sitting_cancelled.mjml", nil))
	require.True(t, cancelSpellingIsExempt("packages/mail/errors_test.go"))
	require.False(t, cancelSpellingIsExempt("server/i18n/en.json"))
}

func cancelSpellingIsExempt(name string) bool {
	for _, prefix := range cancelSpellingExempt {
		if name == prefix || strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return name == "server/architecture/cancel_spelling_test.go"
}

func cancelSpellingViolations(name string, contents []byte) []string {
	var violations []string
	if doubleLCancelSpelling.MatchString(name) {
		violations = append(violations, name+": file name")
	}
	if bytes.IndexByte(contents, 0) >= 0 {
		return violations
	}
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		if text := scanner.Text(); doubleLCancelSpelling.MatchString(text) {
			violations = append(violations, fmt.Sprintf("%s:%d: %s", name, line, strings.TrimSpace(text)))
		}
	}
	return violations
}
