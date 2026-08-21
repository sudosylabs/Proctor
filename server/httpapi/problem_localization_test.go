// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type problemTestLocalizer struct{}

func (problemTestLocalizer) Translate(_ string, id string, _ any) (string, error) {
	translations := map[string]string{
		"problem.forbidden.title":  "Accès refusé",
		"problem.forbidden.detail": "Cette action n’est pas autorisée.",
	}
	return translations[id], nil
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
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Language", "de;q=0.4, fr-CA;q=0.9, en;q=0.8")
	if got := preferredLocale(request, []string{"en", "fr"}); got != "fr" {
		t.Fatalf("preferred locale = %q", got)
	}
}
