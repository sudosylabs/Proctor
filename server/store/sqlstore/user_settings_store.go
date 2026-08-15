// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLUserSettingsStore struct {
	*SQLStore
	documentsQuery sq.SelectBuilder
}

type userSettingsDocumentRow struct {
	UserID        string    `db:"user_id"`
	Source        string    `db:"source"`
	FormatVersion int       `db:"format_version"`
	Revision      string    `db:"revision"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

func userSettingsDocumentColumns() []string {
	return []string{
		"user_settings_documents.user_id",
		"user_settings_documents.source",
		"user_settings_documents.format_version",
		"user_settings_documents.revision",
		"user_settings_documents.created_at",
		"user_settings_documents.updated_at",
	}
}

func newSQLUserSettingsStore(sqlStore *SQLStore) store.UserSettingsStore {
	result := &SQLUserSettingsStore{SQLStore: sqlStore}
	result.documentsQuery = result.getQueryBuilder().
		Select(userSettingsDocumentColumns()...).
		From("user_settings_documents")
	return result
}

func (s SQLUserSettingsStore) Get(ctx context.Context, userID model.UserID) (*model.UserSettingsDocument, error) {
	if !userID.IsValid() {
		return nil, store.NewErrInvalidInput("user_settings_document", "user_id", nil)
	}
	query, arguments, err := s.documentsQuery.
		Where(sq.Eq{"user_settings_documents.user_id": userID.String()}).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build user settings query: %w", err)
	}
	var row userSettingsDocumentRow
	if err := s.GetMaster().Get(ctx, &row, query, arguments...); err != nil {
		if err == sql.ErrNoRows {
			var userExists bool
			if existsErr := s.GetMaster().Get(
				ctx,
				&userExists,
				"SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)",
				userID.String(),
			); existsErr != nil {
				return nil, fmt.Errorf("check user settings owner: %w", existsErr)
			}
			if userExists {
				return nil, invalidPersistedState(
					"user_settings_document",
					"value",
					errors.New("required user settings row is missing"),
				)
			}
			return nil, store.NewErrNotFound("user_settings_document", userID.String()).Wrap(err)
		}
		return nil, fmt.Errorf("get user settings document: %w", err)
	}
	return row.model()
}

func (s SQLUserSettingsStore) Replace(
	ctx context.Context,
	input *store.UserSettingsReplacement,
	command *store.CommandIdempotency,
) (*store.UserSettingsReplacementResult, error) {
	if err := validateUserSettingsReplacement(input, command); err != nil {
		return nil, err
	}
	mutation, err := runIdempotentMutation(ctx, s.SQLStore, "user settings replacement", idempotentMutation[userSettingsReplacementOutcome]{
		command: command,
		execute: func(ctx context.Context, tx *sqlxTxWrapper) (userSettingsReplacementOutcome, error) {
			var row userSettingsDocumentRow
			if err := tx.Get(ctx, &row, `SELECT user_id, source, format_version, revision, created_at, updated_at
			FROM user_settings_documents WHERE user_id = ? FOR UPDATE`, input.UserID.String()); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					var userExists bool
					if existsErr := tx.Get(ctx, &userExists, "SELECT EXISTS (SELECT 1 FROM users WHERE id = ?)", input.UserID.String()); existsErr != nil {
						return userSettingsReplacementOutcome{}, fmt.Errorf("check user settings owner: %w", existsErr)
					}
					if userExists {
						return userSettingsReplacementOutcome{}, invalidPersistedState("user_settings_document", "value", errors.New("required user settings row is missing"))
					}
					return userSettingsReplacementOutcome{}, store.NewErrNotFound("user_settings_document", input.UserID.String()).Wrap(err)
				}
				return userSettingsReplacementOutcome{}, fmt.Errorf("lock user settings document: %w", err)
			}
			current, err := row.model()
			if err != nil {
				return userSettingsReplacementOutcome{}, err
			}
			if current.Revision != input.ExpectedRevision {
				return userSettingsReplacementOutcome{}, &store.ErrUserSettingsRevisionConflict{CurrentRevision: current.Revision}
			}
			if current.Source == input.Source && current.FormatVersion == input.FormatVersion {
				return userSettingsReplacementOutcome{Result: store.UserSettingsReplacementResult{
					Revision: current.Revision, FormatVersion: current.FormatVersion,
					UpdatedAt: current.UpdatedAt,
				}}, nil
			}
			if !input.UpdatedAt.After(current.UpdatedAt) {
				return userSettingsReplacementOutcome{}, store.NewErrInvalidInput("user_settings_document", "updated_at", nil)
			}
			if err := validateUserSettingsReplacementAudit(input); err != nil {
				return userSettingsReplacementOutcome{}, err
			}
			result, err := tx.Exec(ctx, `UPDATE user_settings_documents
			SET source = ?, format_version = ?, revision = ?, updated_at = ?
			WHERE user_id = ? AND revision = ?`,
				input.Source, input.FormatVersion, input.NextRevision.String(), model.TimeUTC(input.UpdatedAt),
				input.UserID.String(), input.ExpectedRevision.String())
			if err != nil {
				return userSettingsReplacementOutcome{}, fmt.Errorf("replace user settings document: %w", translateError("user_settings_document", input.UserID.String(), err))
			}
			if affected, err := result.RowsAffected(); err != nil {
				return userSettingsReplacementOutcome{}, fmt.Errorf("inspect user settings replacement: %w", err)
			} else if affected != 1 {
				return userSettingsReplacementOutcome{}, &store.ErrUserSettingsRevisionConflict{CurrentRevision: current.Revision}
			}
			audit, err := insertAuditEvent(ctx, tx, input.AuditEvent)
			if err != nil {
				return userSettingsReplacementOutcome{}, err
			}
			return userSettingsReplacementOutcome{Result: store.UserSettingsReplacementResult{
				Revision: input.NextRevision, FormatVersion: input.FormatVersion,
				UpdatedAt: model.TimeUTC(input.UpdatedAt), Changed: true,
			}, AuditEventID: audit.ID.String()}, nil
		},
		encode: encodeUserSettingsReplacementOutcome,
		decode: decodeUserSettingsReplacementOutcome,
		freshAuditEventID: func(outcome userSettingsReplacementOutcome) (string, error) {
			return outcome.AuditEventID, nil
		},
	})
	if err != nil {
		return nil, err
	}
	result := mutation.Value.Result
	result.Replayed = mutation.Replayed
	return &result, nil
}

type userSettingsReplacementOutcome struct {
	Result       store.UserSettingsReplacementResult
	AuditEventID string
}

func encodeUserSettingsReplacementOutcome(outcome userSettingsReplacementOutcome) ([]byte, error) {
	return json.Marshal(outcome.Result)
}

func decodeUserSettingsReplacementOutcome(version int, encoded []byte) (userSettingsReplacementOutcome, error) {
	if version != 1 {
		return userSettingsReplacementOutcome{}, invalidPersistedState(
			"command_outcome", "outcome_version", errors.New("unsupported user settings outcome version"),
		)
	}
	var result store.UserSettingsReplacementResult
	if err := json.Unmarshal(encoded, &result); err != nil || !result.Revision.IsValid() ||
		result.FormatVersion <= 0 || result.UpdatedAt.IsZero() {
		return userSettingsReplacementOutcome{}, invalidPersistedState(
			"command_outcome", "outcome", errors.New("invalid user settings outcome"),
		)
	}
	return userSettingsReplacementOutcome{Result: result}, nil
}

func validateUserSettingsReplacement(input *store.UserSettingsReplacement, command *store.CommandIdempotency) error {
	if input == nil || !input.UserID.IsValid() || input.FormatVersion <= 0 ||
		!input.ExpectedRevision.IsValid() || !input.NextRevision.IsValid() ||
		input.ExpectedRevision == input.NextRevision || input.UpdatedAt.IsZero() ||
		command == nil || command.UserID != input.UserID || command.Operation != "user_settings.replace" ||
		command.FingerprintVersion <= 0 || command.OutcomeVersion != 1 ||
		command.Retention <= 0 || command.Wait <= 0 {
		return store.NewErrInvalidInput("user_settings_document", "replacement", nil)
	}
	candidate := &model.UserSettingsDocument{
		UserID: input.UserID, Source: input.Source, FormatVersion: input.FormatVersion,
		Revision: input.NextRevision, CreatedAt: input.UpdatedAt, UpdatedAt: input.UpdatedAt,
	}
	if err := candidate.Validate(); err != nil {
		return store.NewErrInvalidInput("user_settings_document", "replacement", nil).Wrap(err)
	}
	return nil
}

func validateUserSettingsReplacementAudit(input *store.UserSettingsReplacement) error {
	if input.AuditEvent == nil || !input.AuditEvent.ID.IsZero() ||
		input.AuditEvent.ActorID != input.UserID || input.AuditEvent.Action != "user.settings.replace" ||
		input.AuditEvent.Resource != (model.Resource{Type: model.ResourceUser, ID: input.UserID.String()}) ||
		input.AuditEvent.Status != model.AuditStatusSuccess {
		return store.NewErrInvalidInput("user_settings_document", "audit_event", nil)
	}
	return nil
}

func insertUserSettingsDocument(ctx context.Context, executor sqlxExecutor, document *model.UserSettingsDocument) error {
	if document == nil {
		return store.NewErrInvalidInput("user_settings_document", "value", nil)
	}
	candidate := document.Clone()
	if err := candidate.Validate(); err != nil {
		return store.NewErrInvalidInput("user_settings_document", "value", nil).Wrap(err)
	}
	row := newUserSettingsDocumentRow(candidate)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO user_settings_documents (
			user_id, source, format_version, revision, created_at, updated_at
		) VALUES (
			:user_id, :source, :format_version, :revision, :created_at, :updated_at
		)`, &row); err != nil {
		return fmt.Errorf("create user settings document: %w", translateError("user_settings_document", candidate.UserID.String(), err))
	}
	return nil
}

func validateInitialUserSettingsDocument(user *model.User, document *model.UserSettingsDocument) error {
	if user == nil || document == nil {
		return store.NewErrInvalidInput("user_settings_document", "creation", nil)
	}
	if err := document.Validate(); err != nil {
		return store.NewErrInvalidInput("user_settings_document", "value", nil).Wrap(err)
	}
	if document.UserID != user.ID || document.FormatVersion != model.UserSettingsFormatVersion1 ||
		document.Source != model.UserSettingsInitialSource ||
		!document.CreatedAt.Equal(user.CreatedAt) || !document.UpdatedAt.Equal(user.CreatedAt) {
		return store.NewErrInvalidInput("user_settings_document", "creation", nil)
	}
	return nil
}

func newUserSettingsDocumentRow(document *model.UserSettingsDocument) userSettingsDocumentRow {
	return userSettingsDocumentRow{
		UserID:        document.UserID.String(),
		Source:        document.Source,
		FormatVersion: document.FormatVersion,
		Revision:      document.Revision.String(),
		CreatedAt:     model.TimeUTC(document.CreatedAt),
		UpdatedAt:     model.TimeUTC(document.UpdatedAt),
	}
}

func (row userSettingsDocumentRow) model() (*model.UserSettingsDocument, error) {
	userID, err := parsePersistedID("user_settings_document", "user_id", row.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	revision, err := model.ParseUserSettingsRevision(row.Revision)
	if err != nil {
		return nil, invalidPersistedState("user_settings_document", "revision", err)
	}
	document := &model.UserSettingsDocument{
		UserID:        userID,
		Source:        row.Source,
		FormatVersion: row.FormatVersion,
		Revision:      revision,
		CreatedAt:     model.TimeUTC(row.CreatedAt),
		UpdatedAt:     model.TimeUTC(row.UpdatedAt),
	}
	if err := validatePersistedModel("user_settings_document", document); err != nil {
		return nil, err
	}
	return document, nil
}
