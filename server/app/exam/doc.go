// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package exam owns examination use-case policy behind the parent app.App
// facade. An Exam is the stable authoring identity in one Academic Unit; its
// single Draft is mutable authoring state, an Exam Revision is an immutable
// published snapshot, and Exam Managers share responsibility with its Owner.
// A Sitting delivers one Revision to one Class. An Attempt is one candidate's
// lifecycle in that Sitting, Participation generations fence live access, a
// Workspace is mutable candidate code, and a Submission freezes its accepted
// result. Resources and a Starter Workspace are read-only published inputs;
// Integrity evidence, flags, and Review record observed facts and a manager's
// disposition without performing academic grading.
//
// The currently implemented slices coordinate Exam identity, its Draft and
// creator Manager, shipped policy defaults, presence-aware title and Markdown
// editing, authorization, audit, atomic Store mutations, idempotent replay,
// bounded exact retrieval and keyset-paginated catalog discovery, archive
// lifecycle, immutable Revision publication, bounded Revision metadata
// discovery, pre-open Sitting scheduling, rescheduling, discovery and
// cancellation, and safe post-commit effects. Archived Exams remain readable
// but reject new authoring mutations.
//
// The package does not own HTTP or WebSocket contracts, SQL, file bytes,
// Attempt admission, Sitting opening/closing jobs, integrity evaluation, or
// grading. It may depend inward on model and bounded store contracts plus
// consumer-owned ports implemented by the parent application composition; it
// never imports its parent, transports, concrete persistence, VFS, cluster
// adapters, or platform services.
package exam
