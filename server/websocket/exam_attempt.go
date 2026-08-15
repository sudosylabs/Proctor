// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/sudosylabs/proctor/server/app"
	"github.com/sudosylabs/proctor/server/model"
)

const (
	examAttemptConnectAction   = "exam_attempt.connect"
	examAttemptRenewAction     = "exam_attempt.renew"
	examAttemptFocusLossAction = "exam_attempt.focus_loss"
)

type examAttemptApplication interface {
	ConnectExamAttempt(context.Context, app.Invocation, app.ConnectExamAttemptCommand) (app.ExamAttemptConnection, error)
	RenewExamAttemptParticipation(context.Context, app.Invocation, app.RenewExamAttemptParticipationCommand) (app.ExamAttemptParticipationRenewal, error)
	EvaluateExamAttemptFocusLoss(context.Context, app.Invocation, app.EvaluateExamAttemptFocusLossCommand) (app.ExamAttemptFocusLossEvaluation, error)
	CloseExamAttemptConnection(context.Context, app.Invocation, app.CloseExamAttemptConnectionCommand) (app.ExamAttemptConnectionClosed, error)
}

type examAttemptConnectRequest struct {
	ExamSittingID        string `json:"exam_sitting_id"`
	IdempotencyKey       string `json:"idempotency_key"`
	ContinuityCredential string `json:"continuity_credential"`
}

type examAttemptConnectResponse struct {
	AttemptID              string `json:"attempt_id"`
	WorkspaceID            string `json:"workspace_id"`
	ParticipationID        string `json:"participation_id"`
	AttemptConnectionID    string `json:"attempt_connection_id"`
	Generation             int64  `json:"generation"`
	RenewalIntervalSeconds int64  `json:"renewal_interval_seconds"`
	StartedAt              string `json:"started_at"`
	LeaseExpiresAt         string `json:"lease_expires_at"`
	FirstAdmission         bool   `json:"first_admission"`
	Replayed               bool   `json:"replayed"`
}

type examAttemptRenewRequest struct {
	Generation           int64  `json:"generation"`
	Sequence             int64  `json:"sequence"`
	ContinuityCredential string `json:"continuity_credential"`
}

type examAttemptRenewResponse struct {
	Generation       int64  `json:"generation"`
	AcceptedSequence int64  `json:"accepted_sequence"`
	DatabaseTime     string `json:"database_time"`
	LeaseExpiresAt   string `json:"lease_expires_at"`
	Duplicate        bool   `json:"duplicate"`
}

type examAttemptFocusLossRequest struct {
	SchemaVersion        int    `json:"schema_version"`
	Generation           int64  `json:"generation"`
	Sequence             int64  `json:"sequence"`
	DurationMilliseconds int64  `json:"duration_milliseconds"`
	Source               string `json:"source,omitempty"`
	ContinuityCredential string `json:"continuity_credential"`
}

type examAttemptFocusLossResponse struct {
	Generation        int64  `json:"generation"`
	AcceptedSequence  int64  `json:"accepted_sequence"`
	ReceivedAt        string `json:"received_at"`
	Duplicate         bool   `json:"duplicate"`
	GapDetected       bool   `json:"gap_detected"`
	PolicyDisabled    bool   `json:"policy_disabled"`
	WarningCreated    bool   `json:"warning_created"`
	SuspensionCreated bool   `json:"suspension_created"`
}

func decodeExamAttemptConnectRequest(document json.RawMessage) (examAttemptConnectRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptConnectRequest](document, "Exam Attempt connect request", 3)
	if err != nil {
		return value, err
	}
	if value.IdempotencyKey == "" || !model.IsValidCredentialToken(value.ContinuityCredential) {
		return value, errors.New("Exam Attempt connect request fields are invalid")
	}
	if _, err = model.ParseExamSittingID(value.ExamSittingID); err != nil {
		return value, errors.New("Exam Attempt Sitting identity is invalid")
	}
	return value, nil
}

func decodeExamAttemptRenewRequest(document json.RawMessage) (examAttemptRenewRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptRenewRequest](document, "Exam Attempt renewal request", 3)
	if err != nil {
		return value, err
	}
	if value.Generation < 1 || value.Sequence < 1 || !model.IsValidCredentialToken(value.ContinuityCredential) {
		return value, errors.New("Exam Attempt renewal request fields are invalid")
	}
	return value, nil
}

func decodeExamAttemptFocusLossRequest(document json.RawMessage) (examAttemptFocusLossRequest, error) {
	value, err := decodeStrictExamAttemptObject[examAttemptFocusLossRequest](document, "Exam Attempt Focus Loss request", 6)
	if err != nil {
		return value, err
	}
	if value.SchemaVersion != model.FocusLossSignalSchemaVersion || value.Generation < 1 || value.Sequence < 1 ||
		value.DurationMilliseconds < 1 ||
		value.DurationMilliseconds > model.FocusLossMaximumDurationMilliseconds ||
		!model.FocusLossSource(value.Source).IsValid() || !model.IsValidCredentialToken(value.ContinuityCredential) {
		return value, errors.New("Exam Attempt Focus Loss request fields are invalid")
	}
	return value, nil
}

func decodeStrictExamAttemptObject[T any](document json.RawMessage, label string, expectedMembers int) (T, error) {
	var value T
	if !utf8.Valid(document) {
		return value, errors.New(label + " must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return value, errors.New(label + " must be an object")
	}
	seen := make(map[string]struct{}, expectedMembers)
	for decoder.More() {
		member, tokenErr := decoder.Token()
		if tokenErr != nil {
			return value, tokenErr
		}
		name, ok := member.(string)
		if !ok {
			return value, errors.New(label + " member is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return value, errors.New(label + " contains a duplicate member")
		}
		seen[name] = struct{}{}
		var raw json.RawMessage
		if err = decoder.Decode(&raw); err != nil {
			return value, err
		}
	}
	if _, err = decoder.Token(); err != nil {
		return value, err
	}
	strict := json.NewDecoder(bytes.NewReader(document))
	strict.DisallowUnknownFields()
	if err = strict.Decode(&value); err != nil {
		return value, err
	}
	var trailing any
	if err = strict.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return value, errors.New(label + " contains trailing JSON")
		}
		return value, err
	}
	return value, nil
}

func examAttemptConnectError(err error) (string, string) {
	code := "exam.attempt.unavailable"
	message := "Exam Attempt connection failed."
	if failure, ok := app.As(err); ok {
		code = failure.Code()
		if code == "resource.not_found" || code == "authorization.denied" {
			message = "Exam Attempt connection denied."
		}
	}
	return code, message
}

func examAttemptRenewError(err error) (string, string) {
	code := "exam.attempt.unavailable"
	message := "Exam Attempt renewal failed."
	if failure, ok := app.As(err); ok {
		code = failure.Code()
		switch code {
		case "resource.not_found", "authorization.denied":
			message = "Exam Attempt renewal denied."
		case "exam.attempt.connection_lost":
			message = "Secure connectivity could not be renewed. Ask a manager to re-allow access."
		}
	}
	return code, message
}

func examAttemptFocusLossError(err error) (string, string) {
	failure, ok := app.As(err)
	if !ok {
		return "exam.attempt.unavailable", "Focus Loss signal could not be accepted."
	}
	switch failure.Code() {
	case "authorization.denied", "resource.not_found":
		return "resource.not_found", "Focus Loss signal was denied."
	case "authentication.invalid_token":
		return "authentication.invalid_token", "Focus Loss signal was denied."
	case "exam.attempt.connection_closed":
		return "exam.attempt.connection_closed", "Exam Attempt connection is not active."
	case "exam.attempt.connection_lost":
		return "exam.attempt.connection_lost", "Secure connectivity could not be renewed. Ask a manager to re-allow access."
	case "exam.attempt.focus_loss_conflict":
		return "exam.attempt.focus_loss_conflict", "Focus Loss signal conflicts with an accepted sequence."
	case "exam.attempt.sitting_unavailable", "exam.attempt.state_conflict":
		return failure.Code(), "Focus Loss signal could not be accepted."
	default:
		return "exam.attempt.unavailable", "Focus Loss signal could not be accepted."
	}
}
