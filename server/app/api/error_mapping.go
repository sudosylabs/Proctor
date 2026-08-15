// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
)

// applicationFailure is the transport-facing view of a transport-neutral
// application error such as *app.Error (Code + Fields). The interface keeps
// HTTP mapping duck-typed even when the package also imports app for
// constructing errors at the handler boundary.
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
// application codes. Each capability registers its reviewed public codes;
// an unmapped code fails safe as a generic 500.
var applicationErrorMappings = map[string]applicationErrorMapping{
	"authentication.invalid_credentials":                   {status: http.StatusUnauthorized},
	"authentication.invalid_token":                         {status: http.StatusUnauthorized},
	"authentication.credential_ambiguous":                  {status: http.StatusBadRequest},
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
	"idempotency.not_supported":                            {status: http.StatusBadRequest},
	"idempotency.key_required":                             {status: http.StatusBadRequest},
	"idempotency.invalid_key":                              {status: http.StatusBadRequest},
	"idempotency.conflict":                                 {status: http.StatusConflict},
	"idempotency.in_progress":                              {status: http.StatusConflict},
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
	"job.query.invalid":                                    {status: http.StatusBadRequest},
	"job.cancel.unsupported":                               {status: http.StatusConflict},
	"job.retry.unsupported":                                {status: http.StatusConflict},
	"job.conflict":                                         {status: http.StatusConflict},
	"job.unavailable":                                      {status: http.StatusInternalServerError},
	"exam.invalid":                                         {status: http.StatusBadRequest},
	"exam.conflict":                                        {status: http.StatusConflict},
	"exam.archived":                                        {status: http.StatusConflict},
	"exam.revision_conflict":                               {status: http.StatusConflict},
	"exam.revision.no_changes":                             {status: http.StatusConflict},
	"exam.sitting.invalid":                                 {status: http.StatusBadRequest},
	"exam.sitting.conflict":                                {status: http.StatusConflict},
	"exam.sitting.revision_conflict":                       {status: http.StatusConflict},
	"exam.sitting.no_changes":                              {status: http.StatusConflict},
	"exam.sitting.state_conflict":                          {status: http.StatusConflict},
	"exam.sitting.class_ineligible":                        {status: http.StatusConflict},
	"exam.sitting.schedule_outside_period":                 {status: http.StatusConflict},
	"exam.sitting.schedule_not_future":                     {status: http.StatusConflict},
	"exam.sitting.deadline_reached":                        {status: http.StatusConflict},
	"exam.sitting.extension_not_later":                     {status: http.StatusConflict},
	"exam.sitting.correction.invalid":                      {status: http.StatusBadRequest},
	"exam.sitting.correction.invalid_content":              {status: http.StatusBadRequest},
	"exam.sitting.correction.not_found":                    {status: http.StatusNotFound},
	"exam.sitting.correction.conflict":                     {status: http.StatusConflict},
	"exam.sitting.correction.no_changes":                   {status: http.StatusConflict},
	"exam.sitting.correction.manifest_invalid":             {status: http.StatusConflict},
	"exam.sitting.correction.stage_invalid":                {status: http.StatusConflict},
	"exam.sitting.correction.unavailable":                  {status: http.StatusInternalServerError},
	"exam.sitting.unavailable":                             {status: http.StatusInternalServerError},
	"exam.attempt.invalid":                                 {status: http.StatusBadRequest},
	"exam.attempt.not_found":                               {status: http.StatusNotFound},
	"exam.attempt.sitting_unavailable":                     {status: http.StatusConflict},
	"exam.attempt.state_conflict":                          {status: http.StatusConflict},
	"exam.attempt.revision_conflict":                       {status: http.StatusConflict},
	"exam.attempt.suspension_conflict":                     {status: http.StatusConflict},
	"exam.attempt.renewal_conflict":                        {status: http.StatusConflict},
	"exam.attempt.connection_lost":                         {status: http.StatusConflict},
	"exam.attempt.continuity_invalid":                      {status: http.StatusNotFound},
	"exam.attempt.already_connected":                       {status: http.StatusConflict},
	"exam.attempt.connection_closed":                       {status: http.StatusConflict},
	"exam.attempt.conflict":                                {status: http.StatusConflict},
	"exam.attempt.unavailable":                             {status: http.StatusInternalServerError},
	"exam.draft.revision_conflict":                         {status: http.StatusConflict},
	"exam.draft.no_changes":                                {status: http.StatusConflict},
	"exam.manager.exists":                                  {status: http.StatusConflict},
	"exam.manager.not_found":                               {status: http.StatusConflict},
	"exam.manager.ineligible":                              {status: http.StatusConflict},
	"exam.manager.owner_protected":                         {status: http.StatusConflict},
	"exam.owner.no_changes":                                {status: http.StatusConflict},
	"exam.unavailable":                                     {status: http.StatusInternalServerError},
	"exam.resource.invalid":                                {status: http.StatusBadRequest},
	"exam.resource.invalid_content":                        {status: http.StatusBadRequest},
	"exam.resource.limit":                                  {status: http.StatusConflict},
	"exam.resource.no_changes":                             {status: http.StatusConflict},
	"exam.resource.order_invalid":                          {status: http.StatusBadRequest},
	"exam.resource.upload_invalid":                         {status: http.StatusBadRequest},
	"exam.resource.revision_conflict":                      {status: http.StatusConflict},
	"exam.resource.conflict":                               {status: http.StatusConflict},
	"exam.resource.unavailable":                            {status: http.StatusInternalServerError},
	"exam.starter_workspace.invalid":                       {status: http.StatusBadRequest},
	"exam.starter_workspace.path_conflict":                 {status: http.StatusConflict},
	"exam.starter_workspace.parent_not_found":              {status: http.StatusConflict},
	"exam.starter_workspace.directory_not_empty":           {status: http.StatusConflict},
	"exam.starter_workspace.entry_limit":                   {status: http.StatusConflict},
	"exam.starter_workspace.total_size_limit":              {status: http.StatusConflict},
	"exam.starter_workspace.upload_expired":                {status: http.StatusConflict},
	"exam.starter_workspace.object_conflict":               {status: http.StatusConflict},
	"exam.starter_workspace.content_conflict":              {status: http.StatusConflict},
	"exam.starter_workspace.no_changes":                    {status: http.StatusConflict},
	"exam.starter_workspace.invalid_move":                  {status: http.StatusConflict},
	"exam.starter_workspace.entry_kind":                    {status: http.StatusConflict},
	"exam.starter_workspace.conflict":                      {status: http.StatusConflict},
	"exam.starter_workspace.unavailable":                   {status: http.StatusInternalServerError},
	"installation.already_initialized":                     {status: http.StatusConflict},
	"installation.unavailable":                             {status: http.StatusInternalServerError},
	"authentication.password.invalid":                      {status: http.StatusBadRequest},
	"authentication.rate_limited":                          {status: http.StatusTooManyRequests},
	"authentication.account_token.invalid":                 {status: http.StatusBadRequest},
	"authentication.account_recovery.unavailable":          {status: http.StatusServiceUnavailable},
	"authentication.session_required":                      {status: http.StatusUnauthorized},
	"authentication.reauthentication_required":             {status: http.StatusForbidden},
	"personal_access_token.invalid":                        {status: http.StatusBadRequest},
	"personal_access_token.unavailable":                    {status: http.StatusInternalServerError},
	"personal_access_token.maximum_reached":                {status: http.StatusConflict},
	"authentication.mfa.disabled":                          {status: http.StatusNotImplemented},
	"authentication.mfa.not_found":                         {status: http.StatusNotFound},
	"authentication.mfa.conflict":                          {status: http.StatusConflict},
	"authentication.strong_authentication_required":        {status: http.StatusForbidden},
	"authentication.external.request.invalid":              {status: http.StatusBadRequest},
	"authentication.external.provider_not_found":           {status: http.StatusNotFound},
	"authentication.external.invalid":                      {status: http.StatusUnauthorized},
	"authentication.external.invalid_assertion":            {status: http.StatusUnauthorized},
	"authentication.external.rejected":                     {status: http.StatusUnauthorized},
	"authentication.external.unavailable":                  {status: http.StatusBadGateway},
	"authentication.external.internal":                     {status: http.StatusInternalServerError},
	"authentication.external.account_conflict":             {status: http.StatusConflict},
	"authentication.external.account_not_linked":           {status: http.StatusForbidden},
	"authentication.external.inactive_account":             {status: http.StatusForbidden},
	"user.invalid":                                         {status: http.StatusBadRequest},
	"user.conflict":                                        {status: http.StatusConflict},
	"user.last_system_admin":                               {status: http.StatusConflict},
	"profile_picture.invalid":                              {status: http.StatusBadRequest},
	"profile_picture.unavailable":                          {status: http.StatusInternalServerError},
	"websocket.internal":                                   {status: http.StatusInternalServerError},
	"websocket.unavailable":                                {status: http.StatusServiceUnavailable},
	"websocket.request.invalid":                            {status: http.StatusBadRequest},
	"websocket.origin.invalid":                             {status: http.StatusForbidden},
	"authentication.strong_required":                       {status: http.StatusForbidden},
	"authentication.csrf.invalid":                          {status: http.StatusForbidden},
	"audit.event.invalid":                                  {status: http.StatusInternalServerError},
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
