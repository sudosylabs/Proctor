// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package config

// FileContent bounds expensive content validation and rendition processing on
// one node. Changing the limit requires restart.
type FileContent struct {
	MaximumConcurrentOperations int `json:"MaximumConcurrentOperations"`
}
