// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package filecontent owns Proctor's backend-neutral storage mechanics for
// immutable File Revision bytes. It derives private storage keys, stages and
// opens exact stored renditions, and removes unreferenced or abandoned content
// through domain identities and the reusable VFS contract. Purpose-specific
// validation and rendition creation join this package with their working
// pipelines; callers never receive raw VFS operations.
//
// The package does not own File Entry or File Revision lifecycle,
// authorization, audit, retention decisions, search eligibility, application
// events, or live Attempt Workspace synchronization. PostgreSQL publication by
// an owning application use case remains the only visibility boundary; a VFS
// write alone never makes content discoverable.
//
// The storage core depends only on server/model and the reusable VFS contract.
// A purpose-specific codec becomes an allowed dependency only in the same
// change that moves its working pipeline here and narrows the architecture
// allowlist to that codec. Concrete VFS backends, persistence, transports,
// platform service location, configuration, and Jobs remain outside it.
//
// Content is stateless and safe for concurrent use. It starts no goroutines
// and never closes its VFS dependency. Small purpose-specific pipelines may
// buffer input only behind an explicit bound; general file storage must stream.
// Errors and public values never expose private keys or opaque backend
// revisions. Cancellation or an uncertain backend acknowledgement may leave
// invisible bytes that Upload Lease recovery removes after its safety window.
package filecontent
