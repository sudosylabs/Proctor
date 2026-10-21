// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/model"
)

type problemTestLocalizer struct{}

func (problemTestLocalizer) Translate(_ string, id string, _ any) (string, error) {
	translations := map[string]string{
		"authorization.role.system_admin.description": "Accès complet à l’installation.",
		"authorization.role.system_admin.name":        "Administrateur système",
		"problem.forbidden.title":                     "Accès refusé",
		"problem.forbidden.detail":                    "Cette action n’est pas autorisée.",
		"session.revocation.user_logout":              "Déconnectée par l’utilisateur.",
	}
	return translations[id], nil
}

func TestSessionAndBuiltInRolePresentationLocalizesWithoutChangingIdentifiers(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(withRequestLocalization(context.Background(), problemTestLocalizer{}, "fr"))

	session := sessionResponseFromModel(request, &model.Session{
		RevocationReason: model.SessionRevocationUserLogout,
	})
	require.Equal(t, "user_logout", session.RevocationReasonCode)
	require.Equal(t, "Déconnectée par l’utilisateur.", session.RevocationReason)

	role := roleResponseFromModel(request, &model.Role{
		Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
		Description: "Full installation access.", BuiltIn: true,
	})
	require.Equal(t, model.SystemAdministratorRoleName, role.Name)
	require.Equal(t, "Administrateur système", role.DisplayName)
	require.Equal(t, "Accès complet à l’installation.", role.Description)
}

func (problemTestLocalizer) SupportedLocales() []string { return []string{"en", "fr"} }

type localizedFailure struct{}

func (localizedFailure) Error() string             { return "private cause" }
func (localizedFailure) Code() string              { return "authorization.denied" }
func (localizedFailure) Fields() map[string]string { return nil }

func TestWriteErrorLocalizesPresentationWithoutChangingMachineCode(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request = request.WithContext(withRequestLocalization(context.Background(), problemTestLocalizer{}, "fr"))
	response := httptest.NewRecorder()
	WriteError(response, request, localizedFailure{})

	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Equal(t, "authorization.denied", problem.Code)
	require.Equal(t, http.StatusForbidden, problem.Status)
	require.Equal(t, "Accès refusé", problem.Title)
	require.Equal(t, "Cette action n’est pas autorisée.", problem.Detail)
}

func TestPreferredLocaleHonorsAcceptLanguageQuality(t *testing.T) {
	t.Parallel()
	require.Equal(t, "fr", localization.PreferredLocale("de;q=0.4, fr-CA;q=0.9, en;q=0.8", []string{"en", "fr"}))
}

func TestProblemPresentationHasNoHardcodedProseFallback(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	title, detail := localizedProblemPresentation(request, http.StatusForbidden)
	require.Empty(t, title, "presentation without a catalog")
	require.Empty(t, detail, "presentation without a catalog")

	response := httptest.NewRecorder()
	WriteProblem(response, Problem{Status: http.StatusTeapot})
	var problem Problem
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &problem))
	require.Empty(t, problem.Title)
}

func TestNewRequiresLocalizer(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	_, err := New(Options{Logger: logger})
	require.Error(t, err, "New without localizer error")
	require.Contains(t, err.Error(), "localizer is required", "New without localizer error")
}
