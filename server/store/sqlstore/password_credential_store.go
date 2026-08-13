// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/user_store.go.
// Password material is isolated in Proctor's PasswordCredential model and this
// store never returns it through a User query.

package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLPasswordCredentialStore struct {
	*SQLStore
	credentialsQuery sq.SelectBuilder
}

type passwordCredentialRow struct {
	ID                string       `db:"id"`
	CreatedAt         time.Time    `db:"created_at"`
	UpdatedAt         time.Time    `db:"updated_at"`
	ArchivedAt        sql.NullTime `db:"archived_at"`
	UserID            string       `db:"user_id"`
	PasswordHash      string       `db:"password_hash"`
	PasswordChangedAt time.Time    `db:"password_changed_at"`
}

func passwordCredentialSliceColumns() []string {
	return []string{
		"password_credentials.id",
		"password_credentials.created_at",
		"password_credentials.updated_at",
		"password_credentials.archived_at",
		"password_credentials.user_id",
		"password_credentials.password_hash",
		"password_credentials.password_changed_at",
	}
}

func newSQLPasswordCredentialStore(sqlStore *SQLStore) store.PasswordCredentialStore {
	s := &SQLPasswordCredentialStore{SQLStore: sqlStore}
	s.credentialsQuery = s.getQueryBuilder().
		Select(passwordCredentialSliceColumns()...).
		From("password_credentials")
	return s
}

func (s SQLPasswordCredentialStore) Save(
	ctx context.Context,
	credential *model.PasswordCredential,
) (*model.PasswordCredential, error) {
	if credential == nil {
		return nil, store.NewErrInvalidInput("password_credential", "value", nil)
	}
	if !credential.ID.IsZero() {
		return nil, store.NewErrInvalidInput("password_credential", "id", credential.ID.String())
	}
	candidate := *credential
	candidate.PrepareCreate(model.NewPasswordCredentialID(), model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, err
	}

	if err := insertPasswordCredential(ctx, s.GetMaster(), &candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func insertPasswordCredential(
	ctx context.Context,
	executor sqlxExecutor,
	credential *model.PasswordCredential,
) error {
	row := newPasswordCredentialRow(credential)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO password_credentials (
			id, created_at, updated_at, archived_at, user_id,
			password_hash, password_changed_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :user_id,
			:password_hash, :password_changed_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save password credential: %w",
			translateError("password_credential", credential.ID.String(), err),
		)
	}
	return nil
}

func (s SQLPasswordCredentialStore) GetByUser(
	ctx context.Context,
	userID string,
) (*model.PasswordCredential, error) {
	var row passwordCredentialRow
	query := s.credentialsQuery.Where(sq.Eq{
		"password_credentials.user_id":     userID,
		"password_credentials.archived_at": nil,
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("password_credential", userID, err)
	}
	return row.model()
}

func (s SQLPasswordCredentialStore) Update(
	ctx context.Context,
	credential *model.PasswordCredential,
) (*model.PasswordCredential, error) {
	if credential == nil {
		return nil, store.NewErrInvalidInput("password_credential", "value", nil)
	}
	candidate := *credential
	candidate.PrepareUpdate(model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, err
	}

	row := newPasswordCredentialRow(&candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
		UPDATE password_credentials
		   SET updated_at = :updated_at,
		       password_hash = :password_hash,
		       password_changed_at = :password_changed_at
		 WHERE id = :id AND user_id = :user_id AND archived_at IS NULL`, &row)
	if err != nil {
		return nil, fmt.Errorf(
			"update password credential: %w",
			translateError("password_credential", candidate.ID.String(), err),
		)
	}
	if err := requireAffected(result, "password_credential", candidate.ID.String()); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func newPasswordCredentialRow(credential *model.PasswordCredential) passwordCredentialRow {
	return passwordCredentialRow{
		ID:                credential.ID.String(),
		CreatedAt:         UTCTime(credential.CreatedAt),
		UpdatedAt:         UTCTime(credential.UpdatedAt),
		ArchivedAt:        NullTimeFromOptional(credential.ArchivedAt),
		UserID:            credential.UserID.String(),
		PasswordHash:      credential.PasswordHash,
		PasswordChangedAt: UTCTime(credential.PasswordChangedAt),
	}
}

func (row passwordCredentialRow) model() (*model.PasswordCredential, error) {
	id, err := parsePersistedID("password_credential", "id", row.ID, model.ParsePasswordCredentialID)
	if err != nil {
		return nil, err
	}
	userID, err := parsePersistedID("password_credential", "user_id", row.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	value := &model.PasswordCredential{
		ID:                id,
		CreatedAt:         row.CreatedAt.UTC(),
		UpdatedAt:         row.UpdatedAt.UTC(),
		ArchivedAt:        OptionalTimeFromNullTime(row.ArchivedAt),
		UserID:            userID,
		PasswordHash:      row.PasswordHash,
		PasswordChangedAt: row.PasswordChangedAt.UTC(),
	}
	if err := validatePersistedModel("password_credential", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.PasswordCredentialStore = (*SQLPasswordCredentialStore)(nil)
