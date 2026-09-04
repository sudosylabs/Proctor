// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

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
// The package depends on server/model, the consumer-owned application content
// contracts, the reusable VFS contract, and only the image codecs used by its
// working profile-picture pipeline. Concrete VFS backends, persistence,
// transports, platform service location, configuration, and Jobs remain
// outside it.
//
// Content is stateless and safe for concurrent use. It starts no goroutines
// and never closes its VFS dependency. Small purpose-specific pipelines may
// buffer input only behind an explicit bound; general file storage must stream.
// Errors and public values never expose private keys or opaque backend
// revisions. Cancellation or an uncertain backend acknowledgement may leave
// invisible bytes that Upload Lease recovery removes after its safety window.
package filecontent
