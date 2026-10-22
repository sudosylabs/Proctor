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
	"github.com/sudosylabs/proctor/server/store"
)

type MailRekeyView struct {
	JobID         model.JobID
	PrimaryKeyID  string
	RetiringKeyID string
	CreatedAt     time.Time
}

type MailPayloadKeyUsageView struct {
	KeyID            string
	ActiveReferences int64
}

type MailKeyStateView struct {
	PrimaryKeyID         string
	RequiredPrimaryKeyID string
	Active               []MailPayloadKeyUsageView
}

type mailRekeyStarter interface {
	StartRekey(context.Context, *store.MailRekeyStart) (*store.MailRekeyOperation, error)
}

type mailKeyInspector interface {
	InspectKeyState(context.Context) (*store.MailKeyState, error)
}

func (s *mailService) GetMailKeyState(ctx context.Context, invocation invocation.Invocation) (MailKeyStateView, error) {
	if s == nil {
		return MailKeyStateView{}, apperror.New("mail.unavailable")
	}
	if err := requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
		return MailKeyStateView{}, err
	}
	if _, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionMailRekey); err != nil {
		return MailKeyStateView{}, err
	}
	if s.keyState == nil || s.deps.Sealer == nil {
		return MailKeyStateView{}, apperror.New("mail.unavailable")
	}
	state, err := s.keyState.InspectKeyState(ctx)
	if err != nil || state == nil {
		return MailKeyStateView{}, apperror.New("mail.unavailable").Wrap(err)
	}
	view := MailKeyStateView{PrimaryKeyID: s.deps.Sealer.PrimaryKeyID(), RequiredPrimaryKeyID: state.RequiredPrimaryKeyID,
		Active: make([]MailPayloadKeyUsageView, 0, len(state.Active))}
	for _, usage := range state.Active {
		if usage.KeyID == "" || usage.ActiveReferences <= 0 {
			return MailKeyStateView{}, apperror.New("mail.unavailable").Wrap(errors.New("mail key state is invalid"))
		}
		view.Active = append(view.Active, MailPayloadKeyUsageView{KeyID: usage.KeyID, ActiveReferences: usage.ActiveReferences})
	}
	return view, nil
}

func (s *mailService) StartMailRekey(ctx context.Context, invocation invocation.Invocation, retiringKeyID string) (MailRekeyView, error) {
	if s == nil {
		return MailRekeyView{}, apperror.New("mail.unavailable")
	}
	if err := requireStrongRecentSession(invocation.Principal(), s.deps.Now(), s.deps.RecentAuthenticationTTL); err != nil {
		return MailRekeyView{}, err
	}
	resource, err := s.deps.Authorization.AuthorizeInstitution(ctx, invocation, model.ActionMailRekey)
	if err != nil {
		return MailRekeyView{}, err
	}
	if s.rekey == nil || s.rekeyAudit == nil || s.deps.Sealer == nil || s.wake == nil {
		return MailRekeyView{}, apperror.New("mail.unavailable")
	}
	primaryKeyID := s.deps.Sealer.PrimaryKeyID()
	if retiringKeyID == "" || retiringKeyID == primaryKeyID || !s.deps.Sealer.HasKey(retiringKeyID) {
		return MailRekeyView{}, apperror.New("mail.rekey.invalid")
	}
	command, err := json.Marshal(jobhandler.MailRekeyCommand{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID})
	if err != nil {
		return MailRekeyView{}, apperror.New("mail.unavailable").Wrap(err)
	}
	now := model.TimeUTC(s.deps.Now())
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, command,
		"mail-rekey:"+primaryKeyID+":"+retiringKeyID, now, now, 10)
	if err != nil {
		return MailRekeyView{}, apperror.New("mail.unavailable").Wrap(err)
	}
	operation, err := runAuditedMutation(ctx, s.rekeyAudit, mutationAttempt{
		Invocation: invocation, Action: model.ActionMailRekey, Resource: resource,
		ScopeType: model.RoleScopeInstitution, ScopeID: resource.ID, Operation: "start_rekey",
		Value: map[string]any{"job_id": job.ID.String(), "primary_key_id": primaryKeyID, "retiring_key_id": retiringKeyID},
	}, s.deps.Now, func(ctx context.Context, reference mutationAttemptReference) (*store.MailRekeyOperation, error) {
		return s.rekey.StartRekey(ctx, &store.MailRekeyStart{PrimaryKeyID: primaryKeyID,
			RetiringKeyID: retiringKeyID, Job: job, AuditEventID: reference.ID, AuditAt: reference.MutationAt})
	}, mailRekeyStoreErrors.Translate)
	if err != nil {
		return MailRekeyView{}, err
	}
	if operation == nil || !operation.JobID.IsValid() || operation.PrimaryKeyID != primaryKeyID ||
		operation.RetiringKeyID != retiringKeyID || operation.CreatedAt.IsZero() {
		return MailRekeyView{}, apperror.New("mail.unavailable").Wrap(errors.New("mail rekey persistence returned an invalid operation"))
	}
	s.wake()
	return MailRekeyView{JobID: operation.JobID, PrimaryKeyID: operation.PrimaryKeyID,
		RetiringKeyID: operation.RetiringKeyID, CreatedAt: operation.CreatedAt}, nil
}

var mailRekeyStoreErrors = apperror.StoreTable{
	Conflict:    "mail.rekey.conflict",
	Unavailable: "mail.unavailable",
}
