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

func (a *App) StartMailRekey(ctx context.Context, invocation Invocation, retiringKeyID string) (MailRekeyView, error) {
	if a == nil || a.mail == nil {
		return MailRekeyView{}, NewError("mail.unavailable")
	}
	return a.mail.StartRekey(ctx, invocation, retiringKeyID)
}

func (a *App) GetMailKeyState(ctx context.Context, invocation Invocation) (MailKeyStateView, error) {
	if a == nil || a.mail == nil {
		return MailKeyStateView{}, NewError("mail.unavailable")
	}
	return a.mail.KeyState(ctx, invocation)
}

func (s *mailService) KeyState(ctx context.Context, invocation Invocation) (MailKeyStateView, error) {
	if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
		return MailKeyStateView{}, err
	}
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionMailRekey); err != nil {
		return MailKeyStateView{}, err
	}
	if s.keyState == nil || s.sealer == nil {
		return MailKeyStateView{}, NewError("mail.unavailable")
	}
	state, err := s.keyState.InspectKeyState(ctx)
	if err != nil || state == nil {
		return MailKeyStateView{}, NewError("mail.unavailable").Wrap(err)
	}
	view := MailKeyStateView{PrimaryKeyID: s.sealer.PrimaryKeyID(), RequiredPrimaryKeyID: state.RequiredPrimaryKeyID,
		Active: make([]MailPayloadKeyUsageView, 0, len(state.Active))}
	for _, usage := range state.Active {
		if usage.KeyID == "" || usage.ActiveReferences <= 0 {
			return MailKeyStateView{}, NewError("mail.unavailable").Wrap(errors.New("mail key state is invalid"))
		}
		view.Active = append(view.Active, MailPayloadKeyUsageView{KeyID: usage.KeyID, ActiveReferences: usage.ActiveReferences})
	}
	return view, nil
}

func (s *mailService) StartRekey(ctx context.Context, invocation Invocation, retiringKeyID string) (MailRekeyView, error) {
	if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
		return MailRekeyView{}, err
	}
	resource, err := s.authorization.Authorize(ctx, invocation, model.ActionMailRekey)
	if err != nil {
		return MailRekeyView{}, err
	}
	if s.rekey == nil || s.rekeyAudit == nil || s.sealer == nil || s.wake == nil {
		return MailRekeyView{}, NewError("mail.unavailable")
	}
	primaryKeyID := s.sealer.PrimaryKeyID()
	if retiringKeyID == "" || retiringKeyID == primaryKeyID || !s.sealer.HasKey(retiringKeyID) {
		return MailRekeyView{}, NewError("mail.rekey.invalid")
	}
	command, err := json.Marshal(MailRekeyCommandV1{PrimaryKeyID: primaryKeyID, RetiringKeyID: retiringKeyID})
	if err != nil {
		return MailRekeyView{}, NewError("mail.unavailable").Wrap(err)
	}
	now := model.TimeUTC(s.now())
	job, err := model.NewJob(model.NewJobID(), model.JobTypeMailRekey, 1, command,
		"mail-rekey:"+primaryKeyID+":"+retiringKeyID, now, now, 10)
	if err != nil {
		return MailRekeyView{}, NewError("mail.unavailable").Wrap(err)
	}
	operation, err := runAuditedMutation(ctx, s.rekeyAudit, mutationAttempt{
		Invocation: invocation, Action: model.ActionMailRekey, Resource: resource,
		ScopeType: model.RoleScopeInstitution, ScopeID: resource.ID, Operation: "start_rekey",
		Value: map[string]any{"job_id": job.ID.String(), "primary_key_id": primaryKeyID, "retiring_key_id": retiringKeyID},
	}, s.now, func(ctx context.Context, reference mutationAttemptReference) (*store.MailRekeyOperation, error) {
		return s.rekey.StartRekey(ctx, &store.MailRekeyStart{PrimaryKeyID: primaryKeyID,
			RetiringKeyID: retiringKeyID, Job: job, AuditEventID: reference.ID, AuditAt: reference.MutationAtMillis})
	}, mailRekeyStartError)
	if err != nil {
		return MailRekeyView{}, err
	}
	if operation == nil || !operation.JobID.IsValid() || operation.PrimaryKeyID != primaryKeyID ||
		operation.RetiringKeyID != retiringKeyID || operation.CreatedAt.IsZero() {
		return MailRekeyView{}, NewError("mail.unavailable").Wrap(errors.New("mail rekey persistence returned an invalid operation"))
	}
	s.wake()
	return MailRekeyView{JobID: operation.JobID, PrimaryKeyID: operation.PrimaryKeyID,
		RetiringKeyID: operation.RetiringKeyID, CreatedAt: operation.CreatedAt}, nil
}

func mailRekeyStartError(err error) error {
	if store.IsConflict(err) {
		return NewError("mail.rekey.conflict").Wrap(err)
	}
	return NewError("mail.unavailable").Wrap(err)
}
