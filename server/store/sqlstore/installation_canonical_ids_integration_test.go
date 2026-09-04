//go:build integration

// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"errors"
	"testing"

	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
)

func TestInstallationCanonicalIDConstraints(t *testing.T) {
	persistence := openTestStore(t)
	resetPristineTestStore(t, persistence)
	ctx := context.Background()

	institution, err := persistence.Institution().Save(ctx, &model.Institution{
		Name: "installation-constraint", DisplayName: "Installation Constraint",
	})
	if err != nil {
		t.Fatal(err)
	}
	user := saveIntegrationUser(t, ctx, persistence, &model.User{
		Username: "installation-constraint", Email: "installation-constraint@example.edu", DisplayName: "Administrator",
	})
	policy := model.NewInitialAccessPolicy(model.NewAccessPolicyID(), model.NowUTC())
	if err := insertInitialAccessPolicy(ctx, persistence.GetMaster(), policy); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetMaster().Exec(ctx, `
		INSERT INTO installation_states (
			singleton, initialized_at, institution_id, administrator_user_id,
			access_policy_id, bootstrap_secret_digest, bootstrap_command_fingerprint,
			bootstrap_result
		) VALUES (1, NOW(), ?, ?, ?, decode(repeat('01', 32), 'hex'),
		          decode(repeat('02', 32), 'hex'), '{}'::jsonb)`,
		institution.ID.String(), user.ID.String(), policy.ID.String()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		query      string
		constraint string
	}{
		{name: "institution id", query: "UPDATE installation_states SET institution_id = 'bad' WHERE singleton = 1", constraint: "installation_states_institution_id_canonical_check"},
		{name: "administrator user id", query: "UPDATE installation_states SET administrator_user_id = 'bad' WHERE singleton = 1", constraint: "installation_states_administrator_user_id_canonical_check"},
		{name: "access policy id", query: "UPDATE installation_states SET access_policy_id = 'bad' WHERE singleton = 1", constraint: "installation_states_access_policy_id_canonical_check"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := persistence.GetMaster().Exec(ctx, test.query)
			var postgresErr *pq.Error
			if !errors.As(err, &postgresErr) || string(postgresErr.Code) != "23514" || postgresErr.Constraint != test.constraint {
				t.Fatalf("constraint error = %#v, want check violation %s", err, test.constraint)
			}
		})
	}
}
