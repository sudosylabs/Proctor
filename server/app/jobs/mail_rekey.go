// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

const (
	mailRekeyPageSize           = 100
	mailFanoutBundleSealPurpose = appmail.FanoutBundleSealingPurpose
)

type MailRekeyCommandV1 struct {
	PrimaryKeyID  string `json:"primary_key_id"`
	RetiringKeyID string `json:"retiring_key_id"`
}

type MailRekeyCheckpointV1 struct {
	AfterKind   store.MailRekeyTargetKind `json:"after_kind,omitempty"`
	AfterID     string                    `json:"after_id,omitempty"`
	Processed   int64                     `json:"processed"`
	Reencrypted int64                     `json:"reencrypted"`
}

type MailRekeyResultV1 struct {
	PrimaryKeyID         string `json:"primary_key_id"`
	RetiringKeyID        string `json:"retiring_key_id"`
	Processed            int64  `json:"processed"`
	Reencrypted          int64  `json:"reencrypted"`
	NonPrimaryReferences int64  `json:"non_primary_references"`
	RetiringReferences   int64  `json:"retiring_references"`
	RetirementSafe       bool   `json:"retirement_safe"`
}

func DecodeMailRekeyCommand(document json.RawMessage) (MailRekeyCommandV1, error) {
	var value MailRekeyCommandV1
	err := decodeStrictUniqueJobDocument(document, &value)
	return value, err
}

func DecodeMailRekeyCheckpoint(version int, document json.RawMessage) (MailRekeyCheckpointV1, error) {
	return decodeMailRekeyCheckpoint(version, document)
}

func DecodeMailRekeyResult(document json.RawMessage) (MailRekeyResultV1, error) {
	var value MailRekeyResultV1
	err := decodeStrictUniqueJobDocument(document, &value)
	return value, err
}

type MailRekeyJobStore interface {
	ListRekeyTargets(context.Context, *store.MailRekeyTargetPageRequest) (*store.MailRekeyTargetPage, error)
	ReplaceRekeyTarget(context.Context, *store.MailRekeyReplacement) (bool, error)
	ProveRekey(context.Context, *store.MailRekeyProofRequest) (*store.MailRekeyProof, error)
}

type mailRekeyHandler struct {
	mail   MailRekeyJobStore
	sealer *secretseal.Sealer
}

func (h mailRekeyHandler) Run(ctx context.Context, execution jobengine.Execution) jobengine.Outcome {
	if execution.Job == nil || execution.Attempt == nil || !execution.Job.ID.IsValid() || !execution.Attempt.ID.IsValid() ||
		execution.Job.CommandVersion != 1 || h.mail == nil || h.sealer == nil {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("mail rekey execution is incomplete"))
	}
	var command MailRekeyCommandV1
	if decodeStrictJobDocument(execution.Job.Command, &command) != nil || command.PrimaryKeyID == "" ||
		command.RetiringKeyID == "" || command.RetiringKeyID == command.PrimaryKeyID || !h.sealer.HasKey(command.RetiringKeyID) {
		return jobengine.PermanentFailure("job.command.invalid", errors.New("mail rekey command is invalid for this key ring"))
	}
	if command.PrimaryKeyID != h.sealer.PrimaryKeyID() {
		return jobengine.Relinquished("mail.rekey.primary_mismatch", errors.New("mail rekey command requires a different configured primary key"))
	}
	checkpoint, err := decodeMailRekeyCheckpoint(execution.Job.CheckpointVersion, execution.Job.Checkpoint)
	if err != nil {
		return jobengine.PermanentFailure("job.checkpoint.invalid", err)
	}
	initialProof, err := h.mail.ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: execution.Job.ID,
		PrimaryKeyID: command.PrimaryKeyID, RetiringKeyID: command.RetiringKeyID})
	if err != nil {
		return mailRekeyOutcome(err)
	}
	if err = validateMailRekeyProof(initialProof, command); err != nil {
		return jobengine.PermanentFailure("mail.rekey.invariant_failed", err)
	}
	if initialProof.NonPrimaryReferences > math.MaxInt64-checkpoint.Processed {
		return jobengine.PermanentFailure("mail.rekey.invariant_failed", errors.New("mail rekey progress exceeds its bound"))
	}
	progressTotal := checkpoint.Processed + initialProof.NonPrimaryReferences
	for {
		page, listErr := h.mail.ListRekeyTargets(ctx, &store.MailRekeyTargetPageRequest{
			JobID: execution.Job.ID, PrimaryKeyID: command.PrimaryKeyID,
			AfterKind: checkpoint.AfterKind, AfterID: checkpoint.AfterID, Limit: mailRekeyPageSize,
		})
		if listErr != nil {
			return mailRekeyOutcome(listErr)
		}
		if page == nil || len(page.Targets) > mailRekeyPageSize || (page.More && len(page.Targets) == 0) {
			return jobengine.PermanentFailure("mail.rekey.invariant_failed", errors.New("mail rekey page is invalid"))
		}
		for _, target := range page.Targets {
			if !mailRekeyTargetAfter(target, checkpoint) || target.KeyID == command.PrimaryKeyID || !h.sealer.HasKey(target.KeyID) {
				return jobengine.PermanentFailure("mail.rekey.invariant_failed", errors.New("mail rekey target is invalid"))
			}
			replacement, rekeyErr := h.reencryptTarget(target, execution.Job.ID, command.PrimaryKeyID)
			if rekeyErr != nil {
				return jobengine.PermanentFailure("mail.rekey.payload_unavailable", rekeyErr)
			}
			applied, replaceErr := h.mail.ReplaceRekeyTarget(ctx, replacement)
			if replaceErr != nil {
				return mailRekeyOutcome(replaceErr)
			}
			checkpoint.AfterKind, checkpoint.AfterID = target.Kind, target.ID
			checkpoint.Processed++
			if applied {
				checkpoint.Reencrypted++
			}
			if checkpointErr := checkpointMailRekey(ctx, execution, checkpoint, progressTotal); checkpointErr != nil {
				return jobengine.RetryableFailure("dependency.unavailable", checkpointErr)
			}
		}
		if page.More {
			continue
		}
		break
	}
	proof, err := h.mail.ProveRekey(ctx, &store.MailRekeyProofRequest{JobID: execution.Job.ID,
		PrimaryKeyID: command.PrimaryKeyID, RetiringKeyID: command.RetiringKeyID})
	if err != nil {
		return mailRekeyOutcome(err)
	}
	if err = validateMailRekeyProof(proof, command); err != nil {
		return jobengine.PermanentFailure("mail.rekey.invariant_failed", err)
	}
	if proof.NonPrimaryReferences != 0 || !proof.RetirementSafe {
		return jobengine.RetryableFailure("mail.rekey.incomplete", errors.New("active mail payloads still use a non-primary key"))
	}
	result, err := json.Marshal(MailRekeyResultV1{PrimaryKeyID: proof.PrimaryKeyID,
		RetiringKeyID: proof.RetiringKeyID, Processed: checkpoint.Processed, Reencrypted: checkpoint.Reencrypted,
		NonPrimaryReferences: proof.NonPrimaryReferences, RetiringReferences: proof.RetiringReferences,
		RetirementSafe: proof.RetirementSafe})
	return jobengine.Outcome{Kind: jobengine.OutcomeSucceeded, ResultVersion: 1, Result: result, Err: err}
}

func (h mailRekeyHandler) reencryptTarget(target store.MailRekeyTarget, jobID model.JobID, primaryKeyID string) (*store.MailRekeyReplacement, error) {
	purpose, ok := mailRekeyTargetPurpose(target.Kind)
	if !ok || !model.IsValidId(target.ID) || len(target.EncryptedPayload) == 0 {
		return nil, errors.New("mail rekey target identity is invalid")
	}
	var envelope secretseal.Envelope
	if json.Unmarshal(target.EncryptedPayload, &envelope) != nil || envelope.KeyID != target.KeyID {
		return nil, errors.New("mail rekey envelope is invalid")
	}
	binding := secretseal.Binding{Purpose: purpose, Owner: target.ID}
	plaintext, err := h.sealer.Open(binding, envelope)
	if err != nil {
		return nil, err
	}
	defer clear(plaintext)
	rotated, err := h.sealer.Seal(binding, plaintext)
	if err != nil || rotated.KeyID != primaryKeyID {
		return nil, errors.New("mail rekey could not use the fenced primary key")
	}
	document, err := json.Marshal(rotated)
	if err != nil {
		return nil, err
	}
	return &store.MailRekeyReplacement{JobID: jobID, Kind: target.Kind, ID: target.ID,
		ExpectedKeyID: target.KeyID, PrimaryKeyID: primaryKeyID, EncryptedPayload: document}, nil
}

func decodeMailRekeyCheckpoint(version int, document json.RawMessage) (MailRekeyCheckpointV1, error) {
	var checkpoint MailRekeyCheckpointV1
	if len(document) == 0 {
		if version != 0 {
			return checkpoint, errors.New("mail rekey checkpoint version is invalid")
		}
		return checkpoint, nil
	}
	if version != 1 || decodeStrictUniqueJobDocument(document, &checkpoint) != nil ||
		checkpoint.Processed < 0 || checkpoint.Reencrypted < 0 || checkpoint.Reencrypted > checkpoint.Processed ||
		checkpoint.AfterID == "" || !model.IsValidId(checkpoint.AfterID) || !validMailRekeyTargetKind(checkpoint.AfterKind) {
		return checkpoint, errors.New("mail rekey checkpoint is invalid")
	}
	return checkpoint, nil
}

func checkpointMailRekey(ctx context.Context, execution jobengine.Execution, checkpoint MailRekeyCheckpointV1, total int64) error {
	document, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if total <= 0 || checkpoint.Processed > total {
		return errors.New("mail rekey progress is invalid")
	}
	return execution.Checkpoint(ctx, jobengine.CheckpointValue{Version: 1, Document: document,
		Progress: &model.JobProgress{Current: checkpoint.Processed, Total: total, Stage: "reencrypting"}})
}

func validateMailRekeyProof(proof *store.MailRekeyProof, command MailRekeyCommandV1) error {
	if proof == nil || proof.PrimaryKeyID != command.PrimaryKeyID || proof.RetiringKeyID != command.RetiringKeyID ||
		proof.NonPrimaryReferences < 0 || proof.RetiringReferences < 0 || proof.RetiringReferences > proof.NonPrimaryReferences ||
		proof.RetirementSafe != (proof.RetiringReferences == 0) {
		return errors.New("mail rekey proof is invalid")
	}
	return nil
}

func mailRekeyTargetAfter(target store.MailRekeyTarget, checkpoint MailRekeyCheckpointV1) bool {
	if !validMailRekeyTargetKind(target.Kind) || !model.IsValidId(target.ID) {
		return false
	}
	if checkpoint.AfterKind == "" {
		return true
	}
	return target.Kind > checkpoint.AfterKind || (target.Kind == checkpoint.AfterKind && target.ID > checkpoint.AfterID)
}

func validMailRekeyTargetKind(kind store.MailRekeyTargetKind) bool {
	return kind == store.MailRekeyTargetDelivery || kind == store.MailRekeyTargetFanoutBundle
}

func mailRekeyTargetPurpose(kind store.MailRekeyTargetKind) (string, bool) {
	switch kind {
	case store.MailRekeyTargetDelivery:
		return appmail.DeliverySealingPurpose, true
	case store.MailRekeyTargetFanoutBundle:
		return mailFanoutBundleSealPurpose, true
	default:
		return "", false
	}
}

func mailRekeyOutcome(err error) jobengine.Outcome {
	if store.IsConflict(err) {
		return jobengine.PermanentFailure("mail.rekey.fenced", err)
	}
	return jobengine.RetryableFailure("dependency.unavailable", err)
}

func mailRekeyDescriptor(handler jobengine.Handler) jobengine.Descriptor {
	return jobengine.Descriptor{Type: model.JobTypeMailRekey, CommandVersions: []int{1}, CheckpointVersions: []int{1},
		ResultVersions: []int{1}, ProgressStages: []string{"reencrypting"}, PublicErrorCodes: []string{"dependency.unavailable", "job.checkpoint.invalid", "mail.rekey.fenced", "mail.rekey.incomplete", "mail.rekey.invariant_failed", "mail.rekey.payload_unavailable", "mail.rekey.primary_mismatch"},
		Timeout: 5 * time.Minute, Concurrency: 1, MaximumAttempts: 10, LeaseDuration: time.Minute,
		HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Minute, MaximumRetryDelay: 30 * time.Minute,
		Visibility: jobengine.VisibilityOperator, SuccessRetention: 90 * 24 * time.Hour,
		FailureRetention: 180 * 24 * time.Hour, Handler: handler}
}
