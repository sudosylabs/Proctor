// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobhandler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/job"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type mfaRekeyStoreFake struct {
	requests     []store.MFARekeyTargetPageRequest
	pages        []*store.MFARekeyTargetPage
	replacements []*store.MFARekeyReplacement
	proofs       []*store.MFARekeyProof
}

func (f *mfaRekeyStoreFake) ListRekeyTargets(_ context.Context, request *store.MFARekeyTargetPageRequest) (*store.MFARekeyTargetPage, error) {
	f.requests = append(f.requests, *request)
	if len(f.pages) == 0 {
		return &store.MFARekeyTargetPage{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *mfaRekeyStoreFake) ReplaceRekeyTarget(_ context.Context, input *store.MFARekeyReplacement) (bool, error) {
	f.replacements = append(f.replacements, input)
	return true, nil
}

func (f *mfaRekeyStoreFake) ProveRekey(context.Context, *store.MFARekeyProofRequest) (*store.MFARekeyProof, error) {
	proof := f.proofs[0]
	if len(f.proofs) > 1 {
		f.proofs = f.proofs[1:]
	}
	return proof, nil
}

type mfaRekeyFixture struct {
	oldSealer, sealer *secretseal.Sealer
}

func newMFARekeyFixture(t *testing.T) mfaRekeyFixture {
	t.Helper()
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mfa-rekey-key-material-00001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mfa-rekey-key-material-00001"))
	oldSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, MaximumPlaintext: 128})
	require.NoError(t, err)
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 128})
	require.NoError(t, err)
	return mfaRekeyFixture{oldSealer: oldSealer, sealer: sealer}
}

func (f mfaRekeyFixture) target(t *testing.T, secret string) store.MFARekeyTarget {
	t.Helper()
	userID := model.NewUserID()
	envelope, err := f.oldSealer.Seal(secretseal.Binding{Purpose: MFASealingPurpose, Owner: userID.String()}, []byte(secret))
	require.NoError(t, err)
	encoded, err := json.Marshal(envelope)
	require.NoError(t, err)
	return store.MFARekeyTarget{CredentialID: model.NewMFACredentialID(), UserID: userID, KeyID: envelope.KeyID, EncryptedSecret: string(encoded)}
}

func (f mfaRekeyFixture) execution(t *testing.T, checkpoint json.RawMessage, checkpoints *[]job.CheckpointValue) job.Execution {
	t.Helper()
	command, err := json.Marshal(MFARekeyCommand{PrimaryKeyID: f.sealer.PrimaryKeyID(), RetiringKeyID: f.oldSealer.PrimaryKeyID()})
	require.NoError(t, err)
	return job.NewExecution(&model.Job{ID: model.NewJobID(), Type: model.JobTypeMFARekey, Command: command, Checkpoint: checkpoint},
		&model.JobAttempt{ID: model.NewJobAttemptID()}, func(_ context.Context, value job.CheckpointValue) error {
			*checkpoints = append(*checkpoints, value)
			return nil
		}, nil)
}

func (f mfaRekeyFixture) proof(nonPrimary int64) *store.MFARekeyProof {
	return &store.MFARekeyProof{PrimaryKeyID: f.sealer.PrimaryKeyID(), RetiringKeyID: f.oldSealer.PrimaryKeyID(),
		NonPrimaryReferences: nonPrimary, RetiringReferences: nonPrimary, RetirementSafe: nonPrimary == 0}
}

func TestMFARekeyHandlerResealsUnderTheOwnerBindingAndCheckpointsEachCredential(t *testing.T) {
	t.Parallel()
	fixture := newMFARekeyFixture(t)
	first, second := fixture.target(t, "JBSWY3DPEHPK3PXP"), fixture.target(t, "KRSXG5CTMVRXEZLU")
	if second.CredentialID.String() < first.CredentialID.String() {
		first, second = second, first
	}
	persistence := &mfaRekeyStoreFake{pages: []*store.MFARekeyTargetPage{{Targets: []store.MFARekeyTarget{first}, More: true},
		{Targets: []store.MFARekeyTarget{second}}}, proofs: []*store.MFARekeyProof{fixture.proof(2), fixture.proof(0)}}
	var checkpoints []job.CheckpointValue
	execution := fixture.execution(t, nil, &checkpoints)

	outcome := (mfaRekeyHandler{credentials: persistence, sealer: fixture.sealer}).Run(context.Background(), execution)

	require.Equal(t, job.OutcomeSucceeded, outcome.Kind, "outcome = %#v", outcome)
	require.Len(t, persistence.replacements, 2)
	for index, target := range []store.MFARekeyTarget{first, second} {
		replacement := persistence.replacements[index]
		require.Equal(t, target.CredentialID, replacement.CredentialID)
		require.Equal(t, target.KeyID, replacement.ExpectedKeyID, "the replacement is a compare-and-swap on the old key")
		require.Equal(t, fixture.sealer.PrimaryKeyID(), replacement.PrimaryKeyID)
		var rotated secretseal.Envelope
		require.NoError(t, json.Unmarshal([]byte(replacement.EncryptedSecret), &rotated))
		require.Equal(t, fixture.sealer.PrimaryKeyID(), rotated.KeyID)
		plaintext, err := fixture.sealer.Open(secretseal.Binding{Purpose: MFASealingPurpose, Owner: target.UserID.String()}, rotated)
		require.NoError(t, err, "the re-sealed secret stays bound to its owner")
		original, err := fixture.sealer.Open(secretseal.Binding{Purpose: MFASealingPurpose, Owner: target.UserID.String()}, mustEnvelope(t, target.EncryptedSecret))
		require.NoError(t, err)
		require.Equal(t, original, plaintext)
	}
	// Contract: the cursor advances only after each replacement commits.
	require.Equal(t, []store.MFARekeyTargetPageRequest{
		{JobID: execution.Job.ID, PrimaryKeyID: fixture.sealer.PrimaryKeyID(), Limit: mfaRekeyPageSize},
		{JobID: execution.Job.ID, PrimaryKeyID: fixture.sealer.PrimaryKeyID(), AfterCredentialID: first.CredentialID, Limit: mfaRekeyPageSize},
	}, persistence.requests)
	require.Len(t, checkpoints, 2)
	last, err := DecodeMFARekeyCheckpoint(checkpoints[1].Document)
	require.NoError(t, err)
	require.Equal(t, MFARekeyCheckpoint{AfterCredentialID: second.CredentialID, Processed: 2, Resealed: 2}, last)
	require.Equal(t, &model.JobProgress{Current: 2, Total: 2, Stage: "resealing"}, checkpoints[1].Progress)
	result, err := DecodeMFARekeyResult(outcome.Result)
	require.NoError(t, err)
	require.Equal(t, MFARekeyResult{PrimaryKeyID: fixture.sealer.PrimaryKeyID(), RetiringKeyID: fixture.oldSealer.PrimaryKeyID(),
		Processed: 2, Resealed: 2, RetirementSafe: true}, result)
}

func TestMFARekeyHandlerResumesAfterItsCheckpoint(t *testing.T) {
	t.Parallel()
	fixture := newMFARekeyFixture(t)
	resumeAfter := model.NewMFACredentialID()
	checkpoint, err := json.Marshal(MFARekeyCheckpoint{AfterCredentialID: resumeAfter, Processed: 7, Resealed: 6})
	require.NoError(t, err)
	persistence := &mfaRekeyStoreFake{proofs: []*store.MFARekeyProof{fixture.proof(0)}}
	var checkpoints []job.CheckpointValue

	outcome := (mfaRekeyHandler{credentials: persistence, sealer: fixture.sealer}).Run(context.Background(), fixture.execution(t, checkpoint, &checkpoints))

	require.Equal(t, job.OutcomeSucceeded, outcome.Kind)
	require.Equal(t, resumeAfter, persistence.requests[0].AfterCredentialID, "a retried attempt scans after the committed cursor")
	result, err := DecodeMFARekeyResult(outcome.Result)
	require.NoError(t, err)
	require.Equal(t, int64(7), result.Processed, "progress survives the crash")
	require.Equal(t, int64(6), result.Resealed)
}

func TestMFARekeyHandlerRefusesRetirementWhileTheRetiringKeySealsALiveSecret(t *testing.T) {
	t.Parallel()
	fixture := newMFARekeyFixture(t)
	persistence := &mfaRekeyStoreFake{proofs: []*store.MFARekeyProof{fixture.proof(1)}}
	var checkpoints []job.CheckpointValue

	outcome := (mfaRekeyHandler{credentials: persistence, sealer: fixture.sealer}).Run(context.Background(), fixture.execution(t, nil, &checkpoints))

	require.Equal(t, job.OutcomeRetryableFailure, outcome.Kind)
	require.Equal(t, "mfa.rekey.incomplete", outcome.PublicErrorCode)
	require.Empty(t, outcome.Result, "no retirement proof is recorded")
}

func TestMFARekeyHandlerRelinquishesToANodeWithTheFencedPrimary(t *testing.T) {
	t.Parallel()
	fixture := newMFARekeyFixture(t)
	persistence := &mfaRekeyStoreFake{}
	var checkpoints []job.CheckpointValue

	outcome := (mfaRekeyHandler{credentials: persistence, sealer: fixture.oldSealer}).Run(context.Background(), fixture.execution(t, nil, &checkpoints))

	require.Equal(t, job.OutcomeRelinquished, outcome.Kind)
	require.Equal(t, "mfa.rekey.primary_mismatch", outcome.PublicErrorCode)
	require.Empty(t, persistence.requests)
}

func mustEnvelope(t *testing.T, encoded string) secretseal.Envelope {
	t.Helper()
	var envelope secretseal.Envelope
	require.NoError(t, json.Unmarshal([]byte(encoded), &envelope))
	return envelope
}
