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

	jobengine "github.com/sudosylabs/proctor/server/app/job"
	appmail "github.com/sudosylabs/proctor/server/app/mail"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type mailRekeyStoreFake struct {
	pages        []*store.MailRekeyTargetPage
	replacements []*store.MailRekeyReplacement
	proof        *store.MailRekeyProof
	proofs       []*store.MailRekeyProof
}

func (f *mailRekeyStoreFake) ListRekeyTargets(_ context.Context, _ *store.MailRekeyTargetPageRequest) (*store.MailRekeyTargetPage, error) {
	if len(f.pages) == 0 {
		return &store.MailRekeyTargetPage{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *mailRekeyStoreFake) ReplaceRekeyTarget(_ context.Context, input *store.MailRekeyReplacement) (bool, error) {
	f.replacements = append(f.replacements, input)
	return true, nil
}

func (f *mailRekeyStoreFake) ProveRekey(context.Context, *store.MailRekeyProofRequest) (*store.MailRekeyProof, error) {
	if len(f.proofs) > 0 {
		proof := f.proofs[0]
		f.proofs = f.proofs[1:]
		return proof, nil
	}
	return f.proof, nil
}

func TestMailRekeyHandlerReencryptsBoundPayloadAndCheckpoints(t *testing.T) {
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	oldSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, MaximumPlaintext: 1024})
	require.NoError(t, err)
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	deliveryID := model.NewMailDeliveryID()
	envelope, err := oldSealer.Seal(secretseal.Binding{Purpose: appmail.DeliverySealingPurpose, Owner: deliveryID.String()}, []byte(`{"version":1,"secret":"payload"}`))
	require.NoError(t, err)
	encrypted, _ := json.Marshal(envelope)
	persistence := &mailRekeyStoreFake{
		pages: []*store.MailRekeyTargetPage{{Targets: []store.MailRekeyTarget{{
			Kind: store.MailRekeyTargetDelivery, ID: deliveryID.String(), KeyID: envelope.KeyID,
			EncryptedPayload: encrypted,
		}}}, {}},
		proofs: []*store.MailRekeyProof{
			{PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: envelope.KeyID, NonPrimaryReferences: 1, RetiringReferences: 1, RetirementSafe: false},
			{PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: envelope.KeyID, RetirementSafe: true},
		},
	}
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: envelope.KeyID})
	job := &model.Job{ID: model.NewJobID(), Type: model.JobType("mail.rekey"), CommandVersion: 1, Command: command}
	attempt := &model.JobAttempt{ID: model.NewJobAttemptID()}
	var checkpoints []jobengine.CheckpointValue
	execution := jobengine.NewExecution(job, attempt, func(_ context.Context, value jobengine.CheckpointValue) error {
		checkpoints = append(checkpoints, value)
		return nil
	}, nil)

	outcome := (mailRekeyHandler{mail: persistence, sealer: sealer}).Run(context.Background(), execution)
	require.Equal(t, jobengine.OutcomeSucceeded, outcome.Kind)
	require.Equal(t, 1, outcome.ResultVersion)
	require.Len(t, persistence.replacements, 1, "replacements")
	require.Len(t, checkpoints, 1, "replacements")
	require.NotNil(t, checkpoints[0].Progress)
	require.Equal(t, int64(1), checkpoints[0].Progress.Current)
	require.Equal(t, int64(1), checkpoints[0].Progress.Total)
	require.Equal(t, "reencrypting", checkpoints[0].Progress.Stage)
	replacement := persistence.replacements[0]
	require.Equal(t, job.ID, replacement.JobID)
	require.Equal(t, store.MailRekeyTargetDelivery, replacement.Kind)
	require.Equal(t, deliveryID.String(), replacement.ID)
	require.Equal(t, envelope.KeyID, replacement.ExpectedKeyID)
	require.Equal(t, sealer.PrimaryKeyID(), replacement.PrimaryKeyID)
	var rotated secretseal.Envelope
	err = json.Unmarshal(replacement.EncryptedPayload, &rotated)
	require.NoError(t, err, "rotated envelope = %#v, error", rotated)
	require.Equal(t, sealer.PrimaryKeyID(), rotated.KeyID, "rotated envelope = %#v, error", rotated)
	plaintext, err := sealer.Open(secretseal.Binding{Purpose: appmail.DeliverySealingPurpose, Owner: deliveryID.String()}, rotated)
	require.NoError(t, err, "rotated plaintext")
	require.Equal(t, `{"version":1,"secret":"payload"}`, string(plaintext), "rotated plaintext")
	var checkpoint MailRekeyCheckpointV1
	err = json.Unmarshal(checkpoints[0].Document, &checkpoint)
	require.NoError(t, err, "checkpoint = %#v, error", checkpoint)
	require.Equal(t, deliveryID.String(), checkpoint.AfterID, "checkpoint = %#v, error", checkpoint)
	require.Equal(t, int64(1), checkpoint.Processed, "checkpoint = %#v, error", checkpoint)
	require.Equal(t, int64(1), checkpoint.Reencrypted, "checkpoint = %#v, error", checkpoint)
	var result MailRekeyResultV1
	err = json.Unmarshal(outcome.Result, &result)
	require.NoError(t, err, "result = %#v, error", result)
	require.True(t, result.RetirementSafe, "result = %#v, error", result)
	require.Equal(t, int64(1), result.Processed, "result = %#v, error", result)
	require.Equal(t, int64(1), result.Reencrypted, "result = %#v, error", result)
}

func TestMailRekeyHandlerFailsClosedForUnavailableActiveKey(t *testing.T) {
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	unavailableKey := base64.StdEncoding.EncodeToString([]byte("lost-mail-rekey-key-material-001"))
	unavailableSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: unavailableKey, MaximumPlaintext: 1024})
	require.NoError(t, err)
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	deliveryID := model.NewMailDeliveryID()
	envelope, err := unavailableSealer.Seal(secretseal.Binding{Purpose: appmail.DeliverySealingPurpose, Owner: deliveryID.String()}, []byte(`{"version":1}`))
	require.NoError(t, err)
	encrypted, _ := json.Marshal(envelope)
	persistence := &mailRekeyStoreFake{pages: []*store.MailRekeyTargetPage{{Targets: []store.MailRekeyTarget{{
		Kind: store.MailRekeyTargetDelivery, ID: deliveryID.String(), KeyID: envelope.KeyID, EncryptedPayload: encrypted,
	}}}}}
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: oldSealerKeyID(t, oldKey)})
	execution := jobengine.NewExecution(&model.Job{ID: model.NewJobID(), Type: model.JobTypeMailRekey,
		CommandVersion: 1, Command: command}, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, nil)

	outcome := (mailRekeyHandler{mail: persistence, sealer: sealer}).Run(context.Background(), execution)
	require.Equal(t, jobengine.OutcomePermanentFailure, outcome.Kind, "Run() outcome = %#v, replacements", outcome)
	require.Equal(t, "mail.rekey.invariant_failed", outcome.PublicErrorCode, "Run() outcome = %#v, replacements", outcome)
	require.Empty(t, persistence.replacements, "Run() outcome = %#v, replacements", outcome)
}

func TestMailRekeyHandlerRefusesRetirementUntilEveryActiveReferenceIsPrimary(t *testing.T) {
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	retiringKeyID := oldSealerKeyID(t, oldKey)
	persistence := &mailRekeyStoreFake{pages: []*store.MailRekeyTargetPage{{}}, proof: &store.MailRekeyProof{
		PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: retiringKeyID,
		NonPrimaryReferences: 2, RetiringReferences: 2, RetirementSafe: false,
	}}
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: retiringKeyID})
	execution := jobengine.NewExecution(&model.Job{ID: model.NewJobID(), Type: model.JobTypeMailRekey,
		CommandVersion: 1, Command: command}, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, nil)

	outcome := (mailRekeyHandler{mail: persistence, sealer: sealer}).Run(context.Background(), execution)
	require.Equal(t, jobengine.OutcomeRetryableFailure, outcome.Kind)
	require.Equal(t, "mail.rekey.incomplete", outcome.PublicErrorCode)
}

func TestMailRekeyHandlerRelinquishesToANodeWithTheFencedPrimary(t *testing.T) {
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	oldNodeSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, DecryptionKeys: []string{newKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	newNodeSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	require.NoError(t, err)
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: newNodeSealer.PrimaryKeyID(), RetiringKeyID: oldNodeSealer.PrimaryKeyID()})
	execution := jobengine.NewExecution(&model.Job{ID: model.NewJobID(), Type: model.JobTypeMailRekey,
		CommandVersion: 1, Command: command}, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, nil)
	persistence := &mailRekeyStoreFake{}

	outcome := (mailRekeyHandler{mail: persistence, sealer: oldNodeSealer}).Run(context.Background(), execution)

	require.Equal(t, jobengine.OutcomeRelinquished, outcome.Kind, "Run() outcome = %#v, pages", outcome)
	require.Equal(t, "mail.rekey.primary_mismatch", outcome.PublicErrorCode, "Run() outcome = %#v, pages", outcome)
	require.Empty(t, persistence.pages, "Run() outcome = %#v, pages", outcome)
	require.Empty(t, persistence.replacements, "Run() outcome = %#v, pages", outcome)
}

func oldSealerKeyID(t *testing.T, key string) string {
	t.Helper()
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: key, MaximumPlaintext: 1})
	require.NoError(t, err)
	return sealer.PrimaryKeyID()
}
