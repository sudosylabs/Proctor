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

	"github.com/stretchr/testify/require"
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
					require.Equal(t, dir, path.Clean(dir))
					require.True(t, entries[path.Dir(dir)])
					require.False(t, entries[dir])
					entries[dir] = true
				}
				for _, file := range template.Files {
					require.Equal(t, file.Name, path.Clean(file.Name))
					require.True(t, entries[path.Dir(file.Name)])
					require.False(t, entries[file.Name])
					entries[file.Name] = true
				}
				for _, resource := range template.Resources {
					if resource.Media == "application/json" {
						require.True(t, json.Valid([]byte(resource.Content)), "invalid JSON resource: %s", resource.Name)
					}
				}
				require.NotEmpty(t, strings.TrimSpace(template.Instructions), "missing exercise instructions")
			}
			require.Len(t, titles, 4, "profile is missing a populated or empty-draft scenario")
		})
	}
}
