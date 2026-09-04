// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package workspace

import "github.com/sudosylabs/proctor/server/model"

// NormalizePath delegates to the domain's exact canonical POSIX-relative path
// contract. It exists as the child module's single path-authority seam.
func NormalizePath(value string) (string, error) { return model.NormalizeStarterWorkspacePath(value) }
