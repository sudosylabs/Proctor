// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

type mfaKeyStoreFake struct {
	state  *store.MFAKeyState
	input  *store.MFARekeyStart
	err    error
	job    *model.Job
	events *[]string
}

func (f *mfaKeyStoreFake) InspectKeyState(context.Context) (*store.MFAKeyState, error) {
	return f.state, nil
}

func (f *mfaKeyStoreFake) StartRekey(_ context.Context, input *store.MFARekeyStart) (*store.MFARekeyOperation, error) {
	*f.events = append(*f.events, "store")
	f.input = input
	if f.err != nil {
		return nil, f.err
	}
	return &store.MFARekeyOperation{JobID: input.Job.ID, PrimaryKeyID: input.PrimaryKeyID, RetiringKeyID: input.RetiringKeyID, CreatedAt: input.Job.CreatedAt}, nil
}

func (f *mfaKeyStoreFake) Get(_ context.Context, id model.JobID) (*model.Job, error) {
	if f.job == nil || f.job.ID != id {
		return nil, store.NewErrNotFound("job", id.String())
	}
	return f.job, nil
}

type mfaKeyServiceFixture struct {
	service    *mfaKeyService
	store      *mfaKeyStoreFake
	authorizer *mailAuthorizerFake
	auditor    *mailRekeyAuditFake
	events     []string
	woke       int
	sealer     *secretseal.Sealer
	retiringID string
	principal  model.Principal
}

func newMFAKeyServiceFixture(t *testing.T) *mfaKeyServiceFixture {
	t.Helper()
	now := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	oldKey := base64.StdEncoding.EncodeToString([]byte("old-mfa-rekey-key-material-00001"))
	newKey := base64.StdEncoding.EncodeToString([]byte("new-mfa-rekey-key-material-00001"))
	sealer, err := secretseal.New(secretseal.Settings{EncryptionKey: newKey, DecryptionKeys: []string{oldKey}, MaximumPlaintext: 128})
	require.NoError(t, err)
	fixture := &mfaKeyServiceFixture{authorizer: &mailAuthorizerFake{}, sealer: sealer, retiringID: oldSealerKeyID(t, oldKey)}
	fixture.store = &mfaKeyStoreFake{events: &fixture.events}
	fixture.auditor = &mailRekeyAuditFake{beginID: model.NewID(), events: &fixture.events}
	fixture.service, err = newMFAKeyService(mfaKeyServiceDependencies{Keys: fixture.store, Jobs: fixture.store,
		Authorization: fixture.authorizer, Audit: fixture.auditor, Sealer: sealer, RecentAuthenticationTTL: 15 * time.Minute,
		Now: func() time.Time { return now }, Wake: func() { fixture.woke++ }})
	require.NoError(t, err)
	fixture.principal = mailTestPrincipal(now)
	fixture.principal.AuthenticationStrength = model.AuthenticationMultiFactor
	fixture.principal.MFACompletedAt = model.OptionalTimeFrom(now.Add(-time.Minute))
	return fixture
}

func (f *mfaKeyServiceFixture) invocation() invocation.Invocation {
	return invocation.New(f.principal, model.RequestMetadata{})
}

func TestStartMFARekeyAuditsThenFencesAndWakesTheEngine(t *testing.T) {
	t.Parallel()
	fixture := newMFAKeyServiceFixture(t)

	view, err := fixture.service.StartMFARekey(context.Background(), fixture.invocation(), StartMFARekeyCommand{RetiringKeyID: fixture.retiringID})

	require.NoError(t, err)
	// Contract: the audit attempt precedes the fenced start.
	require.Equal(t, []string{"audit", "store"}, fixture.events)
	require.Equal(t, []model.Action{model.ActionMFARekey}, fixture.authorizer.actions)
	input := fixture.store.input
	require.Equal(t, model.JobTypeMFARekey, input.Job.Type)
	require.Equal(t, fixture.sealer.PrimaryKeyID(), input.PrimaryKeyID)
	require.Equal(t, fixture.retiringID, input.RetiringKeyID)
	require.Equal(t, model.AuditEventID(fixture.auditor.beginID), input.AuditEventID)
	command, err := jobhandler.DecodeMFARekeyCommand(input.Job.Command)
	require.NoError(t, err)
	require.Equal(t, jobhandler.MFARekeyCommand{PrimaryKeyID: fixture.sealer.PrimaryKeyID(), RetiringKeyID: fixture.retiringID}, command)
	require.Equal(t, MFARekeyView{JobID: input.Job.ID, PrimaryKeyID: input.PrimaryKeyID, RetiringKeyID: input.RetiringKeyID, CreatedAt: input.Job.CreatedAt}, view)
	require.Equal(t, 1, fixture.woke)
}

func TestStartMFARekeyRefusesAKeyOutsideTheRingOrThePrimary(t *testing.T) {
	t.Parallel()
	for name, retiring := range map[string]string{"empty": "", "unknown": "ffffffffffffffffffffffffffffffff", "primary": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := newMFAKeyServiceFixture(t)
			if name == "primary" {
				retiring = fixture.sealer.PrimaryKeyID()
			}
			_, err := fixture.service.StartMFARekey(context.Background(), fixture.invocation(), StartMFARekeyCommand{RetiringKeyID: retiring})
			require.True(t, apperror.Is(err, "authentication.mfa.rekey.invalid"), "error = %v", err)
			require.Empty(t, fixture.events, "nothing is audited or written")
		})
	}
}

func TestStartMFARekeyPresentsAnUnprovenOrConcurrentRekeyAsConflict(t *testing.T) {
	t.Parallel()
	fixture := newMFAKeyServiceFixture(t)
	fixture.store.err = store.NewErrConflict("mfa_rekey", "active_operation", nil)

	_, err := fixture.service.StartMFARekey(context.Background(), fixture.invocation(), StartMFARekeyCommand{RetiringKeyID: fixture.retiringID})

	require.True(t, apperror.Is(err, "authentication.mfa.rekey.conflict"), "error = %v", err)
	require.Equal(t, "authentication.mfa.rekey.conflict", fixture.auditor.failCode)
	require.Zero(t, fixture.woke)
}

func TestMFARekeyRequiresAStrongRecentSession(t *testing.T) {
	t.Parallel()
	fixture := newMFAKeyServiceFixture(t)
	fixture.principal.AuthenticationStrength = model.AuthenticationSingleFactor
	fixture.principal.MFACompletedAt = model.OptionalTime{}

	_, err := fixture.service.StartMFARekey(context.Background(), fixture.invocation(), StartMFARekeyCommand{RetiringKeyID: fixture.retiringID})

	require.True(t, apperror.Is(err, "authentication.strong_required"), "error = %v", err)
	require.Empty(t, fixture.authorizer.actions)
}

func TestMFAKeyStateMarksKeysTheRingCannotOpen(t *testing.T) {
	t.Parallel()
	fixture := newMFAKeyServiceFixture(t)
	fixture.store.state = &store.MFAKeyState{RequiredPrimaryKeyID: fixture.sealer.PrimaryKeyID(),
		Active: []store.MFASecretKeyUsage{{KeyID: fixture.retiringID, Credentials: 2}, {KeyID: "ffffffffffffffffffffffffffffffff", Credentials: 1}}}

	view, err := fixture.service.GetMFAKeyState(context.Background(), fixture.invocation())

	require.NoError(t, err)
	require.Equal(t, MFAKeyStateView{PrimaryKeyID: fixture.sealer.PrimaryKeyID(), RequiredPrimaryKeyID: fixture.sealer.PrimaryKeyID(),
		Active: []MFASecretKeyUsageView{{KeyID: fixture.retiringID, Credentials: 2, Configured: true},
			{KeyID: "ffffffffffffffffffffffffffffffff", Credentials: 1}}}, view)
}

func TestMFARekeyStatusHidesOtherJobTypesAndProjectsTheProof(t *testing.T) {
	t.Parallel()
	fixture := newMFAKeyServiceFixture(t)
	at := time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)
	command, err := json.Marshal(jobhandler.MFARekeyCommand{PrimaryKeyID: fixture.sealer.PrimaryKeyID(), RetiringKeyID: fixture.retiringID})
	require.NoError(t, err)
	other, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, command, "mail-rekey:other", at, at, 10)
	require.NoError(t, err)
	fixture.store.job = other
	_, err = fixture.service.GetMFARekeyStatus(context.Background(), fixture.invocation(), GetMFARekeyStatusQuery{JobID: other.ID})
	require.True(t, apperror.Is(err, "resource.not_found"), "a mail rekey is not an MFA rekey: error = %v", err)

	succeeded, err := model.NewJob(model.NewJobID(), model.JobTypeMFARekey, command, "mfa-rekey:test", at, at, 10)
	require.NoError(t, err)
	succeeded.Status, succeeded.CompletedAt = model.JobStatusSucceeded, model.OptionalTimeFrom(at)
	succeeded.Result = json.RawMessage(`{"primary_key_id":"` + fixture.sealer.PrimaryKeyID() + `","retiring_key_id":"` + fixture.retiringID +
		`","processed":3,"resealed":3,"non_primary_references":0,"retiring_references":0,"retirement_safe":true}`)
	fixture.store.job = succeeded
	view, err := fixture.service.GetMFARekeyStatus(context.Background(), fixture.invocation(), GetMFARekeyStatusQuery{JobID: succeeded.ID})
	require.NoError(t, err)
	require.Equal(t, &MFARekeyProofView{RetirementSafe: true}, view.Proof)
	require.Equal(t, int64(3), view.Resealed)
}
