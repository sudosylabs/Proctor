// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	appjobs "github.com/sudosylabs/proctor/server/app/jobs"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type MailRekeyProgressView struct {
	Current int64
	Total   int64
	Stage   string
}

type MailRekeyProofView struct {
	NonPrimaryReferences int64
	RetiringReferences   int64
	RetirementSafe       bool
}

// MailRekeyStatusView is the safe, typed operator projection for one rekey
// Job. It deliberately excludes the raw command, checkpoint, result, payload
// identities, ciphertext, recipients, and key material.
type MailRekeyStatusView struct {
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
	Reencrypted     int64
	Progress        *MailRekeyProgressView
	Proof           *MailRekeyProofView
}

type mailRekeyJobReader interface {
	Get(context.Context, model.JobID) (*model.Job, error)
}

func (a *App) GetMailRekeyStatus(ctx context.Context, invocation Invocation, jobID model.JobID) (MailRekeyStatusView, error) {
	if a == nil || a.mail == nil {
		return MailRekeyStatusView{}, NewError("mail.unavailable")
	}
	return a.mail.RekeyStatus(ctx, invocation, jobID)
}

func (s *mailService) RekeyStatus(ctx context.Context, invocation Invocation, jobID model.JobID) (MailRekeyStatusView, error) {
	if err := requireStrongRecentSession(invocation.Principal(), s.now(), s.recentAuthenticationTTL); err != nil {
		return MailRekeyStatusView{}, err
	}
	if _, err := s.authorization.Authorize(ctx, invocation, model.ActionMailRekey); err != nil {
		return MailRekeyStatusView{}, err
	}
	if s.rekeyJobs == nil || !jobID.IsValid() {
		return MailRekeyStatusView{}, NewError("mail.unavailable")
	}
	job, err := s.rekeyJobs.Get(ctx, jobID)
	if err != nil {
		if store.IsNotFound(err) {
			return MailRekeyStatusView{}, NewError("resource.not_found")
		}
		return MailRekeyStatusView{}, NewError("mail.unavailable").Wrap(err)
	}
	// Job identities are shared across domains. A caller with mail.rekey must
	// not learn whether a valid identifier belongs to some unrelated Job type.
	if job == nil {
		return MailRekeyStatusView{}, NewError("mail.unavailable").Wrap(errors.New("mail rekey persistence returned no Job"))
	}
	if job.Type != model.JobTypeMailRekey {
		return MailRekeyStatusView{}, NewError("resource.not_found")
	}
	view, err := projectMailRekeyStatus(job)
	if err != nil {
		return MailRekeyStatusView{}, NewError("mail.unavailable").Wrap(err)
	}
	return view, nil
}

func projectMailRekeyStatus(job *model.Job) (MailRekeyStatusView, error) {
	if job == nil || job.Validate() != nil || job.Type != model.JobTypeMailRekey || job.CommandVersion != 1 {
		return MailRekeyStatusView{}, errors.New("mail rekey Job is invalid")
	}
	command, commandErr := appjobs.DecodeMailRekeyCommand(job.Command)
	if commandErr != nil || !validMailRekeyKeyID(command.PrimaryKeyID) ||
		!validMailRekeyKeyID(command.RetiringKeyID) || command.PrimaryKeyID == command.RetiringKeyID {
		return MailRekeyStatusView{}, errors.New("mail rekey command is invalid")
	}
	view := MailRekeyStatusView{JobID: job.ID, Status: job.Status, PrimaryKeyID: command.PrimaryKeyID,
		RetiringKeyID: command.RetiringKeyID, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt,
		CompletedAt: job.CompletedAt, PublicErrorCode: job.PublicErrorCode,
		AttemptCount: job.AttemptCount, MaximumAttempts: job.MaximumAttempts}
	if len(job.Checkpoint) > 0 {
		checkpoint, err := appjobs.DecodeMailRekeyCheckpoint(job.CheckpointVersion, job.Checkpoint)
		if err != nil || job.Progress == nil || job.Progress.Current != checkpoint.Processed ||
			job.Progress.Total < checkpoint.Processed || job.Progress.Stage != "reencrypting" {
			return MailRekeyStatusView{}, errors.New("mail rekey checkpoint projection is invalid")
		}
		view.Processed, view.Reencrypted = checkpoint.Processed, checkpoint.Reencrypted
		view.Progress = &MailRekeyProgressView{Current: job.Progress.Current, Total: job.Progress.Total, Stage: job.Progress.Stage}
	} else if job.Progress != nil {
		return MailRekeyStatusView{}, errors.New("mail rekey progress has no checkpoint")
	}
	if len(job.Result) > 0 {
		if job.Status != model.JobStatusSucceeded || job.ResultVersion != 1 {
			return MailRekeyStatusView{}, errors.New("mail rekey result state is invalid")
		}
		result, resultErr := appjobs.DecodeMailRekeyResult(job.Result)
		if resultErr != nil ||
			result.PrimaryKeyID != command.PrimaryKeyID || result.RetiringKeyID != command.RetiringKeyID ||
			result.Processed < view.Processed || result.Reencrypted < view.Reencrypted || result.Reencrypted > result.Processed ||
			result.NonPrimaryReferences < 0 || result.RetiringReferences < 0 ||
			result.RetiringReferences > result.NonPrimaryReferences || !result.RetirementSafe ||
			result.NonPrimaryReferences != 0 || result.RetiringReferences != 0 {
			return MailRekeyStatusView{}, errors.New("mail rekey retirement proof is invalid")
		}
		view.Processed, view.Reencrypted = result.Processed, result.Reencrypted
		view.Proof = &MailRekeyProofView{NonPrimaryReferences: result.NonPrimaryReferences,
			RetiringReferences: result.RetiringReferences, RetirementSafe: result.RetirementSafe}
	} else if job.Status == model.JobStatusSucceeded {
		return MailRekeyStatusView{}, errors.New("successful mail rekey has no proof")
	}
	return view, nil
}

func validMailRekeyKeyID(value string) bool {
	if len(value) != 32 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
