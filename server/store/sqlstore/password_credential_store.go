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
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlPasswordCredentialStore struct {
	*SqlStore
	credentialsQuery sq.SelectBuilder
}

type passwordCredentialRow struct {
	ID                string `db:"id"`
	CreateAt          int64  `db:"create_at"`
	UpdateAt          int64  `db:"update_at"`
	DeleteAt          int64  `db:"delete_at"`
	UserID            string `db:"user_id"`
	PasswordHash      string `db:"password_hash"`
	PasswordChangedAt int64  `db:"password_changed_at"`
}

func passwordCredentialSliceColumns() []string {
	return []string{
		"password_credentials.id",
		"password_credentials.create_at",
		"password_credentials.update_at",
		"password_credentials.delete_at",
		"password_credentials.user_id",
		"password_credentials.password_hash",
		"password_credentials.password_changed_at",
	}
}

func newSqlPasswordCredentialStore(sqlStore *SqlStore) store.PasswordCredentialStore {
	s := &SqlPasswordCredentialStore{SqlStore: sqlStore}
	s.credentialsQuery = s.getQueryBuilder().
		Select(passwordCredentialSliceColumns()...).
		From("password_credentials")
	return s
}

func (s SqlPasswordCredentialStore) Save(
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
			id, create_at, update_at, delete_at, user_id,
			password_hash, password_changed_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :user_id,
			:password_hash, :password_changed_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save password credential: %w",
			translateError("password_credential", credential.ID.String(), err),
		)
	}
	return nil
}

func (s SqlPasswordCredentialStore) GetByUser(
	ctx context.Context,
	userID string,
) (*model.PasswordCredential, error) {
	var row passwordCredentialRow
	query := s.credentialsQuery.Where(sq.Eq{
		"password_credentials.user_id":   userID,
		"password_credentials.delete_at": int64(0),
	})
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("password_credential", userID, err)
	}
	return row.model(), nil
}

func (s SqlPasswordCredentialStore) Update(
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
		   SET update_at = :update_at,
		       password_hash = :password_hash,
		       password_changed_at = :password_changed_at
		 WHERE id = :id AND user_id = :user_id AND delete_at = 0`, &row)
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
		CreateAt:          model.MillisFromTime(credential.CreatedAt),
		UpdateAt:          model.MillisFromTime(credential.UpdatedAt),
		DeleteAt:          credential.ArchivedAt.Millis(),
		UserID:            credential.UserID.String(),
		PasswordHash:      credential.PasswordHash,
		PasswordChangedAt: model.MillisFromTime(credential.PasswordChangedAt),
	}
}

func (row passwordCredentialRow) model() *model.PasswordCredential {
	return &model.PasswordCredential{
		ID:                model.PasswordCredentialID(row.ID),
		CreatedAt:         model.TimeFromMillis(row.CreateAt),
		UpdatedAt:         model.TimeFromMillis(row.UpdateAt),
		ArchivedAt:        model.OptionalTimeFromMillis(row.DeleteAt),
		UserID:            model.UserID(row.UserID),
		PasswordHash:      row.PasswordHash,
		PasswordChangedAt: model.TimeFromMillis(row.PasswordChangedAt),
	}
}

var _ store.PasswordCredentialStore = (*SqlPasswordCredentialStore)(nil)
