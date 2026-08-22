// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	if session.RevocationReasonCode != "user_logout" || session.RevocationReason != "Déconnectée par l’utilisateur." {
		t.Fatalf("session revocation presentation = %#v", session)
	}

	role := roleResponseFromModel(request, &model.Role{
		Name: model.SystemAdministratorRoleName, DisplayName: "System Administrator",
		Description: "Full installation access.", BuiltIn: true,
	})
	if role.Name != model.SystemAdministratorRoleName || role.DisplayName != "Administrateur système" ||
		role.Description != "Accès complet à l’installation." {
		t.Fatalf("built-in role presentation = %#v", role)
	}
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
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != "authorization.denied" || problem.Status != http.StatusForbidden {
		t.Fatalf("machine problem changed: %#v", problem)
	}
	if problem.Title != "Accès refusé" || problem.Detail != "Cette action n’est pas autorisée." {
		t.Fatalf("localized presentation = %#v", problem)
	}
}

func TestPreferredLocaleHonorsAcceptLanguageQuality(t *testing.T) {
	t.Parallel()
	if got := localization.PreferredLocale("de;q=0.4, fr-CA;q=0.9, en;q=0.8", []string{"en", "fr"}); got != "fr" {
		t.Fatalf("preferred locale = %q", got)
	}
}

func TestProblemPresentationHasNoHardcodedProseFallback(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	title, detail := localizedProblemPresentation(request, http.StatusForbidden)
	if title != "" || detail != "" {
		t.Fatalf("presentation without a catalog = (%q, %q), want no synthesized prose", title, detail)
	}

	response := httptest.NewRecorder()
	WriteProblem(response, Problem{Status: http.StatusTeapot})
	var problem Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Title != "" {
		t.Fatalf("problem writer synthesized title %q", problem.Title)
	}
}

func TestNewRequiresLocalizer(t *testing.T) {
	t.Parallel()

	logger, _ := newTestLogger(t)
	if _, err := New(Options{Logger: logger}); err == nil || !strings.Contains(err.Error(), "localizer is required") {
		t.Fatalf("New without localizer error = %v, want localizer requirement", err)
	}
}
