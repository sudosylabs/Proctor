// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package exam

import (
	"errors"

	applicationidempotency "github.com/sudosylabs/proctor/server/app/idempotency"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	idempotencyOperationCreate                    = "exam.create.v1"
	idempotencyOperationEditDraftText             = "exam.draft.text.edit.v1"
	idempotencyOperationConfigureDraftFocusLoss   = "exam.draft.focus_loss.configure.v1"
	idempotencyOperationConfigureExecutionProfile = "exam.draft.execution_profile.configure.v1"
	idempotencyOperationConfigureBrowserPolicy    = "exam.draft.browser_policy.configure.v1"
	idempotencyOperationArchive                   = "exam.archive.v1"
	idempotencyOperationAddManager                = "exam.manager.add.v1"
	idempotencyOperationRemoveManager             = "exam.manager.remove.v1"
	idempotencyOperationTransferOwner             = "exam.owner.transfer.v1"
	idempotencyOperationPublishRevision           = "exam.revision.publish.v1"
)

func prepareIdempotency(call Call, operation, key string, semantic any) (*store.CommandIdempotency, error) {
	if key == "" {
		return nil, &Fault{Code: "idempotency.key_required"}
	}
	prepared, err := applicationidempotency.Prepare(call.Principal().UserID, operation, key, semantic)
	var encodingError *applicationidempotency.SemanticEncodingError
	switch {
	case errors.Is(err, applicationidempotency.ErrInvalidPrincipal):
		return nil, &Fault{Code: "idempotency.invalid_key", Cause: err}
	case errors.As(err, &encodingError):
		return nil, &Fault{Code: "request.invalid", Cause: err}
	default:
		return prepared, err
	}
}
