// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package externalauth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/config"
)

func TestProfileMapperRejectsControlCharactersInProvisioningNames(t *testing.T) {
	t.Parallel()

	mapper := NewProfileMapper(config.ExternalAuthenticationProvider{
		ID: "campus", Type: "oidc", AutoProvision: true,
		Claims: config.ExternalClaimMapping{Subject: "sub", Username: "preferred_username", Email: "email",
			DisplayName: "name", FirstName: "given_name", LastName: "family_name"},
	})
	claims := func(name, given, family string) map[string][]string {
		return map[string][]string{"sub": {"subject-1"}, "preferred_username": {"student"}, "email": {"student@example.edu"},
			"name": {name}, "given_name": {given}, "family_name": {family}}
	}
	_, err := mapper.Assertion(claims("Student One", "Student", "One"), true, 1)
	require.NoError(t, err)
	for name, values := range map[string]map[string][]string{
		"display name": claims("Student\r\nBcc: other@example.test", "Student", "One"),
		"first name":   claims("Student One", "Stu\tdent", "One"),
		"last name":    claims("Student One", "Student", "O\u0085ne"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := mapper.Assertion(values, true, 1)
			require.ErrorIs(t, err, ErrInvalidResponse)
		})
	}
}
