// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package review owns post-Submission integrity inspection, revision-fenced
// manager decisions and draft remarks, immutable Review finalization, explicit
// student-result release, manager inspection of bounded late discrepancies,
// and the minimal released candidate projection. It depends only on model,
// the bounded Integrity Review Store contract, the shared safemarkdown leaf,
// and narrow authorization, audit, and realtime ports. It does not own
// integrity collection, Submission sealing/content, HTTP, SQL, or grading.
package review
