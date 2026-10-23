// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"fmt"

	"github.com/mattermost/morph/models"
)

func (m *Migrator) Pending() ([]*models.Migration, error) {
	pending, err := m.engine.Diff(models.Up)
	if err != nil {
		return nil, fmt.Errorf("inspect pending migrations: %w", err)
	}
	return pending, nil
}
