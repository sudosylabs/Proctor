// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
)

// applicationFailure is the transport-facing view of a transport-neutral
// application error such as *app.Error. The API package intentionally depends
// only on this narrow interface so it does not import package app while the
// legacy app → app/api composition edge still exists.
type applicationFailure interface {
	error
	Code() string
	Fields() map[string]string
}

// applicationErrorMapping is the HTTP presentation for one public application
// error code: status, Problem Details type (derived from the code), and
// localization key (defaults to the code).
type applicationErrorMapping struct {
	status          int
	localizationKey string
}

// applicationErrorMappings is the exhaustive HTTP table for transport-neutral
// application codes. Capabilities register their public codes here as they
// migrate off model.AppError. An unmapped code fails safe as a generic 500.
var applicationErrorMappings = map[string]applicationErrorMapping{
	"authentication.invalid_credentials":                   {status: http.StatusUnauthorized},
	"authentication.invalid_token":                         {status: http.StatusUnauthorized},
	"authentication.required":                              {status: http.StatusUnauthorized},
	"authentication.client_type.invalid":                   {status: http.StatusBadRequest},
	"authentication.session.invalid":                       {status: http.StatusBadRequest},
	"authentication.sessions.maximum_reached":              {status: http.StatusConflict},
	"authentication.user.conflict":                         {status: http.StatusConflict},
	"authentication.mfa.required":                          {status: http.StatusUnauthorized},
	"authentication.mfa.unavailable":                       {status: http.StatusServiceUnavailable},
	"authentication.mfa.invalid_code":                      {status: http.StatusUnauthorized},
	"authentication.internal":                              {status: http.StatusInternalServerError},
	"authentication.rate_limit_unavailable":                {status: http.StatusServiceUnavailable},
	"user.not_found":                                       {status: http.StatusNotFound},
	"authorization.denied":                                 {status: http.StatusForbidden},
	"authorization.request.invalid":                        {status: http.StatusBadRequest},
	"authorization.unavailable":                            {status: http.StatusInternalServerError},
	"audit.unavailable":                                    {status: http.StatusInternalServerError},
	"administration.unavailable":                           {status: http.StatusInternalServerError},
	"academic_unit.conflict":                               {status: http.StatusConflict},
	"academic_unit.invalid":                                {status: http.StatusBadRequest},
	"academic_unit.not_found":                              {status: http.StatusNotFound},
	"academic_unit_member.invalid":                         {status: http.StatusBadRequest},
	"academic_unit_member.conflict":                        {status: http.StatusConflict},
	"academic_period.invalid":                              {status: http.StatusBadRequest},
	"academic_period.conflict":                             {status: http.StatusConflict},
	"affiliation.invalid":                                  {status: http.StatusBadRequest},
	"affiliation.conflict":                                 {status: http.StatusConflict},
	"affiliation.student_has_active_enrollment":            {status: http.StatusConflict},
	"class.enrollment_conflict":                            {status: http.StatusConflict},
	"class_member.invalid":                                 {status: http.StatusBadRequest},
	"class_member.student_affiliation_required":            {status: http.StatusConflict},
	"class.invalid":                                        {status: http.StatusBadRequest},
	"class.conflict":                                       {status: http.StatusConflict},
	"institution.conflict":                                 {status: http.StatusConflict},
	"institution.invalid":                                  {status: http.StatusBadRequest},
	"programme.invalid":                                    {status: http.StatusBadRequest},
	"programme.conflict":                                   {status: http.StatusConflict},
	"programme_level.invalid":                              {status: http.StatusBadRequest},
	"programme_level.conflict":                             {status: http.StatusConflict},
	"request.invalid":                                      {status: http.StatusBadRequest},
	"resource.not_found":                                   {status: http.StatusNotFound},
	"session.not_found":                                    {status: http.StatusNotFound},
	"session.id.invalid":                                   {status: http.StatusBadRequest},
	"role.invalid":                                         {status: http.StatusBadRequest},
	"role.conflict":                                        {status: http.StatusConflict},
	"role.built_in.protected":                              {status: http.StatusConflict},
	"role.permission.unknown":                              {status: http.StatusBadRequest},
	"role_binding.invalid":                                 {status: http.StatusBadRequest},
	"role_binding.conflict":                                {status: http.StatusConflict},
	"role_binding.last_system_admin":                       {status: http.StatusConflict},
	"role_binding.system_admin_requires_institution_scope": {status: http.StatusBadRequest},
	"audit.query.invalid":                                  {status: http.StatusBadRequest},
	"installation.already_initialized":                     {status: http.StatusConflict},
	"installation.unavailable":                             {status: http.StatusInternalServerError},
	"authentication.password.invalid":                      {status: http.StatusBadRequest},
	"authentication.rate_limited":                          {status: http.StatusTooManyRequests},
	"user.invalid":                                         {status: http.StatusBadRequest},
	"user.conflict":                                        {status: http.StatusConflict},
	"user.last_system_admin":                               {status: http.StatusConflict},
}

// ApplicationErrorStatuses returns a copy of the registered application-code
// to HTTP status mapping. Tests use it to prove every registered code produces
// its declared status.
func ApplicationErrorStatuses() map[string]int {
	cloned := make(map[string]int, len(applicationErrorMappings))
	for code, mapping := range applicationErrorMappings {
		cloned[code] = mapping.status
	}
	return cloned
}

// LocalizationKey returns the translation key for a public application code.
// Registered overrides win; otherwise the stable machine code is the key.
func LocalizationKey(code string) string {
	if mapping, ok := applicationErrorMappings[code]; ok && mapping.localizationKey != "" {
		return mapping.localizationKey
	}
	return code
}

func problemFromApplicationFailure(request *http.Request, failure applicationFailure) Problem {
	mapping, ok := applicationErrorMappings[failure.Code()]
	if !ok {
		return internalProblem(request)
	}

	status := mapping.status
	if status < 400 || status > 599 {
		return internalProblem(request)
	}
	title, detail := problemPresentation(status)
	return newProblem(request, failure.Code(), status, title, detail, failure.Fields())
}

func newProblem(
	request *http.Request,
	code string,
	status int,
	title, detail string,
	fields map[string]string,
) Problem {
	return Problem{
		Type:      "https://proctor.sudosylabs.com/problems/" + code,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Instance:  request.URL.Path,
		Code:      code,
		RequestID: RequestID(request.Context()),
		Fields:    fields,
	}
}
