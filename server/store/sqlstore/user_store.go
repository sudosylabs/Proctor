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
	"database/sql"
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
	ID               string `db:"id"`
	CreateAt         int64  `db:"create_at"`
	UpdateAt         int64  `db:"update_at"`
	DeleteAt         int64  `db:"delete_at"`
	Revision         int64  `db:"revision"`
	Username         string `db:"username"`
	Email            string `db:"email"`
	EmailVerified    bool   `db:"email_verified"`
	DisplayName      string `db:"display_name"`
	FirstName        string `db:"first_name"`
	LastName         string `db:"last_name"`
	Locale           string `db:"locale"`
	Timezone         string `db:"timezone"`
	LastLoginAt      int64  `db:"last_login_at"`
	LastActivityAt   int64  `db:"last_activity_at"`
	DisabledAt       int64  `db:"disabled_at"`
	ExpectedRevision int64  `db:"expected_revision"`
}

func userSliceColumns() []string {
	return []string{
		"users.id",
		"users.create_at",
		"users.update_at",
		"users.delete_at",
		"users.revision",
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
			id, create_at, update_at, delete_at, revision, username, email,
			email_verified, display_name, first_name, last_name, locale,
			timezone, last_login_at, last_activity_at, disabled_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :revision, :username, :email,
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

func (s SqlUserStore) List(
	ctx context.Context,
	options store.UserListOptions,
) ([]*model.User, error) {
	if options.Limit < 1 || options.Limit > 200 ||
		(options.AfterUsername == "") != (options.AfterId == "") {
		return nil, store.NewErrInvalidInput("user", "list_options", nil)
	}
	query := s.usersQuery.Where(sq.Eq{"users.delete_at": int64(0)})
	if !options.IncludeDisabled {
		query = query.Where(sq.Eq{"users.disabled_at": int64(0)})
	}
	term := strings.TrimSpace(options.Query)
	if term != "" {
		pattern := "%" + term + "%"
		query = query.Where(`(
			users.username ILIKE ? OR users.email ILIKE ? OR
			users.display_name ILIKE ? OR users.first_name ILIKE ? OR
			users.last_name ILIKE ?
		)`, pattern, pattern, pattern, pattern, pattern)
	}
	if options.AfterUsername != "" {
		query = query.Where(
			"(users.username > ? OR (users.username = ? AND users.id > ?))",
			options.AfterUsername,
			options.AfterUsername,
			options.AfterId,
		)
	}
	query = query.OrderBy("users.username", "users.id").Limit(uint64(options.Limit))
	rows := []userRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	users := make([]*model.User, 0, len(rows))
	for _, row := range rows {
		users = append(users, row.model())
	}
	return users, nil
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
	if user == nil || user.Revision <= 0 {
		return nil, store.NewErrInvalidInput("user", "value", nil)
	}
	candidate := *user
	expectedRevision := candidate.Revision
	candidate.PreUpdate()
	candidate.Revision++
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin user update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateUserProfile(ctx, tx, &candidate, expectedRevision); err != nil {
		return nil, err
	}
	updated, err := getUserByID(ctx, tx, candidate.Id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit user update: %w", err)
	}
	return updated, nil
}

func (s SqlUserStore) UpdateProfileWithAudit(ctx context.Context, input *store.UserProfileUpdate) (*model.User, error) {
	if input == nil || input.User == nil || input.ExpectedRevision <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("user", "profile_update", nil)
	}
	candidate := *input.User
	if candidate.Revision != input.ExpectedRevision {
		return nil, store.NewErrInvalidInput("user", "revision", candidate.Revision)
	}
	candidate.PrepareUpdate(input.AuditAt)
	candidate.Revision = input.ExpectedRevision + 1
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, store.NewErrInvalidInput("user", "value", nil).Wrap(appErr)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited user profile update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateUserProfile(ctx, tx, &candidate, input.ExpectedRevision); err != nil {
		return nil, err
	}
	updated, err := getUserByID(ctx, tx, candidate.Id)
	if err != nil {
		return nil, err
	}
	encoded, appErr := model.EncodeAuditData(updated.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
		return nil, fmt.Errorf("complete user profile update audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited user profile update: %w", err)
	}
	return updated, nil
}

func getUserByID(ctx context.Context, executor sqlxExecutor, id string) (*model.User, error) {
	var row userRow
	if err := executor.Get(ctx, &row, `
		SELECT id, create_at, update_at, delete_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		  FROM users
		 WHERE id = ? AND delete_at = 0`, id); err != nil {
		return nil, translateError("user", id, err)
	}
	return row.model(), nil
}

func updateUserProfile(ctx context.Context, executor sqlxExecutor, candidate *model.User, expectedRevision int64) error {
	row := newUserRow(candidate)
	row.ExpectedRevision = expectedRevision
	result, err := executor.NamedExec(ctx, `
		UPDATE users
		   SET update_at = GREATEST(update_at, :update_at),
		       revision = :revision,
		       username = :username,
		       email = :email,
		       email_verified = :email_verified,
		       display_name = :display_name,
		       first_name = :first_name,
		       last_name = :last_name,
		       locale = :locale,
		       timezone = :timezone
		 WHERE id = :id AND delete_at = 0 AND revision = :expected_revision`, &row)
	if err != nil {
		return fmt.Errorf("update user profile: %w", translateError("user", candidate.Id, err))
	}
	if err := requireUserRevisionAffected(ctx, executor, result, candidate.Id); err != nil {
		return err
	}
	return nil
}

func requireUserRevisionAffected(ctx context.Context, executor sqlxExecutor, result sql.Result, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read user affected rows: %w", err)
	}
	if affected != 0 {
		return nil
	}
	var exists bool
	if err := executor.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM users WHERE id = ? AND delete_at = 0)`, id); err != nil {
		return fmt.Errorf("check user revision conflict: %w", err)
	}
	if exists {
		return store.NewErrConflict("user", "user_changed", nil)
	}
	return store.NewErrNotFound("user", id).Wrap(sql.ErrNoRows)
}

func (s SqlUserStore) SetDisabled(
	ctx context.Context,
	id string,
	disabledAt int64,
	updateAt int64,
) (*model.User, error) {
	if updateAt <= 0 || disabledAt < 0 || (disabledAt != 0 && disabledAt != updateAt) {
		return nil, store.NewErrInvalidInput("user", "disabled_at", disabledAt)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin set user disabled state: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := setUserDisabled(ctx, tx, id, disabledAt, updateAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit set user disabled state: %w", err)
	}
	return row.model(), nil
}

func (s SqlUserStore) DisableAndRevokeSessions(
	ctx context.Context,
	id string,
	disabledAt int64,
	reason string,
) (*model.User, []*model.Session, []string, error) {
	if disabledAt <= 0 {
		return nil, nil, nil, store.NewErrInvalidInput(
			"user", "disabled_at", disabledAt,
		)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf(
			"begin disable user and revoke sessions: %w",
			err,
		)
	}
	defer func() { _ = tx.Rollback() }()
	row, err := setUserDisabled(ctx, tx, id, disabledAt, disabledAt)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := lockUserSessions(ctx, tx, id); err != nil {
		return nil, nil, nil, err
	}
	sessionRows, hashes, err := revokeAllUserSessions(
		ctx, tx, id, disabledAt, reason,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, nil, fmt.Errorf(
			"commit disable user and revoke sessions: %w",
			err,
		)
	}
	return row.model(), revokedSessionModels(sessionRows, disabledAt, reason), hashes, nil
}

func setUserDisabled(
	ctx context.Context,
	tx *sqlxTxWrapper,
	id string,
	disabledAt int64,
	updateAt int64,
) (*userRow, error) {
	if updateAt <= 0 || disabledAt < 0 || (disabledAt != 0 && disabledAt != updateAt) {
		return nil, store.NewErrInvalidInput("user", "disabled_at", disabledAt)
	}
	if disabledAt != 0 {
		if _, err := tx.Exec(
			ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"proctor:system-administrator-bindings",
		); err != nil {
			return nil, fmt.Errorf("lock administrator bindings: %w", err)
		}
		var isAdministrator bool
		if err := tx.Get(ctx, &isAdministrator, `
			SELECT EXISTS (
				SELECT 1
				  FROM role_bindings rb
				  JOIN roles r ON r.id = rb.role_id
				 WHERE rb.user_id = $1
				   AND r.name = $2 AND r.built_in = true AND r.delete_at = 0
				   AND rb.scope_type = 'institution' AND rb.delete_at = 0
				   AND rb.start_at <= $3
				   AND (rb.end_at = 0 OR rb.end_at > $3)
			)`, id, model.SystemAdministratorRoleName, disabledAt); err != nil {
			return nil, fmt.Errorf("check administrator binding: %w", err)
		}
		if isAdministrator {
			var remaining bool
			if err := tx.Get(ctx, &remaining, `
				SELECT EXISTS (
					SELECT 1
					  FROM role_bindings rb
					  JOIN roles r ON r.id = rb.role_id
					  JOIN users u ON u.id = rb.user_id
					 WHERE rb.user_id <> $1
					   AND r.name = $2 AND r.built_in = true AND r.delete_at = 0
					   AND rb.scope_type = 'institution' AND rb.delete_at = 0
					   AND rb.start_at <= $3
					   AND (rb.end_at = 0 OR rb.end_at > $3)
					   AND u.delete_at = 0 AND u.disabled_at = 0
				)`, id, model.SystemAdministratorRoleName, disabledAt); err != nil {
				return nil, fmt.Errorf("check remaining administrator: %w", err)
			}
			if !remaining {
				return nil, store.NewErrConflict(
					"user", "users_last_system_admin", nil,
				)
			}
		}
	}
	var row userRow
	if err := tx.Get(ctx, &row, `
		UPDATE users
		   SET update_at = ?, disabled_at = ?, revision = revision + 1
		 WHERE id = ? AND delete_at = 0
		RETURNING id, create_at, update_at, delete_at, revision, username, email,
		          email_verified, display_name, first_name, last_name, locale,
		          timezone, last_login_at, last_activity_at, disabled_at`,
		updateAt, disabledAt, id,
	); err != nil {
		return nil, fmt.Errorf(
			"set user disabled state: %w",
			translateError("user", id, err),
		)
	}
	return &row, nil
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
		Revision:       user.Revision,
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
		Revision:       row.Revision,
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
