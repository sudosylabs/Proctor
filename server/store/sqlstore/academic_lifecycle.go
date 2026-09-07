// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// academicMutationAudit completes an optional audit in the same transaction as
// the shared lifecycle transition. Legacy callers use the same transition with
// no audit completion; application commands always supply their pending audit.
type academicMutationAudit struct {
	eventID string
	at      int64
}

func (a *academicMutationAudit) complete(ctx context.Context, tx *sqlxTxWrapper, data map[string]any) error {
	if a == nil {
		return nil
	}
	encoded, err := model.EncodeAuditData(data)
	if err != nil {
		return err
	}
	if _, err := completeAuditEvent(ctx, tx, a.eventID, model.AuditStatusSuccess, "", encoded, a.at); err != nil {
		return fmt.Errorf("complete academic mutation audit: %w", err)
	}
	return nil
}

func requireAcademicRevision(entity string, expected, current int64) error {
	if expected > 0 && expected != current {
		return store.NewErrConflict(entity, "revision_mismatch", nil)
	}
	return nil
}
