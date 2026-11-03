// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/sudosylabs/proctor/server/app/exam/attempt"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	candidateAttemptCredentialHeader = "X-Proctor-Attempt-Credential"
	candidateAttemptConnectionHeader = "X-Proctor-Attempt-Connection-ID"
	sittingCandidateStatusCursorKind = "sitting_candidate_status"
)

type candidateAttemptHeaderAccess struct {
	ConnectionID         model.AttemptConnectionID
	ContinuityCredential string
}

func candidateAttemptAccessHeaders(request *http.Request) (candidateAttemptHeaderAccess, error) {
	if request == nil {
		return candidateAttemptHeaderAccess{}, errors.New("candidate Attempt headers are required")
	}
	credentials := request.Header.Values(candidateAttemptCredentialHeader)
	connections := request.Header.Values(candidateAttemptConnectionHeader)
	if len(credentials) != 1 || len(connections) != 1 || !model.IsValidCredentialToken(credentials[0]) {
		return candidateAttemptHeaderAccess{}, errors.New("one canonical candidate Attempt credential and Connection ID are required")
	}
	connectionID, err := model.ParseAttemptConnectionID(connections[0])
	if err != nil {
		return candidateAttemptHeaderAccess{}, errors.New("one canonical candidate Attempt credential and Connection ID are required")
	}
	return candidateAttemptHeaderAccess{ConnectionID: connectionID, ContinuityCredential: credentials[0]}, nil
}

type ExamAttemptApplication interface {
	BrowserSourceStatus(context.Context, invocation.Invocation, attempt.BrowserSourceQuery) (*model.BrowserSourceStatus, error)
	BrowserDeliveryReceipts(context.Context, invocation.Invocation, attempt.BrowserSourceQuery, int64, int) (*model.BrowserReceiptPage, error)
	DeclareBrowserDeliveryGaps(context.Context, invocation.Invocation, attempt.BrowserDeliveryGapsCommand) (*model.BrowserDeliveryGapResult, error)
	SealBrowserDelivery(context.Context, invocation.Invocation, attempt.BrowserDeliveryFinalCommand) (*model.BrowserSourceStatus, error)
	UpdateBrowserDeliverySummary(context.Context, invocation.Invocation, attempt.BrowserDeliverySummaryCommand) (*model.BrowserDeliverySummaryResult, error)
	AppendHistoricalBrowserDelivery(context.Context, invocation.Invocation, attempt.BrowserDeliveryAppendCommand) (*model.BrowserActivityAcknowledgement, error)

	BrowserSourceList(context.Context, invocation.Invocation, attempt.BrowserSourceQuery) ([]model.BrowserSourceStatus, error)
	AppendNativeDelivery(context.Context, invocation.Invocation, attempt.NativeDeliveryAppendCommand) (*model.NativeSecurityAcknowledgement, error)
	DeliveryBudget(context.Context, invocation.Invocation, attempt.DeliveryBudgetQuery) (*model.DeliveryBudgetSnapshot, error)
	StopDeliveryDetails(context.Context, invocation.Invocation, attempt.StopDeliveryDetailsCommand) (*model.StopDeliveryDetailsResult, error)
	NativeDeliveryStatus(context.Context, invocation.Invocation, attempt.NativeDeliveryQuery) (*model.NativeSecurityStreamStatus, error)
	NativeDeliveryReceipt(context.Context, invocation.Invocation, attempt.NativeDeliveryQuery, int64) (*model.NativeBatchReceipt, error)
	DeclareNativeDeliveryGaps(context.Context, invocation.Invocation, attempt.NativeDeliveryGapsCommand) (*model.DeliveryGapReceipt, error)
	SealNativeDelivery(context.Context, invocation.Invocation, attempt.NativeDeliveryFinalCommand) (*model.NativeSecurityStreamStatus, error)
	UpdateNativeDeliverySummary(context.Context, invocation.Invocation, attempt.NativeDeliverySummaryCommand) (*model.NativeSecurityStreamStatus, error)
	RecoverExamSecurityPolicy(context.Context, invocation.Invocation, model.ExamAttemptID) (*store.SecurityPolicyRecovery, error)
	PrepareExamSecurityPreflight(context.Context, invocation.Invocation, attempt.PrepareSecurityPreflightCommand) (*store.SecurityPreflightPrepared, error)
	ReportExamSecurityPreflight(context.Context, invocation.Invocation, attempt.ReportSecurityPreflightCommand) (*model.SecurityPreflightResult, error)
	GetExamAttempt(context.Context, invocation.Invocation, attempt.GetManagedAttemptQuery) (store.ExamAttemptManagerSnapshot, error)
	ListExamAttempts(context.Context, invocation.Invocation, attempt.ListManagedAttemptsQuery) (attempt.ManagedAttemptPage, error)
	ListSittingCandidateStatuses(context.Context, invocation.Invocation, attempt.SittingCandidateStatusesQuery) (attempt.SittingCandidateStatusesPage, error)
	ListExamAttemptBrowserActivity(context.Context, invocation.Invocation, attempt.BrowserActivityPageQuery) (attempt.BrowserActivityPage, error)
	AcknowledgeExamAttemptCorrection(context.Context, invocation.Invocation, attempt.AcknowledgeCorrectionCommand) (attempt.CorrectionAcknowledgementResult, error)
	EndExamAttemptByManager(context.Context, invocation.Invocation, attempt.ManagerEndCommand) (store.ExamSubmissionReceipt, error)
	GetCandidateExamPresentation(context.Context, invocation.Invocation, attempt.CandidateAccess) (attempt.Presentation, error)
	ListCandidateExamWorkspace(context.Context, invocation.Invocation, attempt.WorkspaceQuery) (attempt.WorkspacePage, error)
	ListCandidateExamWorkspaceJournal(context.Context, invocation.Invocation, attempt.WorkspaceJournalQuery) (attempt.WorkspaceJournalPage, error)
	CreateCandidateExamWorkspaceDirectory(context.Context, invocation.Invocation, attempt.CreateWorkspaceDirectoryCommand) (attempt.WorkspaceMutationResult, error)
	CreateCandidateExamWorkspaceFile(context.Context, invocation.Invocation, attempt.CreateWorkspaceFileCommand) (attempt.WorkspaceMutationResult, error)
	ReplaceCandidateExamWorkspaceFile(context.Context, invocation.Invocation, attempt.ReplaceWorkspaceFileCommand) (attempt.WorkspaceMutationResult, error)
	MoveCandidateExamWorkspaceEntry(context.Context, invocation.Invocation, attempt.MoveWorkspaceEntryCommand) (attempt.WorkspaceMutationResult, error)
	DeleteCandidateExamWorkspaceEntry(context.Context, invocation.Invocation, attempt.DeleteWorkspaceEntryCommand) (attempt.WorkspaceMutationResult, error)
	OpenCandidateExamResource(context.Context, invocation.Invocation, attempt.OpenResourceQuery) (attempt.OpenedContent, error)
	OpenCandidateExamWorkspaceFile(context.Context, invocation.Invocation, attempt.OpenWorkspaceFileQuery) (attempt.OpenedContent, error)
	ReallowExamAttempt(context.Context, invocation.Invocation, attempt.ReallowCommand) (attempt.ReallowResult, error)
	SuspendExamAttempt(context.Context, invocation.Invocation, attempt.ManualSuspensionCommand) (attempt.ManualSuspensionResult, error)
	SubmitExamAttempt(context.Context, invocation.Invocation, attempt.SubmitCommand) (store.ExamSubmissionReceipt, error)
	GetExamSubmission(context.Context, invocation.Invocation, attempt.GetSubmissionQuery) (attempt.ManagedSubmission, error)
	ListExamSubmissionManifest(context.Context, invocation.Invocation, attempt.ListSubmissionManifestQuery) (attempt.SubmissionManifestPage, error)
	OpenExamSubmissionFile(context.Context, invocation.Invocation, attempt.OpenSubmissionFileQuery) (attempt.OpenedContent, error)
}

type examAttemptHTTPModule struct{ application ExamAttemptApplication }

func examAttemptResource(application ExamAttemptApplication) apiResource {
	module := examAttemptHTTPModule{application: application}
	managerCollection := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("attempts"))
	managerMember := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("attempts"), canonicalID("exam_attempt_id"))
	managerBrowserActivity := appendRoutePath(managerMember, literal("browser-activity"))
	managerEnd := appendRoutePath(managerMember, literal("end"))
	candidateStatuses := apiPath(literal("exams"), canonicalID("exam_id"), literal("sittings"), canonicalID("exam_sitting_id"), literal("candidate-statuses"))
	candidate := apiPath(literal("exam-attempts"), canonicalID("exam_attempt_id"))
	presentation := appendRoutePath(candidate, literal("presentation"))
	workspace := appendRoutePath(candidate, literal("workspace"))
	workspaceChanges := appendRoutePath(workspace, literal("changes"))
	workspaceDirectories := appendRoutePath(workspace, literal("directories"))
	workspaceFiles := appendRoutePath(workspace, literal("files"))
	workspaceEntry := appendRoutePath(workspace, literal("entries"), canonicalID("attempt_workspace_entry_id"))
	resourceContent := appendRoutePath(candidate, literal("resources"), canonicalID("exam_resource_id"), literal("content"))
	workspaceContent := appendRoutePath(candidate, literal("workspace"), literal("files"), canonicalID("attempt_workspace_entry_id"), literal("content"))
	candidateSubmissions := appendRoutePath(candidate, literal("submissions"))
	correctionAcknowledgement := appendRoutePath(candidate, literal("corrections"), canonicalID("exam_revision_id"), literal("acknowledgement"))
	managerSubmission := appendRoutePath(managerMember, literal("submissions"), canonicalID("submission_id"))
	managerSubmissionManifest := appendRoutePath(managerSubmission, literal("manifest"))
	managerSubmissionContent := appendRoutePath(managerSubmission, literal("files"), canonicalID("attempt_workspace_entry_id"), literal("content"))
	reallow := appendRoutePath(managerMember, literal("reallow"))
	suspend := appendRoutePath(managerMember, literal("suspend"))
	return newResource("exam-attempts",
		principalRoute(http.MethodGet, managerCollection, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.listManaged),
		principalRoute(http.MethodGet, candidateStatuses, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.listCandidateStatuses),
		principalRoute(http.MethodGet, managerMember, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "audit.unavailable", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.getManaged),
		principalRoute(http.MethodGet, managerBrowserActivity, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "audit.unavailable", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.not_found", "exam.sitting.unavailable"}, module.listBrowserActivity),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, managerEnd, []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.revision_conflict", "exam.attempt.state_conflict", "exam.attempt.sitting_not_open", "exam.attempt.management_authority_conflict", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.endByManager),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, reallow, []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.revision_conflict", "exam.attempt.suspension_conflict", "exam.attempt.state_conflict", "exam.attempt.sitting_not_open", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.reallow),
		idempotentPrincipalRoute(IdempotencyRequired, http.MethodPost, suspend, []string{"authorization.request.invalid", "authorization.unavailable", "audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.revision_conflict", "exam.attempt.state_conflict", "exam.attempt.sitting_not_open", "exam.attempt.connection_lost", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.suspend),
		sessionRoute(http.MethodGet, presentation, []string{"request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.unavailable", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.presentation),
		sessionRoute(http.MethodGet, workspace, []string{"request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.unavailable", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.workspace),
		sessionRoute(http.MethodGet, workspaceChanges, []string{"request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.unavailable", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.workspaceChanges),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPost, workspaceDirectories, []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.connection_closed", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict", "exam.attempt.workspace.entry_limit", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.createWorkspaceDirectory),
		idempotentProtocolRoute(IdempotencyRequired, model.AttemptWorkspaceMaximumRequestBytes, "candidate-exam-workspace-file-upload",
			RouteProtocolStreamingUpload, AuthSessionRequired, http.MethodPost, workspaceFiles, []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.connection_closed", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict", "exam.attempt.workspace.entry_limit", "exam.attempt.workspace.size_limit", "exam.attempt.workspace.object_conflict", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.createWorkspaceFile),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPatch, workspaceEntry, []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.connection_closed", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.moveWorkspaceEntry),
		idempotentProtocolRoute(IdempotencyRequired, model.AttemptWorkspaceMaximumRequestBytes, "candidate-exam-workspace-file-replacement",
			RouteProtocolStreamingUpload, AuthSessionRequired, http.MethodPut, workspaceContent, []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.connection_closed", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict", "exam.attempt.workspace.content_conflict", "exam.attempt.workspace.size_limit", "exam.attempt.workspace.object_conflict", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.replaceWorkspaceFile),
		idempotentSessionRoute(IdempotencyRequired, http.MethodDelete, workspaceEntry, []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.connection_closed", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.attempt.workspace.path_conflict", "exam.attempt.workspace.entry_conflict", "exam.attempt.workspace.content_conflict", "exam.attempt.workspace.directory_not_empty", "exam.attempt.workspace.cursor_conflict", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.deleteWorkspaceEntry),
		protocolRoute("candidate-exam-resource-content", RouteProtocolBinaryDownload, AuthSessionRequired, http.MethodGet, resourceContent, []string{"request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.unavailable", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.openResource),
		protocolRoute("candidate-exam-workspace-content", RouteProtocolBinaryDownload, AuthSessionRequired, http.MethodGet, workspaceContent, []string{"request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.unavailable", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.openWorkspaceFile),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPost, candidateSubmissions, []string{"audit.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.sitting_not_open", "exam.attempt.state_conflict", "exam.attempt.connection_closed", "exam.attempt.conflict", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "exam.attempt.workspace.cursor_conflict", "exam.attempt.focus_loss_conflict", "exam.attempt.revision_conflict", "exam.attempt.correction_conflict", "exam.attempt.browser_activity_conflict", "exam.attempt.connection_lost", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.submit),
		idempotentSessionRoute(IdempotencyRequired, http.MethodPut, correctionAcknowledgement, []string{"request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.revision_conflict", "exam.attempt.correction_conflict", "exam.attempt.state_conflict", "exam.attempt.sitting_not_open", "exam.attempt.connection_closed", "exam.attempt.unavailable", "idempotency.key_required", "idempotency.key_invalid", "idempotency.conflict", "idempotency.in_progress", "authorization.unavailable", "exam.attempt.registered_desktop_required", "exam.delivery.metadata_capacity"}, module.acknowledgeCorrection),
		principalRoute(http.MethodGet, managerSubmission, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.getSubmission),
		principalRoute(http.MethodGet, managerSubmissionManifest, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.listSubmissionManifest),
		protocolRoute("exam-submission-file-content", RouteProtocolBinaryDownload, AuthPrincipalRequired, http.MethodGet,
			managerSubmissionContent, []string{"authorization.request.invalid", "authorization.unavailable", "request.invalid", "resource.not_found", "exam.attempt.invalid", "exam.attempt.unavailable", "exam.delivery.metadata_capacity", "exam.sitting.invalid", "exam.sitting.unavailable"}, module.openSubmissionFile),
	)
}

func idempotentSessionRoute(requirement IdempotencyRequirement, method string, path routePath, errorCodes []string, operation operation) routeDefinition {
	definition := sessionRoute(method, path, errorCodes, operation)
	definition.idempotency = requirement
	return definition
}

func candidateAttemptContentResult(request operationRequest, opened attempt.OpenedContent, etag string,
	untrusted func(http.Header),
) (protocolResult, error) {
	if opened.Body == nil || opened.SizeBytes < 0 || strings.TrimSpace(opened.MediaType) == "" || etag == `""` {
		return protocolResult{}, errors.New("Exam Attempt application returned incomplete content")
	}
	headers := privateNoStoreHeaders()
	headers.Set("Content-Type", opened.MediaType)
	if untrusted != nil {
		untrusted(headers)
	}
	headers.Set("ETag", etag)
	if etagMatches(request.request.Header.Get("If-None-Match"), etag) {
		_ = opened.Body.Close()
		return notModifiedProtocolResult(opened.SizeBytes).withHeaders(headers), nil
	}
	return binaryDownloadProtocolResult(opened.Body, opened.SizeBytes).withHeaders(headers), nil
}

func candidateAccess(request operationRequest) (attempt.CandidateAccess, error) {
	attemptID, err := request.params.RequireExamAttemptID()
	if err != nil {
		return attempt.CandidateAccess{}, err
	}
	headers, err := candidateAttemptAccessHeaders(request.request)
	if err != nil {
		return attempt.CandidateAccess{}, invalidRequestError(err)
	}
	return attempt.CandidateAccess{AttemptID: attemptID, ConnectionID: headers.ConnectionID,
		ContinuityCredential: headers.ContinuityCredential}, nil
}

func privateNoStoreHeaders() http.Header { return http.Header{"Cache-Control": {"private, no-store"}} }
