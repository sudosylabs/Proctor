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
	examAttemptConnectAction = "exam_attempt.connect"
	examAttemptRenewAction   = "exam_attempt.renew"
)

type examAttemptApplication interface {
	ConnectExamAttempt(context.Context, app.Invocation, app.ConnectExamAttemptCommand) (app.ExamAttemptConnection, error)
	RenewExamAttemptParticipation(context.Context, app.Invocation, app.RenewExamAttemptParticipationCommand) (app.ExamAttemptParticipationRenewal, error)
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

func decodeExamAttemptConnectRequest(document json.RawMessage) (examAttemptConnectRequest, error) {
	var value examAttemptConnectRequest
	if !utf8.Valid(document) {
		return value, errors.New("Exam Attempt connect request must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return value, errors.New("Exam Attempt connect request must be an object")
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		member, tokenErr := decoder.Token()
		if tokenErr != nil {
			return value, tokenErr
		}
		name, ok := member.(string)
		if !ok {
			return value, errors.New("Exam Attempt connect request member is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return value, errors.New("Exam Attempt connect request contains a duplicate member")
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
			return value, errors.New("Exam Attempt connect request contains trailing JSON")
		}
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
	var value examAttemptRenewRequest
	if !utf8.Valid(document) {
		return value, errors.New("Exam Attempt renewal request must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return value, errors.New("Exam Attempt renewal request must be an object")
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		member, tokenErr := decoder.Token()
		if tokenErr != nil {
			return value, tokenErr
		}
		name, ok := member.(string)
		if !ok {
			return value, errors.New("Exam Attempt renewal request member is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return value, errors.New("Exam Attempt renewal request contains a duplicate member")
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
			return value, errors.New("Exam Attempt renewal request contains trailing JSON")
		}
		return value, err
	}
	if value.Generation < 1 || value.Sequence < 1 || !model.IsValidCredentialToken(value.ContinuityCredential) {
		return value, errors.New("Exam Attempt renewal request fields are invalid")
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
