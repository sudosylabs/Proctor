// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package workspace owns the bounded logical hierarchy and opaque-content
// mechanics of an Exam Draft's Starter Workspace. It does not own Attempt
// workspaces, Exam Resources, publication, HTTP, SQL, VFS selection, or
// infrastructure lifecycle. Logical paths are PostgreSQL metadata and never
// object-store keys. The parent exam application remains the public facade.
package workspace
