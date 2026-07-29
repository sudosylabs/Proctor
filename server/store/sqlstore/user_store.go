// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/channels/store/sqlstore/user_store.go.
// Proctor retains the per-model query builder, explicit columns, named writes,
// normalized identity lookups, model lifecycle, and translated store errors
// while using its credential-separated user model.

package sqlstore

import (
	"context"
	"fmt"
	"strings"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlUserStore struct {
	*SqlStore
	usersQuery sq.SelectBuilder
}

type userRow struct {
	ID             string `db:"id"`
	CreateAt       int64  `db:"create_at"`
	UpdateAt       int64  `db:"update_at"`
	DeleteAt       int64  `db:"delete_at"`
	Username       string `db:"username"`
	Email          string `db:"email"`
	EmailVerified  bool   `db:"email_verified"`
	DisplayName    string `db:"display_name"`
	FirstName      string `db:"first_name"`
	LastName       string `db:"last_name"`
	Locale         string `db:"locale"`
	Timezone       string `db:"timezone"`
	LastLoginAt    int64  `db:"last_login_at"`
	LastActivityAt int64  `db:"last_activity_at"`
	DisabledAt     int64  `db:"disabled_at"`
}

func userSliceColumns() []string {
	return []string{
		"users.id",
		"users.create_at",
		"users.update_at",
		"users.delete_at",
		"users.username",
		"users.email",
		"users.email_verified",
		"users.display_name",
		"users.first_name",
		"users.last_name",
		"users.locale",
		"users.timezone",
		"users.last_login_at",
		"users.last_activity_at",
		"users.disabled_at",
	}
}

func newSqlUserStore(sqlStore *SqlStore) store.UserStore {
	s := &SqlUserStore{SqlStore: sqlStore}
	s.usersQuery = s.getQueryBuilder().Select(userSliceColumns()...).From("users")
	return s
}

func (s SqlUserStore) Save(ctx context.Context, user *model.User) (*model.User, error) {
	if user == nil {
		return nil, store.NewErrInvalidInput("user", "value", nil)
	}
	if user.Id != "" {
		return nil, store.NewErrInvalidInput("user", "id", user.Id)
	}

	candidate := *user
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	if err := insertUser(ctx, s.GetMaster(), &candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (s SqlUserStore) SaveWithPassword(
	ctx context.Context,
	user *model.User,
	credential *model.PasswordCredential,
) (*model.User, *model.PasswordCredential, error) {
	if user == nil || credential == nil {
		return nil, nil, store.NewErrInvalidInput("user", "local_account", nil)
	}
	if user.Id != "" || credential.Id != "" {
		return nil, nil, store.NewErrInvalidInput("user", "id", "must_be_empty")
	}
	userCandidate := *user
	userCandidate.PreSave()
	if appErr := userCandidate.IsValid(); appErr != nil {
		return nil, nil, appErr
	}
	credentialCandidate := *credential
	credentialCandidate.UserId = userCandidate.Id
	credentialCandidate.PreSave()
	if appErr := credentialCandidate.IsValid(); appErr != nil {
		return nil, nil, appErr
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("begin local user save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertUser(ctx, tx, &userCandidate); err != nil {
		return nil, nil, err
	}
	if err := insertPasswordCredential(ctx, tx, &credentialCandidate); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit local user save: %w", err)
	}
	return &userCandidate, &credentialCandidate, nil
}

func insertUser(ctx context.Context, executor sqlxExecutor, user *model.User) error {
	row := newUserRow(user)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO users (
			id, create_at, update_at, delete_at, username, email,
			email_verified, display_name, first_name, last_name, locale,
			timezone, last_login_at, last_activity_at, disabled_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :username, :email,
			:email_verified, :display_name, :first_name, :last_name, :locale,
			:timezone, :last_login_at, :last_activity_at, :disabled_at
		)`, &row); err != nil {
		return fmt.Errorf("save user: %w", translateError("user", user.Id, err))
	}
	return nil
}

func (s SqlUserStore) Get(ctx context.Context, id string) (*model.User, error) {
	return s.get(ctx, s.usersQuery.Where(sq.Eq{
		"users.id":        id,
		"users.delete_at": int64(0),
	}), id)
}

func (s SqlUserStore) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	return s.get(ctx, s.usersQuery.Where(sq.Eq{
		"users.username":  username,
		"users.delete_at": int64(0),
	}), username)
}

func (s SqlUserStore) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	return s.get(ctx, s.usersQuery.Where(sq.Eq{
		"users.email":     email,
		"users.delete_at": int64(0),
	}), email)
}

func (s SqlUserStore) get(
	ctx context.Context,
	query sq.SelectBuilder,
	key string,
) (*model.User, error) {
	var row userRow
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("user", key, err)
	}
	return row.model(), nil
}

func (s SqlUserStore) Update(ctx context.Context, user *model.User) (*model.User, error) {
	if user == nil {
		return nil, store.NewErrInvalidInput("user", "value", nil)
	}
	candidate := *user
	candidate.PreUpdate()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}

	row := newUserRow(&candidate)
	result, err := s.GetMaster().NamedExec(ctx, `
		UPDATE users
		   SET update_at = :update_at,
		       username = :username,
		       email = :email,
		       email_verified = :email_verified,
		       display_name = :display_name,
		       first_name = :first_name,
		       last_name = :last_name,
		       locale = :locale,
		       timezone = :timezone,
		       last_login_at = :last_login_at,
		       last_activity_at = :last_activity_at,
		       disabled_at = :disabled_at
		 WHERE id = :id AND delete_at = 0`, &row)
	if err != nil {
		return nil, fmt.Errorf("update user: %w", translateError("user", candidate.Id, err))
	}
	if err := requireAffected(result, "user", candidate.Id); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (s SqlUserStore) UpdateLastLogin(ctx context.Context, id string, at int64) error {
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE users
		   SET update_at = GREATEST(update_at, ?),
		       last_login_at = GREATEST(last_login_at, ?),
		       last_activity_at = GREATEST(last_activity_at, ?)
		 WHERE id = ? AND delete_at = 0`,
		at,
		at,
		at,
		id,
	)
	if err != nil {
		return fmt.Errorf("update user last login: %w", err)
	}
	return requireAffected(result, "user", id)
}

func newUserRow(user *model.User) userRow {
	return userRow{
		ID:             user.Id,
		CreateAt:       user.CreateAt,
		UpdateAt:       user.UpdateAt,
		DeleteAt:       user.DeleteAt,
		Username:       user.Username,
		Email:          user.Email,
		EmailVerified:  user.EmailVerified,
		DisplayName:    user.DisplayName,
		FirstName:      user.FirstName,
		LastName:       user.LastName,
		Locale:         user.Locale,
		Timezone:       user.Timezone,
		LastLoginAt:    user.LastLoginAt,
		LastActivityAt: user.LastActivityAt,
		DisabledAt:     user.DisabledAt,
	}
}

func (row userRow) model() *model.User {
	return &model.User{
		Id:             row.ID,
		CreateAt:       row.CreateAt,
		UpdateAt:       row.UpdateAt,
		DeleteAt:       row.DeleteAt,
		Username:       row.Username,
		Email:          row.Email,
		EmailVerified:  row.EmailVerified,
		DisplayName:    row.DisplayName,
		FirstName:      row.FirstName,
		LastName:       row.LastName,
		Locale:         row.Locale,
		Timezone:       row.Timezone,
		LastLoginAt:    row.LastLoginAt,
		LastActivityAt: row.LastActivityAt,
		DisabledAt:     row.DisabledAt,
	}
}

var _ store.UserStore = (*SqlUserStore)(nil)
