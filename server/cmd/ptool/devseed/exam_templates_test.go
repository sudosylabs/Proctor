// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package devseed

import (
	"encoding/json"
	"path"
	"strings"
	"testing"
)

func TestExamProfilesContainCoherentWorkspaceTrees(t *testing.T) {
	t.Parallel()
	for _, profile := range []string{"small", "desktop", "large"} {
		t.Run(profile, func(t *testing.T) {
			titles := map[string]bool{}
			for i := 0; i < sizeFor(profile).Exams; i++ {
				template := examinationTemplate(i)
				titles[template.Title] = true
				entries := map[string]bool{".": true}
				for _, dir := range template.Directories {
					if path.Clean(dir) != dir || !entries[path.Dir(dir)] || entries[dir] {
						t.Fatalf("invalid directory order or path: %s", dir)
					}
					entries[dir] = true
				}
				for _, file := range template.Files {
					if path.Clean(file.Name) != file.Name || !entries[path.Dir(file.Name)] || entries[file.Name] {
						t.Fatalf("file has no parent or collides: %s", file.Name)
					}
					entries[file.Name] = true
				}
				for _, resource := range template.Resources {
					if resource.Media == "application/json" && !json.Valid([]byte(resource.Content)) {
						t.Fatalf("invalid JSON resource: %s", resource.Name)
					}
				}
				if strings.TrimSpace(template.Instructions) == "" {
					t.Fatal("missing exercise instructions")
				}
			}
			if len(titles) != 4 {
				t.Fatalf("profile is missing a populated or empty-draft scenario: %d", len(titles))
			}
		})
	}
}
