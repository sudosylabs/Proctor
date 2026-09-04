// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package jobs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

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
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	deliveryID := model.NewMailDeliveryID()
	envelope, err := oldSealer.Seal(secretseal.Binding{Purpose: appmail.DeliverySealingPurpose, Owner: deliveryID.String()}, []byte(`{"version":1,"secret":"payload"}`))
	if err != nil {
		t.Fatal(err)
	}
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
	if outcome.Kind != jobengine.OutcomeSucceeded || outcome.ResultVersion != 1 {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if len(persistence.replacements) != 1 || len(checkpoints) != 1 {
		t.Fatalf("replacements=%d checkpoints=%d", len(persistence.replacements), len(checkpoints))
	}
	if checkpoints[0].Progress == nil || checkpoints[0].Progress.Current != 1 || checkpoints[0].Progress.Total != 1 ||
		checkpoints[0].Progress.Stage != "reencrypting" {
		t.Fatalf("safe progress = %#v", checkpoints[0].Progress)
	}
	replacement := persistence.replacements[0]
	if replacement.JobID != job.ID || replacement.Kind != store.MailRekeyTargetDelivery ||
		replacement.ID != deliveryID.String() || replacement.ExpectedKeyID != envelope.KeyID ||
		replacement.PrimaryKeyID != sealer.PrimaryKeyID() {
		t.Fatalf("replacement = %#v", replacement)
	}
	var rotated secretseal.Envelope
	if err = json.Unmarshal(replacement.EncryptedPayload, &rotated); err != nil || rotated.KeyID != sealer.PrimaryKeyID() {
		t.Fatalf("rotated envelope = %#v, error = %v", rotated, err)
	}
	plaintext, err := sealer.Open(secretseal.Binding{Purpose: appmail.DeliverySealingPurpose, Owner: deliveryID.String()}, rotated)
	if err != nil || string(plaintext) != `{"version":1,"secret":"payload"}` {
		t.Fatalf("rotated plaintext = %q, error = %v", plaintext, err)
	}
	var checkpoint MailRekeyCheckpointV1
	if err = json.Unmarshal(checkpoints[0].Document, &checkpoint); err != nil || checkpoint.AfterID != deliveryID.String() || checkpoint.Processed != 1 || checkpoint.Reencrypted != 1 {
		t.Fatalf("checkpoint = %#v, error = %v", checkpoint, err)
	}
	var result MailRekeyResultV1
	if err = json.Unmarshal(outcome.Result, &result); err != nil || !result.RetirementSafe || result.Processed != 1 || result.Reencrypted != 1 {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestMailRekeyHandlerFailsClosedForUnavailableActiveKey(t *testing.T) {
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	unavailableKey := base64.StdEncoding.EncodeToString([]byte("lost-mail-rekey-key-material-001"))
	unavailableSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: unavailableKey, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	deliveryID := model.NewMailDeliveryID()
	envelope, err := unavailableSealer.Seal(secretseal.Binding{Purpose: appmail.DeliverySealingPurpose, Owner: deliveryID.String()}, []byte(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, _ := json.Marshal(envelope)
	persistence := &mailRekeyStoreFake{pages: []*store.MailRekeyTargetPage{{Targets: []store.MailRekeyTarget{{
		Kind: store.MailRekeyTargetDelivery, ID: deliveryID.String(), KeyID: envelope.KeyID, EncryptedPayload: encrypted,
	}}}}}
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: oldSealerKeyID(t, oldKey)})
	execution := jobengine.NewExecution(&model.Job{ID: model.NewJobID(), Type: model.JobTypeMailRekey,
		CommandVersion: 1, Command: command}, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, nil)

	outcome := (mailRekeyHandler{mail: persistence, sealer: sealer}).Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomePermanentFailure || outcome.PublicErrorCode != "mail.rekey.invariant_failed" ||
		len(persistence.replacements) != 0 {
		t.Fatalf("Run() outcome = %#v, replacements = %d", outcome, len(persistence.replacements))
	}
}

func TestMailRekeyHandlerRefusesRetirementUntilEveryActiveReferenceIsPrimary(t *testing.T) {
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	retiringKeyID := oldSealerKeyID(t, oldKey)
	persistence := &mailRekeyStoreFake{pages: []*store.MailRekeyTargetPage{{}}, proof: &store.MailRekeyProof{
		PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: retiringKeyID,
		NonPrimaryReferences: 2, RetiringReferences: 2, RetirementSafe: false,
	}}
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: sealer.PrimaryKeyID(), RetiringKeyID: retiringKeyID})
	execution := jobengine.NewExecution(&model.Job{ID: model.NewJobID(), Type: model.JobTypeMailRekey,
		CommandVersion: 1, Command: command}, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, nil)

	outcome := (mailRekeyHandler{mail: persistence, sealer: sealer}).Run(context.Background(), execution)
	if outcome.Kind != jobengine.OutcomeRetryableFailure || outcome.PublicErrorCode != "mail.rekey.incomplete" {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
}

func TestMailRekeyHandlerRelinquishesToANodeWithTheFencedPrimary(t *testing.T) {
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mail-rekey-key-material-0001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mail-rekey-key-material-0001"))
	oldNodeSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: oldKey, DecryptionKeys: []string{newKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	newNodeSealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 1024})
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: newNodeSealer.PrimaryKeyID(), RetiringKeyID: oldNodeSealer.PrimaryKeyID()})
	execution := jobengine.NewExecution(&model.Job{ID: model.NewJobID(), Type: model.JobTypeMailRekey,
		CommandVersion: 1, Command: command}, &model.JobAttempt{ID: model.NewJobAttemptID()}, nil, nil)
	persistence := &mailRekeyStoreFake{}

	outcome := (mailRekeyHandler{mail: persistence, sealer: oldNodeSealer}).Run(context.Background(), execution)

	if outcome.Kind != jobengine.OutcomeRelinquished || outcome.PublicErrorCode != "mail.rekey.primary_mismatch" ||
		len(persistence.pages) != 0 || len(persistence.replacements) != 0 {
		t.Fatalf("Run() outcome = %#v, pages = %d, replacements = %d", outcome, len(persistence.pages), len(persistence.replacements))
	}
}

func oldSealerKeyID(t *testing.T, key string) string {
	t.Helper()
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: key, MaximumPlaintext: 1})
	if err != nil {
		t.Fatal(err)
	}
	return sealer.PrimaryKeyID()
}
