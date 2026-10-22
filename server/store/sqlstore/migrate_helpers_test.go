// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"errors"
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

// Down rolls back a bounded number of migrations. It is exposed for migration
// verification and deliberate operator tooling, not normal server startup.
func (m *Migrator) Down(steps int) (int, error) {
	if steps <= 0 {
		return 0, errors.New("migration rollback steps must be greater than zero")
	}
	applied, err := m.engine.ApplyDown(steps)
	if err != nil {
		return applied, fmt.Errorf("roll back database migrations: %w", err)
	}
	return applied, nil
}
