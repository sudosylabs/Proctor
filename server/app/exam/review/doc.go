// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package review owns post-Submission integrity inspection, revision-fenced
// manager decisions and draft remarks, immutable Review finalization, explicit
// student-result release, manager inspection of bounded late discrepancies,
// and the minimal released candidate projection. It depends only on model,
// the bounded Integrity Review Store contract, the shared safemarkdown leaf,
// and narrow authorization, audit, and realtime ports. It does not own
// integrity collection, Submission sealing/content, HTTP, SQL, or grading.
package review
