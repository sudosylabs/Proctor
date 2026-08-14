// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package workspace

import "github.com/sudosylabs/proctor/server/model"

// NormalizePath delegates to the domain's exact canonical POSIX-relative path
// contract. It exists as the child module's single path-authority seam.
func NormalizePath(value string) (string, error) { return model.NormalizeStarterWorkspacePath(value) }
