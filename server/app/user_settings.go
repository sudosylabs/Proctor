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

	apprealtime "github.com/sudosylabs/proctor/server/app/realtime"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// UserSettingsView is the exact current self-owned settings source plus the
// metadata a client needs for compatibility and optimistic concurrency.
type UserSettingsView struct {
	Source        string
	FormatVersion int
	Revision      model.UserSettingsRevision
	Writable      bool
	UpdatedAt     time.Time
}

type userSettingsStore interface {
	Get(context.Context, model.UserID) (*model.UserSettingsDocument, error)
	Replace(context.Context, *store.UserSettingsReplacement, *store.CommandIdempotency) (*store.UserSettingsReplacementResult, error)
}

type UserSettingsReplacementResult struct {
	Revision      model.UserSettingsRevision
	FormatVersion int
	UpdatedAt     time.Time
	Changed       bool
	Replayed      bool
}

type ReplaceOwnUserSettingsCommand struct {
	Source           string
	FormatVersion    int
	ExpectedRevision model.UserSettingsRevision
	IdempotencyKey   string
}

type UserSettingsDiagnostic struct {
	Code   string `json:"code"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

type userSettingsAuditInput struct {
	Invocation        Invocation
	UserID            model.UserID
	PreviousRevision  model.UserSettingsRevision
	ResultingRevision model.UserSettingsRevision
	FormatVersion     int
	SourceBytes       int
}

type userSettingsAuditPreparer interface {
	PrepareReplacement(context.Context, userSettingsAuditInput) (*model.AuditEvent, error)
}

type userSettingsChanged struct {
	UserID        model.UserID
	Revision      model.UserSettingsRevision
	FormatVersion int
	ChangedAt     time.Time
}

type userSettingsEffects interface {
	Changed(context.Context, userSettingsChanged) error
}

type userSettingsEffectFailures interface {
	Report(context.Context, string, error)
}

type userSettingsService struct {
	settings       userSettingsStore
	audit          userSettingsAuditPreparer
	effects        userSettingsEffects
	effectFailures userSettingsEffectFailures
	now            func() time.Time
}

func newUserSettingsService(
	settings userSettingsStore,
	audit userSettingsAuditPreparer,
	effects userSettingsEffects,
	effectFailures userSettingsEffectFailures,
	now func() time.Time,
) (*userSettingsService, error) {
	if settings == nil {
		return nil, errors.New("user settings store is required")
	}
	if audit == nil {
		return nil, errors.New("user settings audit preparer is required")
	}
	if effects == nil {
		return nil, errors.New("user settings effects are required")
	}
	if effectFailures == nil {
		return nil, errors.New("user settings effect failure reporter is required")
	}
	if now == nil {
		return nil, errors.New("user settings clock is required")
	}
	return &userSettingsService{
		settings: settings, audit: audit, effects: effects,
		effectFailures: effectFailures, now: now,
	}, nil
}

func (a *App) ReplaceOwnUserSettings(
	ctx context.Context,
	invocation Invocation,
	command ReplaceOwnUserSettingsCommand,
) (UserSettingsReplacementResult, error) {
	if a == nil || a.userSettings == nil {
		return UserSettingsReplacementResult{}, NewError("user_settings.unavailable")
	}
	return a.userSettings.ReplaceOwn(ctx, invocation, command)
}

func (s *userSettingsService) ReplaceOwn(
	ctx context.Context,
	invocation Invocation,
	command ReplaceOwnUserSettingsCommand,
) (UserSettingsReplacementResult, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return UserSettingsReplacementResult{}, invalidTokenAppError()
	}
	if command.IdempotencyKey == "" {
		return UserSettingsReplacementResult{}, NewError("idempotency.key_required")
	}
	if command.FormatVersion != model.UserSettingsFormatVersion1 {
		return UserSettingsReplacementResult{}, NewError("user_settings.format_unsupported")
	}
	if !command.ExpectedRevision.IsValid() {
		return UserSettingsReplacementResult{}, NewError("request.invalid").WithField("field", "expected_revision")
	}
	diagnostics := validateUserSettingsSource(command.Source)
	if len(diagnostics) != 0 {
		public := make([]UserSettingsDiagnostic, len(diagnostics))
		for index, diagnostic := range diagnostics {
			public[index] = UserSettingsDiagnostic{
				Code: diagnostic.Code, Line: diagnostic.Line, Column: diagnostic.Column,
			}
		}
		encoded, _ := json.Marshal(public)
		return UserSettingsReplacementResult{}, NewError("user_settings.invalid").WithField("diagnostics", string(encoded))
	}
	current, err := s.settings.Get(ctx, principal.UserID)
	if err != nil || current == nil || current.UserID != principal.UserID || current.Validate() != nil {
		if err == nil {
			err = errors.New("invalid persisted settings document")
		}
		return UserSettingsReplacementResult{}, userSettingsUnavailable(err)
	}
	if current.FormatVersion != model.UserSettingsFormatVersion1 {
		return UserSettingsReplacementResult{}, NewError("user_settings.format_unsupported")
	}
	nextRevision := model.NewUserSettingsRevision()
	updatedAt := model.TimeUTC(s.now())
	if !updatedAt.After(current.UpdatedAt) {
		updatedAt = current.UpdatedAt.Add(time.Millisecond)
	}
	idempotency, err := newCommandIdempotency(
		invocation,
		"user_settings.replace",
		command.IdempotencyKey,
		struct {
			Source           string                     `json:"source"`
			FormatVersion    int                        `json:"format_version"`
			ExpectedRevision model.UserSettingsRevision `json:"expected_revision"`
		}{command.Source, command.FormatVersion, command.ExpectedRevision},
	)
	if err != nil {
		return UserSettingsReplacementResult{}, err
	}
	var audit *model.AuditEvent
	if current.Revision == command.ExpectedRevision &&
		(current.Source != command.Source || current.FormatVersion != command.FormatVersion) {
		audit, err = s.audit.PrepareReplacement(ctx, userSettingsAuditInput{
			Invocation: invocation, UserID: principal.UserID,
			PreviousRevision: current.Revision, ResultingRevision: nextRevision,
			FormatVersion: command.FormatVersion, SourceBytes: len(command.Source),
		})
		if err != nil {
			return UserSettingsReplacementResult{}, err
		}
	}
	stored, err := s.settings.Replace(ctx, &store.UserSettingsReplacement{
		UserID: principal.UserID, Source: command.Source, FormatVersion: command.FormatVersion,
		ExpectedRevision: command.ExpectedRevision, NextRevision: nextRevision,
		UpdatedAt: updatedAt, AuditEvent: audit,
	}, idempotency)
	if err != nil {
		return UserSettingsReplacementResult{}, userSettingsReplacementError(err)
	}
	if stored == nil || !stored.Revision.IsValid() || stored.FormatVersion <= 0 || stored.UpdatedAt.IsZero() {
		return UserSettingsReplacementResult{}, userSettingsUnavailable(errors.New("invalid settings replacement result"))
	}
	result := UserSettingsReplacementResult{
		Revision: stored.Revision, FormatVersion: stored.FormatVersion,
		UpdatedAt: stored.UpdatedAt, Changed: stored.Changed, Replayed: stored.Replayed,
	}
	if result.Changed && !result.Replayed {
		if effectErr := s.effects.Changed(ctx, userSettingsChanged{
			UserID: principal.UserID, Revision: result.Revision,
			FormatVersion: result.FormatVersion, ChangedAt: result.UpdatedAt,
		}); effectErr != nil {
			s.effectFailures.Report(ctx, "user_settings_changed", effectErr)
		}
	}
	return result, nil
}

type userSettingsRealtimeEffects struct{ realtime *realtimeService }

func (e userSettingsRealtimeEffects) Changed(ctx context.Context, change userSettingsChanged) error {
	event, err := apprealtime.NewUserSettingsChangedEvent(
		change.UserID, change.Revision, change.FormatVersion, change.ChangedAt,
	)
	if err != nil {
		return err
	}
	return e.realtime.Publish(ctx, event)
}

type userSettingsEffectReporter struct{ realtime *realtimeService }

func (r userSettingsEffectReporter) Report(ctx context.Context, operation string, err error) {
	r.realtime.reportTransientFailure(ctx, operation, err)
}

func userSettingsReplacementError(err error) error {
	var revision *store.ErrUserSettingsRevisionConflict
	var invalid *store.ErrInvalidInput
	switch {
	case errors.As(err, &revision):
		return NewError("user_settings.revision_conflict").WithField("current_revision", revision.CurrentRevision.String()).Wrap(err)
	case errors.As(err, &invalid):
		return NewError("user_settings.invalid").Wrap(err)
	case idempotencyError(err) != nil:
		return idempotencyError(err)
	default:
		return userSettingsUnavailable(err)
	}
}

// ReadOwn returns only the document owned by the interactive Session
// principal. There is intentionally no caller-supplied User identifier.
func (a *App) ReadOwnUserSettings(ctx context.Context, invocation Invocation) (UserSettingsView, error) {
	if a == nil || a.userSettings == nil {
		return UserSettingsView{}, NewError("user_settings.unavailable")
	}
	return a.userSettings.ReadOwn(ctx, invocation)
}

func (s *userSettingsService) ReadOwn(ctx context.Context, invocation Invocation) (UserSettingsView, error) {
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess {
		return UserSettingsView{}, invalidTokenAppError()
	}
	document, err := s.settings.Get(ctx, principal.UserID)
	if err != nil {
		return UserSettingsView{}, userSettingsUnavailable(err)
	}
	if document == nil || document.UserID != principal.UserID || document.Validate() != nil {
		return UserSettingsView{}, userSettingsUnavailable(errors.New("invalid persisted settings metadata"))
	}
	writable := document.FormatVersion == model.UserSettingsFormatVersion1
	if writable && len(validateUserSettingsSource(document.Source)) != 0 {
		return UserSettingsView{}, userSettingsUnavailable(errors.New("invalid persisted settings source"))
	}
	return UserSettingsView{
		Source:        document.Source,
		FormatVersion: document.FormatVersion,
		Revision:      document.Revision,
		Writable:      writable,
		UpdatedAt:     document.UpdatedAt,
	}, nil
}

func userSettingsUnavailable(err error) error {
	return NewError("user_settings.unavailable").Wrap(err)
}

func prepareInitialUserSettingsDocument(user *model.User) (*model.UserSettingsDocument, error) {
	if user == nil || !user.ID.IsValid() || user.CreatedAt.IsZero() {
		return nil, errors.New("prepared user is required for initial settings")
	}
	return model.NewUserSettingsDocument(user.ID, model.NewUserSettingsRevision(), user.CreatedAt)
}

var _ userSettingsStore = (store.UserSettingsStore)(nil)
