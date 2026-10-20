// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

const (
	idempotencyOperationConfigureDraftNativePolicy = "exam.draft.native_policy.configure.v1"
	idempotencyOperationCreate                     = "exam.create.v1"
	idempotencyOperationEditDraftText              = "exam.draft.text.edit.v1"
	idempotencyOperationConfigureDraftFocusLoss    = "exam.draft.focus_loss.configure.v1"
	idempotencyOperationConfigureBrowserPolicy     = "exam.draft.browser_policy.configure.v1"
	idempotencyOperationArchive                    = "exam.archive.v1"
	idempotencyOperationAddManager                 = "exam.manager.add.v1"
	idempotencyOperationRemoveManager              = "exam.manager.remove.v1"
	idempotencyOperationTransferOwner              = "exam.owner.transfer.v1"
	idempotencyOperationPublishRevision            = "exam.revision.publish.v1"
)
