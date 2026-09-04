// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package architecture_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const licenseHeaderRule = "---------------------------------------------------------------------------------------------"

var (
	apacheMattermostFiles = map[string]struct{}{
		"model/academic_unit_member.go":  {},
		"model/audit_event.go":           {},
		"model/authorization.go":         {},
		"model/class_member.go":          {},
		"model/client.go":                {},
		"model/mfa.go":                   {},
		"model/personal_access_token.go": {},
		"model/role.go":                  {},
		"model/session.go":               {},
		"model/user.go":                  {},
		"model/user_token.go":            {},
		"model/utils.go":                 {},
	}
	bsdGoFiles = map[string]struct{}{
		"internal/autocert/autocert.go": {},
		"internal/autocert/cache.go":    {},
		"internal/autocert/renewal.go":  {},
	}
)

func TestServerCodeFilesHaveCanonicalLicenseHeaders(t *testing.T) {
	moduleRoot := serverModuleRoot(t)
	candidates := serverModuleCandidates(t, moduleRoot)
	seenExceptions := make(map[string]struct{})
	var issues []string

	for _, name := range candidates {
		style, ok := serverCodeHeaderStyle(name)
		if !ok {
			continue
		}

		contents, err := os.ReadFile(filepath.Join(moduleRoot, filepath.FromSlash(name)))
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: read license header: %v", name, err))
			continue
		}

		licenseID := "AGPL-3.0-only"
		mattermost := bytes.Contains(bytes.ToLower(contents), []byte("adapted from "+"mattermost"))
		if _, ok := apacheMattermostFiles[name]; ok {
			licenseID = "Apache-2.0"
			seenExceptions[name] = struct{}{}
			if !mattermost {
				issues = append(issues, name+": Apache exception requires an in-file Mattermost adaptation notice")
			}
		}
		if _, ok := bsdGoFiles[name]; ok {
			licenseID = "BSD-3-Clause"
			mattermost = false
			seenExceptions[name] = struct{}{}
		}

		expected := canonicalServerLicenseHeader(style, licenseID, mattermost)
		contents = trimGoBuildConstraint(contents, name)
		if !bytes.HasPrefix(contents, []byte(expected)) {
			origin := "Sudosy"
			if mattermost {
				origin = "Mattermost-derived"
			}
			issues = append(issues, fmt.Sprintf("%s: expected canonical %s %s header", name, origin, licenseID))
		}
	}

	for name := range apacheMattermostFiles {
		if _, ok := seenExceptions[name]; !ok {
			issues = append(issues, name+": configured Apache exception is missing from the server source set")
		}
	}
	for name := range bsdGoFiles {
		if _, ok := seenExceptions[name]; !ok {
			issues = append(issues, name+": configured BSD source is missing from the server source set")
		}
	}

	sort.Strings(issues)
	for _, issue := range issues {
		t.Error(issue)
	}
}

func TestServerUsesCanonicalCopyrightHolderName(t *testing.T) {
	moduleRoot := serverModuleRoot(t)
	var issues []string
	for _, name := range serverModuleCandidates(t, moduleRoot) {
		contents, err := os.ReadFile(filepath.Join(moduleRoot, filepath.FromSlash(name)))
		if err != nil {
			issues = append(issues, fmt.Sprintf("%s: read copyright text: %v", name, err))
			continue
		}
		if bytes.Contains(contents, []byte("Sudo"+"Sylabs")) ||
			bytes.Contains(contents, []byte("Sudo Systems"+" Labs Ltd")) {
			issues = append(issues, name+": copyright holder must be written as Sudosy Labs")
		}
	}

	sort.Strings(issues)
	for _, issue := range issues {
		t.Error(issue)
	}
}

func serverModuleRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), ".."))
}

func serverModuleCandidates(t *testing.T, moduleRoot string) []string {
	t.Helper()
	if repositoryRoot, ok := documentationRepositoryRoot(t); ok {
		var names []string
		for name := range documentationCandidates(t, repositoryRoot) {
			if strings.HasPrefix(name, "server/") {
				names = append(names, strings.TrimPrefix(name, "server/"))
			}
		}
		sort.Strings(names)
		return names
	}

	var names []string
	err := filepath.WalkDir(moduleRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".build", ".git", "dist", "node_modules":
				if path != moduleRoot {
					return filepath.SkipDir
				}
			}
			return nil
		}
		relative, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if name != "config/config.json" {
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk server source: %v", err)
	}
	sort.Strings(names)
	return names
}

func serverCodeHeaderStyle(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go", ".mjs", ".mod":
		return "slash", true
	case ".sql":
		return "sql", true
	case ".yaml", ".yml":
		return "hash", true
	case ".mjml", ".html":
		return "html", true
	case ".txt":
		if strings.HasPrefix(name, "templates/") {
			return "go-template", true
		}
	}
	if filepath.Base(name) == "Makefile" {
		return "hash", true
	}
	return "", false
}

func canonicalServerLicenseHeader(style, licenseID string, mattermost bool) string {
	lines := []string{licenseHeaderRule}
	switch licenseID {
	case "Apache-2.0":
		lines = append(lines,
			"Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.",
			"Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.",
			"Licensed under the Apache License, Version 2.0.",
			"See LICENSES/Apache-2.0.txt and NOTICE in the server module root for",
			"license and attribution information.",
			"SPDX-License-Identifier: Apache-2.0",
		)
	case "BSD-3-Clause":
		lines = append(lines,
			"Copyright 2016 The Go Authors. All rights reserved.",
			"Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.",
			"Use of this source code is governed by a BSD-style",
			"license that can be found in the LICENSE file.",
			"SPDX-License-Identifier: BSD-3-Clause",
		)
	default:
		if mattermost {
			lines = append(lines,
				"Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.",
				"Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.",
			)
		} else {
			lines = append(lines, "Copyright (c) 2026 Sudosy Labs. All rights reserved.")
		}
		lines = append(lines,
			"Licensed under the GNU Affero General Public License, version 3 only.",
			"See LICENSE in the server module root for license information.",
			"SPDX-License-Identifier: AGPL-3.0-only",
		)
	}
	lines = append(lines, licenseHeaderRule)

	switch style {
	case "html":
		return "<!--\n" + strings.Join(lines, "\n") + "\n-->"
	case "go-template":
		return "{{/*\n" + strings.Join(lines, "\n") + "\n*/}}"
	case "hash":
		return prefixedLicenseHeader(lines, "#")
	case "sql":
		return prefixedLicenseHeader(lines, "--")
	default:
		return prefixedLicenseHeader(lines, "//")
	}
}

func prefixedLicenseHeader(lines []string, prefix string) string {
	formatted := make([]string, 0, len(lines))
	for _, line := range lines {
		formatted = append(formatted, prefix+" "+line)
	}
	return strings.Join(formatted, "\n")
}

func trimGoBuildConstraint(contents []byte, name string) []byte {
	if filepath.Ext(name) != ".go" || !bytes.HasPrefix(contents, []byte("//go:build ")) {
		return contents
	}
	separator := bytes.Index(contents, []byte("\n\n"))
	if separator < 0 {
		return contents
	}
	return contents[separator+2:]
}
