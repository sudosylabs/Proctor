// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

// Package secretseal encrypts bounded recoverable application secrets with a
// versioned, purpose-bound envelope. It owns cryptographic mechanics only;
// callers retain ownership of persistence, rotation workflow, and retention.
package secretseal
