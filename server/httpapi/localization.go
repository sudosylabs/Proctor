// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/localization"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	systemAdministratorRoleNameID        = "authorization.role.system_admin.name"
	systemAdministratorRoleDescriptionID = "authorization.role.system_admin.description"
)

const localizationOrigin = "httpapi"

var problemPresentationNames = []string{
	"bad_request",
	"client_error",
	"conflict",
	"forbidden",
	"internal",
	"method_not_allowed",
	"not_live",
	"not_found",
	"not_ready",
	"service_unavailable",
	"too_many_requests",
	"unauthorized",
}

// LocalizationDefinitions returns the closed set of messages currently owned
// by the HTTP presentation boundary.
func LocalizationDefinitions() []localization.Definition {
	definitions := make([]localization.Definition, 0, len(problemPresentationNames)*2+len(model.AllSessionRevocationReasons())+2)
	for _, name := range problemPresentationNames {
		for _, field := range []string{"detail", "title"} {
			definitions = append(definitions, localization.Definition{
				ID:     "problem." + name + "." + field,
				Origin: localizationOrigin,
			})
		}
	}
	for _, reason := range model.AllSessionRevocationReasons() {
		definitions = append(definitions, localization.Definition{
			ID:     sessionRevocationLocalizationID(reason),
			Origin: localizationOrigin,
		})
	}
	definitions = append(definitions,
		localization.Definition{ID: systemAdministratorRoleNameID, Origin: localizationOrigin},
		localization.Definition{ID: systemAdministratorRoleDescriptionID, Origin: localizationOrigin},
	)
	return definitions
}

func sessionRevocationLocalizationID(reason model.SessionRevocationReason) string {
	return "session.revocation." + string(reason)
}

func translatedRequestText(request *http.Request, id, fallback string) string {
	if request == nil {
		return fallback
	}
	requestLocalization, ok := request.Context().Value(localizationContextKey{}).(requestLocalization)
	if !ok || requestLocalization.localizer == nil {
		return fallback
	}
	translated, err := requestLocalization.localizer.Translate(requestLocalization.locale, id, nil)
	if err != nil {
		return fallback
	}
	return translated
}
