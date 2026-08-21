// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"net/http"
	"sort"

	application "github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

type AccessPolicyApplication interface {
	GetAccessPolicy(context.Context, application.Invocation) (application.AccessPolicyView, error)
	PreflightAccessPolicy(context.Context, application.Invocation, application.PreflightAccessPolicyCommand) (application.AccessPolicyPreflightResult, error)
	ReplaceAccessPolicy(context.Context, application.Invocation, application.ReplaceAccessPolicyCommand) (application.AccessPolicyView, error)
	DiscoverAccess(context.Context) (application.PublicAccessDiscovery, error)
}

type unavailableAccessPolicyApplication struct{}

func (unavailableAccessPolicyApplication) GetAccessPolicy(context.Context, application.Invocation) (application.AccessPolicyView, error) {
	return application.AccessPolicyView{}, application.NewError("access_policy.unavailable")
}
func (unavailableAccessPolicyApplication) PreflightAccessPolicy(context.Context, application.Invocation, application.PreflightAccessPolicyCommand) (application.AccessPolicyPreflightResult, error) {
	return application.AccessPolicyPreflightResult{}, application.NewError("access_policy.unavailable")
}
func (unavailableAccessPolicyApplication) ReplaceAccessPolicy(context.Context, application.Invocation, application.ReplaceAccessPolicyCommand) (application.AccessPolicyView, error) {
	return application.AccessPolicyView{}, application.NewError("access_policy.unavailable")
}
func (unavailableAccessPolicyApplication) DiscoverAccess(context.Context) (application.PublicAccessDiscovery, error) {
	return application.PublicAccessDiscovery{}, application.NewError("access_policy.unavailable")
}

type accessPolicySettingsRequest struct {
	ExpectedRevision                 int64                                            `json:"expected_revision"`
	RevokeExistingSessions           Optional[bool]                                   `json:"revoke_existing_sessions"`
	LocalLoginEnabled                Optional[bool]                                   `json:"local_login_enabled"`
	PublicRegistrationEnabled        Optional[bool]                                   `json:"public_registration_enabled"`
	InvitationAdmissionEnabled       Optional[bool]                                   `json:"invitation_admission_enabled"`
	InvitationLocalCredentialEnabled Optional[bool]                                   `json:"invitation_local_credential_enabled"`
	DesktopAuthorizationEnabled      Optional[bool]                                   `json:"desktop_authorization_enabled"`
	ProviderAdmissions               Optional[map[string]model.ProviderAdmissionMode] `json:"provider_admissions"`
}

type accessPolicyResponse struct {
	ID                               string                                   `json:"id"`
	Revision                         int64                                    `json:"revision"`
	CreatedAt                        int64                                    `json:"created_at"`
	UpdatedAt                        int64                                    `json:"updated_at"`
	LocalLoginEnabled                bool                                     `json:"local_login_enabled"`
	PublicRegistrationEnabled        bool                                     `json:"public_registration_enabled"`
	InvitationAdmissionEnabled       bool                                     `json:"invitation_admission_enabled"`
	InvitationLocalCredentialEnabled bool                                     `json:"invitation_local_credential_enabled"`
	DesktopAuthorizationEnabled      bool                                     `json:"desktop_authorization_enabled"`
	ProviderAdmissions               map[string]model.ProviderAdmissionMode   `json:"provider_admissions"`
	History                          []accessPolicyTransitionResponse         `json:"history"`
	AvailableProviders               []externalAuthenticationProviderResponse `json:"available_providers"`
	DurableMail                      bool                                     `json:"durable_mail"`
}

type accessPolicyTransitionResponse struct {
	FromRevision  int64    `json:"from_revision"`
	ToRevision    int64    `json:"to_revision"`
	ActorUserID   string   `json:"actor_user_id"`
	ChangedFields []string `json:"changed_fields"`
	ChangedAt     int64    `json:"changed_at"`
	Outcome       string   `json:"outcome"`
}

type accessPolicyBlockerResponse struct {
	Code       string `json:"code"`
	ProviderID string `json:"provider_id,omitempty"`
}
type accessPolicyPreflightResponse struct {
	Blockers []accessPolicyBlockerResponse `json:"blockers"`
}

type publicInstitutionPresentationResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}
type publicAccessCapabilitiesResponse struct {
	LocalLogin           bool `json:"local_login"`
	PublicRegistration   bool `json:"public_registration"`
	InvitationAdmission  bool `json:"invitation_admission"`
	DesktopAuthorization bool `json:"desktop_authorization"`
}
type desktopAuthorizationCompatibilityResponse struct {
	Protocol       string `json:"protocol"`
	MinimumVersion int    `json:"minimum_version"`
	MaximumVersion int    `json:"maximum_version"`
}
type publicAccessDiscoveryResponse struct {
	DiscoveryVersion     int                                       `json:"discovery_version"`
	CanonicalOrigin      string                                    `json:"canonical_origin"`
	InstallationID       string                                    `json:"installation_id,omitempty"`
	Initialized          bool                                      `json:"initialized"`
	Institution          *publicInstitutionPresentationResponse    `json:"institution,omitempty"`
	PolicyRevision       int64                                     `json:"policy_revision,omitempty"`
	Capabilities         publicAccessCapabilitiesResponse          `json:"capabilities"`
	Providers            []externalAuthenticationProviderResponse  `json:"providers"`
	DesktopAuthorization desktopAuthorizationCompatibilityResponse `json:"desktop_authorization"`
}

type accessPolicyResourceModule struct{ application AccessPolicyApplication }

func accessPolicyResource(application AccessPolicyApplication) resource {
	module := accessPolicyResourceModule{application: application}
	readErrors := operatorReadErrorCodes("access_policy.unavailable")
	preflightErrors := operatorReadErrorCodes("authentication.csrf.invalid", "authentication.strong_required",
		"authentication.reauthentication_required", "request.invalid", "access_policy.invalid",
		"access_policy.revision_conflict", "access_policy.unavailable")
	mutationErrors := operatorMutationErrorCodes("authentication.strong_required", "authentication.reauthentication_required",
		"idempotency.key_required", "idempotency.invalid_key", "idempotency.conflict", "idempotency.in_progress", "request.invalid", "access_policy.invalid",
		"access_policy.revision_conflict", "access_policy.blocked", "access_policy.unavailable")
	replace := strongRecentSessionRoute(http.MethodPut, apiPath(literal("access-policy")), mutationErrors, module.replace)
	replace.idempotency = IdempotencyRequired
	return newResource("access-policy",
		publicRoute(http.MethodGet, apiPath(literal("discovery")), []string{"access_policy.unavailable"}, module.discover),
		principalRoute(http.MethodGet, apiPath(literal("access-policy")), readErrors, module.get),
		strongRecentSessionRoute(http.MethodPost, apiPath(literal("access-policy"), literal("preflight")), preflightErrors, module.preflight),
		replace,
	)
}

func (m accessPolicyResourceModule) get(request operationRequest) (operationResult, error) {
	view, err := m.application.GetAccessPolicy(request.context, request.invocation())
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, accessPolicyResponseFromView(view)), nil
}

func (m accessPolicyResourceModule) preflight(request operationRequest) (operationResult, error) {
	var body accessPolicySettingsRequest
	if err := request.decodeJSON(&body, "preflightAccessPolicy"); err != nil {
		return operationResult{}, err
	}
	settings, revokeExistingSessions, err := requiredAccessPolicySettings(body)
	if err != nil {
		return operationResult{}, err
	}
	result, err := m.application.PreflightAccessPolicy(request.context, request.invocation(), application.PreflightAccessPolicyCommand{
		ExpectedRevision: body.ExpectedRevision, Settings: settings,
		RevokeExistingSessions: revokeExistingSessions})
	if err != nil {
		return operationResult{}, err
	}
	response := accessPolicyPreflightResponse{Blockers: make([]accessPolicyBlockerResponse, 0, len(result.Blockers))}
	for _, blocker := range result.Blockers {
		response.Blockers = append(response.Blockers, accessPolicyBlockerResponse{Code: string(blocker.Code), ProviderID: blocker.ProviderID})
	}
	return jsonResult(http.StatusOK, response), nil
}

func (m accessPolicyResourceModule) replace(request operationRequest) (operationResult, error) {
	var body accessPolicySettingsRequest
	if err := request.decodeJSON(&body, "replaceAccessPolicy"); err != nil {
		return operationResult{}, err
	}
	settings, revokeExistingSessions, err := requiredAccessPolicySettings(body)
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.application.ReplaceAccessPolicy(request.context, request.invocation(), application.ReplaceAccessPolicyCommand{
		ExpectedRevision: body.ExpectedRevision, Settings: settings,
		RevokeExistingSessions: revokeExistingSessions, IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, accessPolicyResponseFromView(view)), nil
}

func requiredAccessPolicySettings(body accessPolicySettingsRequest) (model.AccessPolicySettings, bool, error) {
	requiredBoolean := func(field string, value Optional[bool]) (bool, error) {
		candidate := value.ValuePointer()
		if candidate == nil {
			return false, application.NewError("request.invalid").WithField("field", field)
		}
		return *candidate, nil
	}
	revoke, err := requiredBoolean("revoke_existing_sessions", body.RevokeExistingSessions)
	if err != nil {
		return model.AccessPolicySettings{}, false, err
	}
	localLogin, err := requiredBoolean("local_login_enabled", body.LocalLoginEnabled)
	if err != nil {
		return model.AccessPolicySettings{}, false, err
	}
	publicRegistration, err := requiredBoolean("public_registration_enabled", body.PublicRegistrationEnabled)
	if err != nil {
		return model.AccessPolicySettings{}, false, err
	}
	invitationAdmission, err := requiredBoolean("invitation_admission_enabled", body.InvitationAdmissionEnabled)
	if err != nil {
		return model.AccessPolicySettings{}, false, err
	}
	invitationLocalCredential, err := requiredBoolean("invitation_local_credential_enabled", body.InvitationLocalCredentialEnabled)
	if err != nil {
		return model.AccessPolicySettings{}, false, err
	}
	desktopAuthorization, err := requiredBoolean("desktop_authorization_enabled", body.DesktopAuthorizationEnabled)
	if err != nil {
		return model.AccessPolicySettings{}, false, err
	}
	providerAdmissions := body.ProviderAdmissions.ValuePointer()
	if providerAdmissions == nil {
		return model.AccessPolicySettings{}, false, application.NewError("request.invalid").WithField("field", "provider_admissions")
	}
	return model.AccessPolicySettings{
		LocalLoginEnabled: localLogin, PublicRegistrationEnabled: publicRegistration,
		InvitationAdmissionEnabled: invitationAdmission, InvitationLocalCredentialEnabled: invitationLocalCredential,
		DesktopAuthorizationEnabled: desktopAuthorization, ProviderAdmissions: *providerAdmissions,
	}, revoke, nil
}

func (m accessPolicyResourceModule) discover(request operationRequest) (operationResult, error) {
	discovery, err := m.application.DiscoverAccess(request.context)
	if err != nil {
		return operationResult{}, err
	}
	return jsonResult(http.StatusOK, publicAccessDiscoveryResponseFromView(discovery)), nil
}

func accessPolicyResponseFromView(view application.AccessPolicyView) accessPolicyResponse {
	response := accessPolicyResponse{History: []accessPolicyTransitionResponse{}, AvailableProviders: []externalAuthenticationProviderResponse{}}
	if view.Policy == nil {
		return response
	}
	p := view.Policy
	response.ID, response.Revision, response.CreatedAt, response.UpdatedAt = p.ID.String(), p.Revision, model.MillisFromTime(p.CreatedAt), model.MillisFromTime(p.UpdatedAt)
	response.LocalLoginEnabled, response.PublicRegistrationEnabled = p.LocalLoginEnabled, p.PublicRegistrationEnabled
	response.InvitationAdmissionEnabled, response.InvitationLocalCredentialEnabled = p.InvitationAdmissionEnabled, p.InvitationLocalCredentialEnabled
	response.DesktopAuthorizationEnabled, response.ProviderAdmissions = p.DesktopAuthorizationEnabled, p.Settings().ProviderAdmissions
	response.DurableMail = view.DurableMail
	for _, transition := range view.History {
		if transition != nil {
			response.History = append(response.History, accessPolicyTransitionResponse{
				FromRevision: transition.FromRevision, ToRevision: transition.ToRevision, ActorUserID: transition.ActorID.String(),
				ChangedFields: append([]string(nil), transition.ChangedFields...), ChangedAt: model.MillisFromTime(transition.ChangedAt), Outcome: string(transition.Outcome)})
		}
	}
	for _, provider := range view.AvailableProviders {
		response.AvailableProviders = append(response.AvailableProviders, externalAuthenticationProviderResponse{ID: provider.Id, DisplayName: provider.DisplayName, Type: provider.Type})
	}
	return response
}

func publicAccessDiscoveryResponseFromView(view application.PublicAccessDiscovery) publicAccessDiscoveryResponse {
	response := publicAccessDiscoveryResponse{DiscoveryVersion: view.DiscoveryVersion, CanonicalOrigin: view.CanonicalOrigin,
		InstallationID: view.InstallationID, Initialized: view.Initialized, PolicyRevision: view.PolicyRevision,
		Capabilities: publicAccessCapabilitiesResponse{LocalLogin: view.Capabilities.LocalLogin, PublicRegistration: view.Capabilities.PublicRegistration,
			InvitationAdmission: view.Capabilities.InvitationAdmission, DesktopAuthorization: view.Capabilities.DesktopAuthorization},
		Providers: []externalAuthenticationProviderResponse{}, DesktopAuthorization: desktopAuthorizationCompatibilityResponse{
			Protocol: view.DesktopAuthorization.Protocol, MinimumVersion: view.DesktopAuthorization.MinimumVersion, MaximumVersion: view.DesktopAuthorization.MaximumVersion}}
	if view.Institution != nil {
		response.Institution = &publicInstitutionPresentationResponse{ID: view.Institution.ID.String(), Name: view.Institution.Name, DisplayName: view.Institution.DisplayName}
	}
	for _, provider := range view.Providers {
		response.Providers = append(response.Providers, externalAuthenticationProviderResponse{ID: provider.Id, DisplayName: provider.DisplayName, Type: provider.Type})
	}
	sort.Slice(response.Providers, func(i, j int) bool { return response.Providers[i].ID < response.Providers[j].ID })
	return response
}
