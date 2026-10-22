// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import "github.com/sudosylabs/proctor/server/model"

const (
	idempotencyOperationConfigureDraftNativePolicy = "exam.draft.native_policy.configure"
	idempotencyOperationCreate                     = string(model.ActionExamCreate)
	idempotencyOperationEditDraftText              = "exam.draft.text.edit"
	idempotencyOperationConfigureDraftFocusLoss    = "exam.draft.focus_loss.configure"
	idempotencyOperationConfigureBrowserPolicy     = "exam.draft.browser_policy.configure"
	idempotencyOperationArchive                    = "exam.archive"
	idempotencyOperationAddManager                 = "exam.manager.add"
	idempotencyOperationRemoveManager              = "exam.manager.remove"
	idempotencyOperationTransferOwner              = "exam.owner.transfer"
	idempotencyOperationPublishRevision            = "exam.revision.publish"
)
