// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/app/jobhandler"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/secretseal"
	"github.com/sudosylabs/proctor/server/store"
)

// MFASecretKeyUsageView counts the live TOTP secrets one key seals and says
// whether this node's configured ring holds that key.
type MFASecretKeyUsageView struct {
	KeyID       string
	Credentials int64
	Configured  bool
}

// MFAKeyStateView is the operator projection of the MFA key ring: this node's
// configured primary, the durable fence every writer must honor, the rekey
// that installed it, and the live secrets per sealing key.
type MFAKeyStateView struct {
	PrimaryKeyID         string
	RequiredPrimaryKeyID string
	ActiveRekeyJobID     model.JobID
	Active               []MFASecretKeyUsageView
}

// StartMFARekeyCommand re-seals every live TOTP secret under the configured
// primary so that RetiringKeyID can leave the ring.
type StartMFARekeyCommand struct {
	RetiringKeyID string
}

// MFARekeyView is the queued rekey StartMFARekey returns.
type MFARekeyView struct {
	JobID         model.JobID
	PrimaryKeyID  string
	RetiringKeyID string
	CreatedAt     time.Time
}

// GetMFARekeyStatusQuery names one MFA rekey Job.
type GetMFARekeyStatusQuery struct {
	JobID model.JobID
}

// MFARekeyProgressView is the safe progress of a running rekey.
type MFARekeyProgressView struct {
	Current int64
	Total   int64
	Stage   string
}

// MFARekeyProofView is the retirement proof of a completed rekey.
type MFARekeyProofView struct {
	NonPrimaryReferences int64
	RetiringReferences   int64
	RetirementSafe       bool
}

// MFARekeyStatusView is the safe, typed operator projection of one MFA rekey
// Job. It excludes the raw command, checkpoint, credential identities,
// ciphertext, and key material.
type MFARekeyStatusView struct {
	JobID           model.JobID
	Status          model.JobStatus
	PrimaryKeyID    string
	RetiringKeyID   string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     model.OptionalTime
	PublicErrorCode string
	AttemptCount    int
	MaximumAttempts int
	Processed       int64
	Resealed        int64
	Progress        *MFARekeyProgressView
	Proof           *MFARekeyProofView
}

type mfaKeyStore interface {
	InspectKeyState(context.Context) (*store.MFAKeyState, error)
	StartRekey(context.Context, *store.MFARekeyStart) (*store.MFARekeyOperation, error)
}

type mfaRekeyJobReader interface {
	Get(context.Context, model.JobID) (*model.Job, error)
}

// mfaKeyServiceDependencies are the collaborators of mfaKeyService;
// newMFAKeyService validates them once and mfaKeyService keeps them in deps.
type mfaKeyServiceDependencies struct {
	Keys                    mfaKeyStore
	Jobs                    mfaRekeyJobReader
	Authorization           institutionAuthorizer
	Audit                   mutationAuditor
	Sealer                  *secretseal.Sealer
	RecentAuthenticationTTL time.Duration
	Now                     func() time.Time
	Wake                    func()
}

// mfaKeyService owns the operator view and rotation of the MFA key ring.
type mfaKeyService struct {
	deps mfaKeyServiceDependencies
}

func newMFAKeyService(deps mfaKeyServiceDependencies) (*mfaKeyService, error) {
	if deps.Keys == nil || deps.Jobs == nil || deps.Authorization == nil || deps.Audit == nil || deps.Now == nil || deps.Wake == nil {
		return nil, errors.New("MFA key service dependencies are incomplete")
	}
	return &mfaKeyService{deps: deps}, nil
}

func (s *mfaKeyService) authorize(ctx context.Context, invocation invocation.Invocation) (model.Resource, error) {
	if s == nil {
		return model.Resource{}, apperror.New("authentication.mfa.unavailable")
	}
	if err := requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
		return model.Resource{}, err
	}
	return s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionMFARekey)
}

// GetMFAKeyState reports the MFA key ring to an operator with mfa.rekey.
func (s *mfaKeyService) GetMFAKeyState(ctx context.Context, invocation invocation.Invocation) (MFAKeyStateView, error) {
	if _, err := s.authorize(ctx, invocation); err != nil {
		return MFAKeyStateView{}, err
	}
	if s.deps.Sealer == nil {
		return MFAKeyStateView{}, apperror.New("authentication.mfa.unavailable")
	}
	state, err := s.deps.Keys.InspectKeyState(ctx)
	if err != nil || state == nil {
		return MFAKeyStateView{}, apperror.New("authentication.mfa.unavailable").Wrap(err)
	}
	view := MFAKeyStateView{PrimaryKeyID: s.deps.Sealer.PrimaryKeyID(), RequiredPrimaryKeyID: state.RequiredPrimaryKeyID,
		ActiveRekeyJobID: state.ActiveRekeyJobID, Active: make([]MFASecretKeyUsageView, 0, len(state.Active))}
	for _, usage := range state.Active {
		if usage.KeyID == "" || usage.Credentials <= 0 {
			return MFAKeyStateView{}, apperror.New("authentication.mfa.unavailable").Wrap(errors.New("MFA key state is invalid"))
		}
		view.Active = append(view.Active, MFASecretKeyUsageView{KeyID: usage.KeyID, Credentials: usage.Credentials,
			Configured: s.deps.Sealer.HasKey(usage.KeyID)})
	}
	return view, nil
}

// StartMFARekey fences the MFA key ring on this node's configured primary and
// queues the Job that re-seals every live TOTP secret under it. The retiring
// key must be another key of the configured ring.
func (s *mfaKeyService) StartMFARekey(ctx context.Context, invocation invocation.Invocation, command StartMFARekeyCommand) (MFARekeyView, error) {
	resource, err := s.authorize(ctx, invocation)
	if err != nil {
		return MFARekeyView{}, err
	}
	if s.deps.Sealer == nil {
		return MFARekeyView{}, apperror.New("authentication.mfa.unavailable")
	}
	primaryKeyID := s.deps.Sealer.PrimaryKeyID()
	if command.RetiringKeyID == "" || command.RetiringKeyID == primaryKeyID || !s.deps.Sealer.HasKey(command.RetiringKeyID) {
		return MFARekeyView{}, apperror.Invalid("authentication.mfa.rekey.invalid", "retiring_key_id")
	}
	encoded, err := json.Marshal(jobhandler.MFARekeyCommand{PrimaryKeyID: primaryKeyID, RetiringKeyID: command.RetiringKeyID})
	if err != nil {
		return MFARekeyView{}, apperror.New("authentication.mfa.unavailable").Wrap(err)
	}
	now := model.TimeUTC(s.deps.Now())
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMFARekey, encoded,
		"mfa-rekey:"+primaryKeyID+":"+command.RetiringKeyID, now, now, 10)
	if err != nil {
		return MFARekeyView{}, apperror.New("authentication.mfa.unavailable").Wrap(err)
	}
	operation, err := runAuditedMutation(ctx, s.deps.Audit, mutationAttempt{
		Invocation: invocation, Action: model.ActionMFARekey, Resource: resource,
		ScopeType: model.RoleScopeInstitution, ScopeID: resource.ID, Operation: "start_rekey",
		Value: map[string]any{"job_id": job.ID.String(), "primary_key_id": primaryKeyID, "retiring_key_id": command.RetiringKeyID},
	}, s.deps.Now, func(ctx context.Context, reference mutationAttemptReference) (*store.MFARekeyOperation, error) {
		return s.deps.Keys.StartRekey(ctx, &store.MFARekeyStart{PrimaryKeyID: primaryKeyID, RetiringKeyID: command.RetiringKeyID,
			Job: job, AuditEventID: reference.ID, AuditAt: reference.MutationAt})
	}, mfaRekeyStoreErrors.Translate)
	if err != nil {
		return MFARekeyView{}, err
	}
	if operation == nil || operation.JobID != job.ID || operation.PrimaryKeyID != primaryKeyID ||
		operation.RetiringKeyID != command.RetiringKeyID || operation.CreatedAt.IsZero() {
		return MFARekeyView{}, apperror.New("authentication.mfa.unavailable").Wrap(errors.New("MFA rekey persistence returned an invalid operation"))
	}
	s.deps.Wake()
	return MFARekeyView{JobID: operation.JobID, PrimaryKeyID: operation.PrimaryKeyID,
		RetiringKeyID: operation.RetiringKeyID, CreatedAt: operation.CreatedAt}, nil
}

// GetMFARekeyStatus reports the safe progress and retirement proof of one MFA
// rekey Job. Another Job type is not found, so the caller learns nothing about
// unrelated Jobs.
func (s *mfaKeyService) GetMFARekeyStatus(ctx context.Context, invocation invocation.Invocation, query GetMFARekeyStatusQuery) (MFARekeyStatusView, error) {
	if _, err := s.authorize(ctx, invocation); err != nil {
		return MFARekeyStatusView{}, err
	}
	if !query.JobID.IsValid() {
		return MFARekeyStatusView{}, apperror.Invalid("request.invalid", "job_id")
	}
	job, err := s.deps.Jobs.Get(ctx, query.JobID)
	if err != nil {
		if store.IsNotFound(err) {
			return MFARekeyStatusView{}, apperror.New("resource.not_found")
		}
		return MFARekeyStatusView{}, apperror.New("authentication.mfa.unavailable").Wrap(err)
	}
	if job == nil {
		return MFARekeyStatusView{}, apperror.New("authentication.mfa.unavailable").Wrap(errors.New("MFA rekey persistence returned no Job"))
	}
	if job.Type != model.JobTypeMFARekey {
		return MFARekeyStatusView{}, apperror.New("resource.not_found")
	}
	view, err := projectMFARekeyStatus(job)
	if err != nil {
		return MFARekeyStatusView{}, apperror.New("authentication.mfa.unavailable").Wrap(err)
	}
	return view, nil
}

func projectMFARekeyStatus(job *model.Job) (MFARekeyStatusView, error) {
	if job.Validate() != nil {
		return MFARekeyStatusView{}, errors.New("MFA rekey Job is invalid")
	}
	command, err := jobhandler.DecodeMFARekeyCommand(job.Command)
	if err != nil || !validMailRekeyKeyID(command.PrimaryKeyID) || !validMailRekeyKeyID(command.RetiringKeyID) ||
		command.PrimaryKeyID == command.RetiringKeyID {
		return MFARekeyStatusView{}, errors.New("MFA rekey command is invalid")
	}
	view := MFARekeyStatusView{JobID: job.ID, Status: job.Status, PrimaryKeyID: command.PrimaryKeyID,
		RetiringKeyID: command.RetiringKeyID, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
		CompletedAt: job.CompletedAt, PublicErrorCode: job.PublicErrorCode,
		AttemptCount: job.AttemptCount, MaximumAttempts: job.MaximumAttempts}
	if len(job.Checkpoint) > 0 {
		checkpoint, checkpointErr := jobhandler.DecodeMFARekeyCheckpoint(job.Checkpoint)
		if checkpointErr != nil || job.Progress == nil || job.Progress.Current != checkpoint.Processed ||
			job.Progress.Total < checkpoint.Processed || job.Progress.Stage != "resealing" {
			return MFARekeyStatusView{}, errors.New("MFA rekey checkpoint projection is invalid")
		}
		view.Processed, view.Resealed = checkpoint.Processed, checkpoint.Resealed
		view.Progress = &MFARekeyProgressView{Current: job.Progress.Current, Total: job.Progress.Total, Stage: job.Progress.Stage}
	} else if job.Progress != nil {
		return MFARekeyStatusView{}, errors.New("MFA rekey progress has no checkpoint")
	}
	if len(job.Result) == 0 {
		if job.Status == model.JobStatusSucceeded {
			return MFARekeyStatusView{}, errors.New("successful MFA rekey has no proof")
		}
		return view, nil
	}
	result, err := jobhandler.DecodeMFARekeyResult(job.Result)
	if err != nil || job.Status != model.JobStatusSucceeded ||
		result.PrimaryKeyID != command.PrimaryKeyID || result.RetiringKeyID != command.RetiringKeyID ||
		result.Processed < view.Processed || result.Resealed < view.Resealed || result.Resealed > result.Processed ||
		result.NonPrimaryReferences != 0 || result.RetiringReferences != 0 || !result.RetirementSafe {
		return MFARekeyStatusView{}, errors.New("MFA rekey retirement proof is invalid")
	}
	view.Processed, view.Resealed = result.Processed, result.Resealed
	view.Proof = &MFARekeyProofView{NonPrimaryReferences: result.NonPrimaryReferences,
		RetiringReferences: result.RetiringReferences, RetirementSafe: result.RetirementSafe}
	return view, nil
}

var mfaRekeyStoreErrors = apperror.StoreTable{
	Conflict:    "authentication.mfa.rekey.conflict",
	Unavailable: "authentication.mfa.unavailable",
}

// mfaKeyAuthorization resolves the Institution resource for MFA key-ring
// authorization.
type mfaKeyAuthorization struct {
	authorization *accessControlService
	institutions  store.InstitutionStore
}

func (a mfaKeyAuthorization) AuthorizeInstitution(ctx context.Context, invocation invocation.Invocation, action model.Action) (model.Resource, error) {
	institution, err := a.institutions.GetSingleton(ctx)
	if err != nil || institution == nil || !institution.ID.IsValid() {
		return model.Resource{}, apperror.New("authentication.mfa.unavailable").Wrap(err)
	}
	resource := model.Resource{Type: model.ResourceInstitution, ID: institution.ID.String()}
	if err = a.authorization.Authorize(ctx, invocation, action, resource); err != nil {
		return model.Resource{}, err
	}
	return resource, nil
}
