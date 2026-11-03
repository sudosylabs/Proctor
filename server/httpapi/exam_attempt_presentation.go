// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"net/http"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type candidateExamPresentationResponse struct {
	Capacity                     examCapacityPolicyResponse           `json:"capacity"`
	AttemptID                    string                               `json:"attempt_id"`
	SittingID                    string                               `json:"exam_sitting_id"`
	Title                        string                               `json:"title"`
	InstructionsMarkdown         string                               `json:"instructions_markdown"`
	CandidateRuntimeCapabilities candidateRuntimeCapabilitiesResponse `json:"candidate_runtime_capabilities"`
	BrowserPolicy                *candidateBrowserPolicyResponse      `json:"browser_policy"`
	LiveCorrections              []candidateLiveCorrectionResponse    `json:"live_corrections"`
	Resources                    []candidateExamResourceResponse      `json:"resources"`
}

type candidateLiveCorrectionResponse struct {
	RevisionID              string                               `json:"revision_id"`
	RevisionNumber          int64                                `json:"revision_number"`
	EffectiveAt             string                               `json:"effective_at"`
	Summary                 string                               `json:"summary"`
	ChangedAreas            []model.ExamCorrectionChangedArea    `json:"changed_areas"`
	AffectedCapabilities    []model.CandidateCapability          `json:"affected_capabilities"`
	AcknowledgementRequired bool                                 `json:"acknowledgement_required"`
	AcknowledgementState    model.CorrectionAcknowledgementState `json:"acknowledgement_state"`
	AcknowledgedAt          *string                              `json:"acknowledged_at,omitempty"`
}

type candidateBrowserPolicyResponse struct {
	PolicyRevisionNumber      int64                                `json:"policy_revision_number"`
	BrowserActivityDisclosure model.BrowserActivityDisclosure      `json:"browser_activity_disclosure"`
	Enabled                   bool                                 `json:"enabled"`
	StartRuleID               string                               `json:"start_rule_id,omitempty"`
	Rules                     []candidateBrowserPolicyRuleResponse `json:"rules,omitempty"`
	PolicyRevisionID          string                               `json:"policy_revision_id"`
	PolicyDigest              string                               `json:"policy_digest"`
}

type candidateBrowserPolicyRuleResponse struct {
	RuleID                   string `json:"rule_id"`
	Origin                   string `json:"origin"`
	PathPrefix               string `json:"path_prefix"`
	HostMatch                string `json:"host_match"`
	AllowRedirects           bool   `json:"allow_redirects"`
	BlockedNavigationOutcome string `json:"blocked_navigation_outcome"`
	InstitutionHTTPException bool   `json:"institution_http_exception"`
}

type candidateRuntimeCapabilitiesResponse struct {
	SchemaVersion                 int                                   `json:"schema_version"`
	ServerTime                    string                                `json:"server_time"`
	InteractionState              string                                `json:"interaction_state"`
	AttemptConfiguration          candidateAttemptConfigurationResponse `json:"attempt_configuration"`
	FocusLossCollectionEnabled    bool                                  `json:"focus_loss_collection_enabled"`
	PendingCorrectionCapabilities []model.CandidateCapability           `json:"pending_correction_capabilities"`
	WorkspaceMutationAllowed      bool                                  `json:"workspace_mutation_allowed"`
	SubmissionAllowed             bool                                  `json:"submission_allowed"`
	Browser                       candidateBrowserCapabilityResponse    `json:"browser"`
	ExamRevision                  candidateExamRevisionResponse         `json:"exam_revision"`
	Departure                     candidateDepartureResponse            `json:"departure"`
}

type candidateAttemptConfigurationResponse struct {
	Revision            string                                 `json:"attempt_configuration_revision"`
	Presentation        model.AttemptConfigurationPresentation `json:"presentation"`
	ApprovedCommands    []string                               `json:"approved_commands"`
	ApprovedKeybindings []string                               `json:"approved_keybindings"`
	Digest              string                                 `json:"digest"`
}

type candidateBrowserCapabilityResponse struct {
	State            string `json:"state"`
	PolicyRevisionID string `json:"policy_revision_id,omitempty"`
	PolicyDigest     string `json:"policy_digest,omitempty"`
}

type candidateExamRevisionResponse struct {
	AdmissionRevisionID     string `json:"admission_revision_id"`
	CurrentRevisionID       string `json:"current_revision_id"`
	AcknowledgementRequired bool   `json:"acknowledgement_required"`
}

type candidateDepartureResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type candidateExamResourceResponse struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"display_name"`
	DescriptionMarkdown string `json:"description_markdown"`
	Position            int    `json:"position"`
	MediaType           string `json:"media_type"`
	Size                int64  `json:"size"`
	SHA256              string `json:"sha256"`
}

type acknowledgeExamCorrectionRequest struct {
	ParticipationID           string `json:"participation_id"`
	Generation                int64  `json:"generation"`
	ExpectedCurrentRevisionID string `json:"expected_current_revision_id"`
}

type acknowledgeExamCorrectionResponse struct {
	CurrentRevisionID    string                               `json:"current_revision_id"`
	RuntimeCapabilities  candidateRuntimeCapabilitiesResponse `json:"runtime_capabilities"`
	RevisionID           string                               `json:"revision_id"`
	AcknowledgementState string                               `json:"acknowledgement_state"`
	AcknowledgedAt       string                               `json:"acknowledged_at"`
}

func (m examAttemptHTTPModule) acknowledgeCorrection(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	correctionRevisionID, err := request.params.RequireExamRevisionID()
	if err != nil {
		return operationResult{}, err
	}
	var body acknowledgeExamCorrectionRequest
	if err = decodeCandidateWorkspaceJSON(request, &body); err != nil {
		return operationResult{}, err
	}
	mutationAccess, err := candidateWorkspaceMutationAccess(access, candidateWorkspaceMutationAccessRequest{
		ParticipationID: body.ParticipationID, Generation: body.Generation})
	if err != nil {
		return operationResult{}, err
	}
	currentRevisionID, err := model.ParseExamRevisionID(body.ExpectedCurrentRevisionID)
	if err != nil {
		return operationResult{}, invalidRequestError(err)
	}
	result, err := m.application.AcknowledgeExamAttemptCorrection(request.context, request.invocation(), attempt.AcknowledgeCorrectionCommand{
		Access: mutationAccess, CorrectionRevisionID: correctionRevisionID, ExpectedCurrentRevisionID: currentRevisionID,
		IdempotencyKey: request.idempotencyKey})
	if err != nil {
		return operationResult{}, err
	}
	if !result.AcknowledgedAt.Valid {
		return operationResult{}, errUnpresentableResult
	}
	return jsonResult(http.StatusOK, acknowledgeExamCorrectionResponse{RevisionID: result.CorrectionRevisionID.String(),
		CurrentRevisionID: result.CurrentRevisionID.String(), RuntimeCapabilities: candidateRuntimeCapabilitiesFromStore(result.RuntimeCapabilities),
		AcknowledgementState: string(model.CorrectionAcknowledgementAcknowledged),
		AcknowledgedAt:       model.FormatRFC3339(result.AcknowledgedAt.Time)}).withHeaders(noStoreHeaders()), nil
}

func (m examAttemptHTTPModule) presentation(request operationRequest) (operationResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return operationResult{}, err
	}
	view, err := m.application.GetCandidateExamPresentation(request.context, request.invocation(), access)
	if err != nil {
		return operationResult{}, err
	}
	response := candidateExamPresentationResponse{AttemptID: view.AttemptID.String(), SittingID: view.SittingID.String(), Title: view.Title,
		InstructionsMarkdown:         view.InstructionsMarkdown,
		Capacity:                     examCapacityPolicyResponseFromModel(view.Capacity),
		CandidateRuntimeCapabilities: candidateRuntimeCapabilitiesFromStore(view.RuntimeCapabilities),
		BrowserPolicy:                candidateBrowserPolicyFromStore(view.BrowserPolicy),
		LiveCorrections:              candidateLiveCorrectionsFromModel(view.LiveCorrections),
		Resources:                    make([]candidateExamResourceResponse, 0, len(view.Resources))}
	for _, resource := range view.Resources {
		response.Resources = append(response.Resources, candidateExamResourceResponse{ID: resource.ResourceID.String(), DisplayName: resource.DisplayName,
			DescriptionMarkdown: resource.DescriptionMarkdown, Position: resource.Position, MediaType: string(resource.MediaType), Size: resource.SizeBytes, SHA256: resource.SHA256})
	}
	return jsonResult(http.StatusOK, response).withHeaders(noStoreHeaders()), nil
}

func candidateLiveCorrectionsFromModel(values []model.CandidateLiveCorrection) []candidateLiveCorrectionResponse {
	result := make([]candidateLiveCorrectionResponse, len(values))
	for index, value := range values {
		item := candidateLiveCorrectionResponse{RevisionID: value.RevisionID.String(), RevisionNumber: value.RevisionNumber,
			EffectiveAt: model.FormatRFC3339(value.EffectiveAt), Summary: value.Summary,
			ChangedAreas:         append([]model.ExamCorrectionChangedArea(nil), value.ChangedAreas...),
			AffectedCapabilities: append([]model.CandidateCapability{}, value.AffectedCapabilities...), AcknowledgementRequired: value.AcknowledgementRequired, AcknowledgementState: value.AcknowledgementState}
		if value.AcknowledgedAt.Valid {
			at := model.FormatRFC3339(value.AcknowledgedAt.Time)
			item.AcknowledgedAt = &at
		}
		result[index] = item
	}
	return result
}

func candidateBrowserPolicyFromStore(value *store.CandidateBrowserPolicy) *candidateBrowserPolicyResponse {
	if value == nil {
		return nil
	}
	response := &candidateBrowserPolicyResponse{Enabled: value.Policy.Enabled,
		PolicyRevisionNumber: value.PolicyRevisionNumber, BrowserActivityDisclosure: value.BrowserActivityDisclosure,
		StartRuleID: value.Policy.StartRuleID, PolicyRevisionID: value.PolicyRevisionID.String(), PolicyDigest: value.PolicyDigest,
		Rules: make([]candidateBrowserPolicyRuleResponse, len(value.Policy.Rules))}
	for index, rule := range value.Policy.Rules {
		response.Rules[index] = candidateBrowserPolicyRuleResponse{RuleID: rule.RuleID, Origin: rule.Origin, PathPrefix: rule.PathPrefix,
			HostMatch: string(rule.HostMatch), AllowRedirects: rule.AllowRedirects, BlockedNavigationOutcome: string(rule.BlockedNavigationOutcome), InstitutionHTTPException: rule.InstitutionHTTPException}
	}
	return response
}

func candidateRuntimeCapabilitiesFromStore(value store.CandidateRuntimeCapabilities) candidateRuntimeCapabilitiesResponse {
	browser := candidateBrowserCapabilityResponse{State: string(value.Browser.State)}
	if value.Browser.PolicyRevisionID.IsValid() {
		browser.PolicyRevisionID = value.Browser.PolicyRevisionID.String()
		browser.PolicyDigest = value.Browser.PolicyDigest
	}
	return candidateRuntimeCapabilitiesResponse{SchemaVersion: value.SchemaVersion,
		ServerTime: model.FormatRFC3339(value.ServerTime), InteractionState: string(value.InteractionState),
		AttemptConfiguration: candidateAttemptConfigurationResponse{Revision: value.AttemptConfiguration.Revision,
			Presentation: value.AttemptConfiguration.Presentation, ApprovedCommands: append([]string{}, value.AttemptConfiguration.ApprovedCommands...),
			ApprovedKeybindings: append([]string{}, value.AttemptConfiguration.ApprovedKeybindings...), Digest: value.AttemptConfiguration.Digest},
		FocusLossCollectionEnabled:    value.FocusLossCollectionEnabled,
		PendingCorrectionCapabilities: append([]model.CandidateCapability{}, value.PendingCorrectionCapabilities...),
		WorkspaceMutationAllowed:      value.WorkspaceMutationAllowed, SubmissionAllowed: value.SubmissionAllowed,
		Browser: browser,
		ExamRevision: candidateExamRevisionResponse{AdmissionRevisionID: value.ExamRevision.AdmissionRevisionID.String(),
			CurrentRevisionID: value.ExamRevision.CurrentRevisionID.String(), AcknowledgementRequired: value.ExamRevision.AcknowledgementRequired},
		Departure: candidateDepartureResponse{Allowed: value.Departure.Allowed, Reason: value.Departure.Reason}}
}

func (m examAttemptHTTPModule) openResource(request operationRequest) (protocolResult, error) {
	access, err := candidateAccess(request)
	if err != nil {
		return protocolResult{}, err
	}
	resourceID, err := request.params.RequireExamResourceID()
	if err != nil {
		return protocolResult{}, err
	}
	opened, err := m.application.OpenCandidateExamResource(request.context, request.invocation(), attempt.OpenResourceQuery{Access: access, ResourceID: resourceID})
	if err != nil {
		return protocolResult{}, err
	}
	return candidateAttemptContentResult(request, opened, `"`+opened.SHA256+`"`, nil)
}
