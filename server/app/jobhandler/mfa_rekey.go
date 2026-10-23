// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

// MFASealingPurpose authenticates every TOTP secret envelope to its purpose;
// the owner is the credential's User.
const MFASealingPurpose = "mfa.totp"

const mfaRekeyPageSize = 100

// MFARekeyCommand is the immutable command of the MFA rekey Job.
type MFARekeyCommand struct {
	PrimaryKeyID  string `json:"primary_key_id"`
	RetiringKeyID string `json:"retiring_key_id"`
}

// MFARekeyCheckpoint is the resumable checkpoint of the MFA rekey Job.
type MFARekeyCheckpoint struct {
	AfterCredentialID model.MFACredentialID `json:"after_credential_id"`
	Processed         int64                 `json:"processed"`
	Resealed          int64                 `json:"resealed"`
}

// MFARekeyResult is the safe result of the MFA rekey Job: the retirement proof.
type MFARekeyResult struct {
	PrimaryKeyID         string `json:"primary_key_id"`
	RetiringKeyID        string `json:"retiring_key_id"`
	Processed            int64  `json:"processed"`
	Resealed             int64  `json:"resealed"`
	NonPrimaryReferences int64  `json:"non_primary_references"`
	RetiringReferences   int64  `json:"retiring_references"`
	RetirementSafe       bool   `json:"retirement_safe"`
}

// DecodeMFARekeyCommand strictly decodes an MFA rekey Job command.
func DecodeMFARekeyCommand(document json.RawMessage) (MFARekeyCommand, error) {
	var value MFARekeyCommand
	err := decodeStrictUniqueJobDocument(document, &value)
	return value, err
}

// DecodeMFARekeyCheckpoint strictly decodes and validates an MFA rekey Job checkpoint.
func DecodeMFARekeyCheckpoint(document json.RawMessage) (MFARekeyCheckpoint, error) {
	return decodeMFARekeyCheckpoint(document)
}

// DecodeMFARekeyResult strictly decodes an MFA rekey Job result.
func DecodeMFARekeyResult(document json.RawMessage) (MFARekeyResult, error) {
	var value MFARekeyResult
	err := decodeStrictUniqueJobDocument(document, &value)
	return value, err
}

// MFARekeyJobStore is the Store surface the MFA rekey handler uses.
type MFARekeyJobStore interface {
	ListRekeyTargets(context.Context, *store.MFARekeyTargetPageRequest) (*store.MFARekeyTargetPage, error)
	ReplaceRekeyTarget(context.Context, *store.MFARekeyReplacement) (bool, error)
	ProveRekey(context.Context, *store.MFARekeyProofRequest) (*store.MFARekeyProof, error)
}

// mfaRekeyHandler re-seals every live TOTP secret under the fenced primary.
// Each replacement is a compare-and-swap on the credential's key identity and
// is checkpointed after it commits, so a retry or another node resumes after
// the last re-sealed credential and a replayed replacement is a no-op.
type mfaRekeyHandler struct {
	credentials MFARekeyJobStore
	sealer      *secretseal.Sealer
}

func (h mfaRekeyHandler) Run(ctx context.Context, execution job.Execution) job.Outcome {
	if execution.Job == nil || execution.Attempt == nil || !execution.Job.ID.IsValid() || !execution.Attempt.ID.IsValid() ||
		h.credentials == nil || h.sealer == nil {
		return job.PermanentFailure("job.command.invalid", errors.New("MFA rekey execution is incomplete"))
	}
	command, err := DecodeMFARekeyCommand(execution.Job.Command)
	if err != nil || command.PrimaryKeyID == "" || command.RetiringKeyID == "" || command.RetiringKeyID == command.PrimaryKeyID ||
		!h.sealer.HasKey(command.RetiringKeyID) {
		return job.PermanentFailure("job.command.invalid", errors.New("MFA rekey command is invalid for this key ring"))
	}
	if command.PrimaryKeyID != h.sealer.PrimaryKeyID() {
		return job.Relinquished("mfa.rekey.primary_mismatch", errors.New("MFA rekey command requires a different configured primary key"))
	}
	checkpoint, err := decodeMFARekeyCheckpoint(execution.Job.Checkpoint)
	if err != nil {
		return job.PermanentFailure("job.checkpoint.invalid", err)
	}
	proofRequest := &store.MFARekeyProofRequest{JobID: execution.Job.ID, PrimaryKeyID: command.PrimaryKeyID, RetiringKeyID: command.RetiringKeyID}
	initialProof, err := h.credentials.ProveRekey(ctx, proofRequest)
	if err != nil {
		return mfaRekeyOutcome(err)
	}
	if err = validateMFARekeyProof(initialProof, command); err != nil {
		return job.PermanentFailure("mfa.rekey.invariant_failed", err)
	}
	if initialProof.NonPrimaryReferences > math.MaxInt64-checkpoint.Processed {
		return job.PermanentFailure("mfa.rekey.invariant_failed", errors.New("MFA rekey progress exceeds its bound"))
	}
	progressTotal := checkpoint.Processed + initialProof.NonPrimaryReferences
	for {
		page, listErr := h.credentials.ListRekeyTargets(ctx, &store.MFARekeyTargetPageRequest{JobID: execution.Job.ID,
			PrimaryKeyID: command.PrimaryKeyID, AfterCredentialID: checkpoint.AfterCredentialID, Limit: mfaRekeyPageSize})
		if listErr != nil {
			return mfaRekeyOutcome(listErr)
		}
		if page == nil || len(page.Targets) > mfaRekeyPageSize || (page.More && len(page.Targets) == 0) {
			return job.PermanentFailure("mfa.rekey.invariant_failed", errors.New("MFA rekey page is invalid"))
		}
		for _, target := range page.Targets {
			if !target.CredentialID.IsValid() || target.CredentialID.String() <= checkpoint.AfterCredentialID.String() ||
				target.KeyID == command.PrimaryKeyID || !h.sealer.HasKey(target.KeyID) {
				return job.PermanentFailure("mfa.rekey.invariant_failed", errors.New("MFA rekey target is invalid"))
			}
			replacement, sealErr := h.reseal(target, execution.Job.ID, command.PrimaryKeyID)
			if sealErr != nil {
				return job.PermanentFailure("mfa.rekey.secret_unavailable", sealErr)
			}
			applied, replaceErr := h.credentials.ReplaceRekeyTarget(ctx, replacement)
			if replaceErr != nil {
				return mfaRekeyOutcome(replaceErr)
			}
			checkpoint.AfterCredentialID = target.CredentialID
			checkpoint.Processed++
			if applied {
				checkpoint.Resealed++
			}
			if checkpointErr := checkpointMFARekey(ctx, execution, checkpoint, max(progressTotal, checkpoint.Processed)); checkpointErr != nil {
				return job.RetryableFailure("dependency.unavailable", checkpointErr)
			}
		}
		if !page.More {
			break
		}
	}
	proof, err := h.credentials.ProveRekey(ctx, proofRequest)
	if err != nil {
		return mfaRekeyOutcome(err)
	}
	if err = validateMFARekeyProof(proof, command); err != nil {
		return job.PermanentFailure("mfa.rekey.invariant_failed", err)
	}
	if proof.NonPrimaryReferences != 0 || !proof.RetirementSafe {
		// A credential the scan passed was replaced meanwhile by one sealed
		// with an older key only if the fence was bypassed; retry rescans.
		return job.RetryableFailure("mfa.rekey.incomplete", errors.New("live MFA secrets still use a non-primary key"))
	}
	result, err := json.Marshal(MFARekeyResult{PrimaryKeyID: proof.PrimaryKeyID, RetiringKeyID: proof.RetiringKeyID,
		Processed: checkpoint.Processed, Resealed: checkpoint.Resealed, NonPrimaryReferences: proof.NonPrimaryReferences,
		RetiringReferences: proof.RetiringReferences, RetirementSafe: proof.RetirementSafe})
	return job.Outcome{Kind: job.OutcomeSucceeded, Result: result, Err: err}
}

func (h mfaRekeyHandler) reseal(target store.MFARekeyTarget, jobID model.JobID, primaryKeyID string) (*store.MFARekeyReplacement, error) {
	if !target.UserID.IsValid() || target.EncryptedSecret == "" {
		return nil, errors.New("MFA rekey target identity is invalid")
	}
	var envelope secretseal.Envelope
	if json.Unmarshal([]byte(target.EncryptedSecret), &envelope) != nil || envelope.KeyID != target.KeyID {
		return nil, errors.New("MFA rekey envelope is invalid")
	}
	binding := secretseal.Binding{Purpose: MFASealingPurpose, Owner: target.UserID.String()}
	plaintext, err := h.sealer.Open(binding, envelope)
	if err != nil {
		return nil, err
	}
	defer clear(plaintext)
	rotated, err := h.sealer.Seal(binding, plaintext)
	if err != nil || rotated.KeyID != primaryKeyID {
		return nil, errors.New("MFA rekey could not use the fenced primary key")
	}
	document, err := json.Marshal(rotated)
	if err != nil {
		return nil, err
	}
	return &store.MFARekeyReplacement{JobID: jobID, CredentialID: target.CredentialID, ExpectedKeyID: target.KeyID,
		PrimaryKeyID: primaryKeyID, EncryptedSecret: string(document)}, nil
}

func decodeMFARekeyCheckpoint(document json.RawMessage) (MFARekeyCheckpoint, error) {
	var checkpoint MFARekeyCheckpoint
	if len(document) == 0 {
		return checkpoint, nil
	}
	if decodeStrictUniqueJobDocument(document, &checkpoint) != nil || checkpoint.Processed < 1 || checkpoint.Resealed < 0 ||
		checkpoint.Resealed > checkpoint.Processed || !checkpoint.AfterCredentialID.IsValid() {
		return checkpoint, errors.New("MFA rekey checkpoint is invalid")
	}
	return checkpoint, nil
}

func checkpointMFARekey(ctx context.Context, execution job.Execution, checkpoint MFARekeyCheckpoint, total int64) error {
	document, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	return execution.Checkpoint(ctx, job.CheckpointValue{Document: document,
		Progress: &model.JobProgress{Current: checkpoint.Processed, Total: total, Stage: "resealing"}})
}

func validateMFARekeyProof(proof *store.MFARekeyProof, command MFARekeyCommand) error {
	if proof == nil || proof.PrimaryKeyID != command.PrimaryKeyID || proof.RetiringKeyID != command.RetiringKeyID ||
		proof.NonPrimaryReferences < 0 || proof.RetiringReferences < 0 || proof.RetiringReferences > proof.NonPrimaryReferences ||
		proof.RetirementSafe != (proof.RetiringReferences == 0) {
		return errors.New("MFA rekey proof is invalid")
	}
	return nil
}

func mfaRekeyOutcome(err error) job.Outcome {
	if store.IsConflict(err) {
		return job.PermanentFailure("mfa.rekey.fenced", err)
	}
	return job.RetryableFailure("dependency.unavailable", err)
}

func mfaRekeyDescriptor(handler job.Handler) job.Descriptor {
	return job.Descriptor{Type: model.JobTypeMFARekey, Checkpoints: true,
		ProgressStages: []string{"resealing"}, PublicErrorCodes: []string{"dependency.unavailable", "job.checkpoint.invalid", "job.command.invalid", "mfa.rekey.fenced", "mfa.rekey.incomplete", "mfa.rekey.invariant_failed", "mfa.rekey.primary_mismatch", "mfa.rekey.secret_unavailable"},
		Timeout: 5 * time.Minute, Concurrency: 1, MaximumAttempts: 10, LeaseDuration: time.Minute,
		HeartbeatInterval: 15 * time.Second, BaseRetryDelay: time.Minute, MaximumRetryDelay: 30 * time.Minute,
		Visibility: job.VisibilityOperator, SuccessRetention: 90 * 24 * time.Hour,
		FailureRetention: 180 * 24 * time.Hour, Handler: handler}
}
