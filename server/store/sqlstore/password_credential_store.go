// ---------------------------------------------------------------------------------------------
// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Modifications Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------
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
	Revision          int64        `db:"revision"`
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
		"password_credentials.revision",
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
			revision, password_hash, password_changed_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :user_id,
			:revision, :password_hash, :password_changed_at
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

func (s SQLPasswordCredentialStore) Rehash(
	ctx context.Context,
	input *store.PasswordCredentialRehash,
) error {
	if input == nil || !input.ID.IsValid() || !input.UserID.IsValid() || input.ExpectedRevision < 1 ||
		!model.IsValidPasswordHash(input.ExpectedHash) || !model.IsValidPasswordHash(input.PasswordHash) {
		return store.NewErrInvalidInput("password_credential", "rehash", nil)
	}

	result, err := s.GetMaster().Exec(ctx, `
		UPDATE password_credentials
		   SET updated_at = GREATEST(updated_at, statement_timestamp()), password_hash = ?
		 WHERE id = ? AND user_id = ? AND password_hash = ? AND revision = ? AND archived_at IS NULL`,
		input.PasswordHash, input.ID.String(), input.UserID.String(), input.ExpectedHash, input.ExpectedRevision)
	if err != nil {
		return fmt.Errorf("rehash password credential: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read password rehash outcome: %w", err)
	}
	if affected != 1 {
		return store.ErrPasswordCredentialChanged
	}
	return nil
}

func newPasswordCredentialRow(credential *model.PasswordCredential) passwordCredentialRow {
	return passwordCredentialRow{
		ID:                credential.ID.String(),
		CreatedAt:         UTCTime(credential.CreatedAt),
		UpdatedAt:         UTCTime(credential.UpdatedAt),
		ArchivedAt:        NullTimeFromOptional(credential.ArchivedAt),
		UserID:            credential.UserID.String(),
		Revision:          credential.Revision,
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
		Revision:          row.Revision,
		PasswordHash:      row.PasswordHash,
		PasswordChangedAt: row.PasswordChangedAt.UTC(),
	}
	if err := validatePersistedModel("password_credential", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.PasswordCredentialStore = (*SQLPasswordCredentialStore)(nil)

// The caller holds the per-User Session lock, also held by reset and removal.
// Rehash does not advance Revision, so a work-factor upgrade cannot invalidate
// independently collected proof of the same password.
func requireCurrentPasswordProof(ctx context.Context, executor sqlxExecutor, userID model.UserID, proof store.PasswordCredentialProof) error {
	if !proof.IsValid() {
		return store.ErrPasswordCredentialChanged
	}
	var current bool
	if err := executor.Get(ctx, &current, `SELECT EXISTS(
		SELECT 1 FROM password_credentials WHERE id=? AND user_id=? AND revision=? AND archived_at IS NULL
	)`, proof.ID.String(), userID.String(), proof.Revision); err != nil {
		return fmt.Errorf("check password proof: %w", err)
	}
	if !current {
		return store.ErrPasswordCredentialChanged
	}
	return nil
}
