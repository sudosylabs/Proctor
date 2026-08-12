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
	"time"
	"unicode/utf8"

	sq "github.com/Masterminds/squirrel"
	"github.com/lib/pq"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLUserStore struct {
	*SQLStore
	usersQuery sq.SelectBuilder
}

type userRow struct {
	ID                          string         `db:"id"`
	CreatedAt                   time.Time      `db:"created_at"`
	UpdatedAt                   time.Time      `db:"updated_at"`
	ArchivedAt                  sql.NullTime   `db:"archived_at"`
	Revision                    int64          `db:"revision"`
	Username                    string         `db:"username"`
	Email                       string         `db:"email"`
	EmailVerified               bool           `db:"email_verified"`
	DisplayName                 string         `db:"display_name"`
	FirstName                   string         `db:"first_name"`
	LastName                    string         `db:"last_name"`
	Locale                      string         `db:"locale"`
	Timezone                    string         `db:"timezone"`
	LastLoginAt                 sql.NullTime   `db:"last_login_at"`
	LastActivityAt              sql.NullTime   `db:"last_activity_at"`
	DisabledAt                  sql.NullTime   `db:"disabled_at"`
	DefaultProfilePictureSeed   string         `db:"default_profile_picture_seed"`
	DefaultProfilePictureFileID sql.NullString `db:"default_profile_picture_file_id"`
	CustomProfilePictureFileID  sql.NullString `db:"custom_profile_picture_file_id"`
	ProfilePictureChangedAt     sql.NullTime   `db:"profile_picture_changed_at"`
	ExpectedRevision            int64          `db:"expected_revision"`
}

func userSliceColumns() []string {
	return []string{
		"users.id",
		"users.created_at",
		"users.updated_at",
		"users.archived_at",
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
		"users.default_profile_picture_seed",
		"users.default_profile_picture_file_id",
		"users.custom_profile_picture_file_id",
		"users.profile_picture_changed_at",
	}
}

func newSQLUserStore(sqlStore *SQLStore) store.UserStore {
	s := &SQLUserStore{SQLStore: sqlStore}
	s.usersQuery = s.getQueryBuilder().Select(userSliceColumns()...).From("users")
	return s
}

func (s SQLUserStore) Create(ctx context.Context, input *store.UserCreation) (*store.UserCreationResult, error) {
	if input == nil || input.User == nil || input.DefaultProfilePictureJob == nil {
		return nil, store.NewErrInvalidInput("user", "creation", nil)
	}
	user := *input.User
	job := *input.DefaultProfilePictureJob
	if err := user.Validate(); err != nil {
		return nil, err
	}
	if err := validateUserDefaultProfilePictureJob(&user, &job); err != nil {
		return nil, err
	}
	var credential *model.PasswordCredential
	if input.PasswordCredential != nil {
		candidate := *input.PasswordCredential
		if candidate.UserID != user.ID {
			return nil, store.NewErrInvalidInput("user", "password_credential_user_id", candidate.UserID.String())
		}
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		credential = &candidate
	}

	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin user creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertUser(ctx, tx, &user); err != nil {
		return nil, err
	}
	if credential != nil {
		if err := insertPasswordCredential(ctx, tx, credential); err != nil {
			return nil, err
		}
	}
	if _, err := insertQueuedJob(ctx, tx, &job, false); err != nil {
		return nil, fmt.Errorf("enqueue default profile picture generation: %w", translateError("job", job.ID.String(), err))
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit user creation: %w", err)
	}
	return &store.UserCreationResult{User: &user, PasswordCredential: credential}, nil
}

func validateUserDefaultProfilePictureJob(user *model.User, job *model.Job) error {
	if user == nil || job == nil {
		return store.NewErrInvalidInput("user", "default_profile_picture_job", nil)
	}
	command, commandErr := model.DecodeDefaultProfilePictureCommand(job.CommandVersion, job.Command)
	if job.Validate() != nil ||
		job.Type != model.JobTypeProfilePictureGenerateDefault ||
		job.Status != model.JobStatusQueued || job.AttemptCount != 0 ||
		job.DedupePolicy != model.JobDedupeActive ||
		job.DedupeKey != user.ID.String() || commandErr != nil || command.UserID != user.ID {
		return store.NewErrInvalidInput("user", "default_profile_picture_job", nil)
	}
	return nil
}

func insertUser(ctx context.Context, executor sqlxExecutor, user *model.User) error {
	row := newUserRow(user)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO users (
			id, created_at, updated_at, archived_at, revision, username, email,
			email_verified, display_name, first_name, last_name, locale,
			timezone, last_login_at, last_activity_at, disabled_at,
			default_profile_picture_seed, default_profile_picture_file_id,
			custom_profile_picture_file_id, profile_picture_changed_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :revision, :username, :email,
			:email_verified, :display_name, :first_name, :last_name, :locale,
			:timezone, :last_login_at, :last_activity_at, :disabled_at,
			:default_profile_picture_seed, :default_profile_picture_file_id,
			:custom_profile_picture_file_id, :profile_picture_changed_at
		)`, &row); err != nil {
		return fmt.Errorf("save user: %w", translateError("user", user.ID.String(), err))
	}
	return nil
}

func (s SQLUserStore) Get(ctx context.Context, id string) (*model.User, error) {
	return s.get(ctx, s.usersQuery.Where(sq.Eq{
		"users.id":          id,
		"users.archived_at": nil,
	}), id)
}

func (s SQLUserStore) GetByUsername(ctx context.Context, username string) (*model.User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	return s.get(ctx, s.usersQuery.Where(sq.Eq{
		"users.username":    username,
		"users.archived_at": nil,
	}), username)
}

func (s SQLUserStore) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	return s.get(ctx, s.usersQuery.Where(sq.Eq{
		"users.email":       email,
		"users.archived_at": nil,
	}), email)
}

func (s SQLUserStore) List(
	ctx context.Context,
	options store.UserListOptions,
) ([]*model.User, error) {
	if options.Limit < 1 || options.Limit > 200 ||
		(options.AfterUsername == "") != (options.AfterId == "") ||
		(options.AfterId != "" && !model.IsValidId(options.AfterId)) ||
		len(options.Visibility.ClassIDs)+len(options.Visibility.AcademicUnitRootIDs) > 256 ||
		!validVisibilityIDs(options.Visibility.ClassIDs) ||
		!validVisibilityIDs(options.Visibility.AcademicUnitRootIDs) {
		return nil, store.NewErrInvalidInput("user", "list_options", nil)
	}
	query := s.usersQuery.Where(sq.Eq{"users.archived_at": nil})
	if !options.Visibility.InstitutionWide {
		if len(options.Visibility.ClassIDs) == 0 && len(options.Visibility.AcademicUnitRootIDs) == 0 {
			query = query.Where("FALSE")
		} else {
			if options.Visibility.ActiveAt <= 0 {
				return nil, store.NewErrInvalidInput("user", "visibility_active_at", nil)
			}
			activeAt := model.TimeFromMillis(options.Visibility.ActiveAt)
			query = query.Where(`EXISTS (
				SELECT 1 FROM class_members cm
				JOIN classes c ON c.id = cm.class_id AND c.archived_at IS NULL
				JOIN programme_levels pl ON pl.id = c.programme_level_id AND pl.archived_at IS NULL
				JOIN programmes p ON p.id = pl.programme_id AND p.archived_at IS NULL
				WHERE cm.user_id = users.id AND cm.archived_at IS NULL
				AND cm.start_at <= ? AND (cm.end_at IS NULL OR cm.end_at > ?)
				AND (cm.class_id = ANY(?) OR p.academic_unit_id IN (
					WITH RECURSIVE allowed_units AS (
						SELECT id FROM academic_units WHERE id = ANY(?) AND archived_at IS NULL
						UNION ALL SELECT child.id FROM academic_units child
						JOIN allowed_units parent ON child.parent_id = parent.id
						WHERE child.archived_at IS NULL
					) SELECT id FROM allowed_units
				))
			)`, activeAt, activeAt, pq.Array(options.Visibility.ClassIDs), pq.Array(options.Visibility.AcademicUnitRootIDs))
		}
	}
	if !options.IncludeDisabled {
		query = query.Where(sq.Eq{"users.disabled_at": nil})
	}
	if options.MissingDefaultProfilePicture {
		query = query.Where(sq.Eq{"users.default_profile_picture_file_id": nil})
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

func validVisibilityIDs(ids []string) bool {
	for _, id := range ids {
		if !model.IsValidId(id) {
			return false
		}
	}
	return true
}

func (s SQLUserStore) get(
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

func (s SQLUserStore) Update(ctx context.Context, user *model.User) (*model.User, error) {
	if user == nil || user.Revision <= 0 {
		return nil, store.NewErrInvalidInput("user", "value", nil)
	}
	candidate := *user
	expectedRevision := candidate.Revision
	candidate.PrepareUpdate(model.NowUTC())
	candidate.Revision++
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin user update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateUserProfile(ctx, tx, &candidate, expectedRevision); err != nil {
		return nil, err
	}
	updated, err := getUserByID(ctx, tx, candidate.ID.String())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit user update: %w", err)
	}
	return updated, nil
}

func (s SQLUserStore) UpdateProfileWithAudit(ctx context.Context, input *store.UserProfileUpdate) (*model.User, error) {
	if input == nil || input.User == nil || input.ExpectedRevision <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("user", "profile_update", nil)
	}
	candidate := *input.User
	if candidate.Revision != input.ExpectedRevision {
		return nil, store.NewErrInvalidInput("user", "revision", candidate.Revision)
	}
	candidate.PrepareUpdate(model.TimeFromMillis(input.AuditAt))
	candidate.Revision = input.ExpectedRevision + 1
	if err := candidate.Validate(); err != nil {
		return nil, store.NewErrInvalidInput("user", "value", nil).Wrap(err)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited user profile update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateUserProfile(ctx, tx, &candidate, input.ExpectedRevision); err != nil {
		return nil, err
	}
	updated, err := getUserByID(ctx, tx, candidate.ID.String())
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
		SELECT id, created_at, updated_at, archived_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		       , default_profile_picture_seed, default_profile_picture_file_id,
		       custom_profile_picture_file_id, profile_picture_changed_at
		  FROM users
		 WHERE id = ? AND archived_at IS NULL`, id); err != nil {
		return nil, translateError("user", id, err)
	}
	return row.model(), nil
}

func updateUserProfile(ctx context.Context, executor sqlxExecutor, candidate *model.User, expectedRevision int64) error {
	row := newUserRow(candidate)
	row.ExpectedRevision = expectedRevision
	result, err := executor.NamedExec(ctx, `
		UPDATE users
		   SET updated_at = GREATEST(updated_at, :updated_at),
		       revision = :revision,
		       username = :username,
		       email = :email,
		       email_verified = :email_verified,
		       display_name = :display_name,
		       first_name = :first_name,
		       last_name = :last_name,
		       locale = :locale,
		       timezone = :timezone
		 WHERE id = :id AND archived_at IS NULL AND revision = :expected_revision`, &row)
	if err != nil {
		return fmt.Errorf("update user profile: %w", translateError("user", candidate.ID.String(), err))
	}
	if err := requireUserRevisionAffected(ctx, executor, result, candidate.ID.String()); err != nil {
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
	if err := executor.Get(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL)`, id); err != nil {
		return fmt.Errorf("check user revision conflict: %w", err)
	}
	if exists {
		return store.NewErrConflict("user", "user_changed", nil)
	}
	return store.NewErrNotFound("user", id).Wrap(sql.ErrNoRows)
}

func (s SQLUserStore) SetDisabledWithAudit(
	ctx context.Context,
	input *store.UserDisabledStateChange,
) (*store.UserDisabledStateResult, error) {
	if input == nil || !model.IsValidId(input.ID) || input.ExpectedRevision <= 0 ||
		input.ChangedAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("user", "disabled_state_change", nil)
	}
	revocationReason := model.SanitizeUnicode(input.RevocationReason)
	if input.Disabled && utf8.RuneCountInString(revocationReason) > model.SessionRevocationMaxRunes {
		return nil, store.NewErrInvalidInput("session", "revocation_reason", nil)
	}
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin audited user disabled state change: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize disabling with login and refresh rotation before changing the
	// user row. A login that commits first is included in the revocation; one
	// that follows observes the disabled account.
	if input.Disabled {
		if err := lockUserSessions(ctx, tx, input.ID); err != nil {
			return nil, err
		}
	}
	disabledAt := int64(0)
	if input.Disabled {
		disabledAt = input.ChangedAt
	}
	row, err := setUserDisabled(
		ctx,
		tx,
		input.ID,
		input.ExpectedRevision,
		disabledAt,
		input.ChangedAt,
	)
	if err != nil {
		return nil, err
	}

	result := &store.UserDisabledStateResult{
		User:               row.model(),
		RevokedSessions:    []*model.Session{},
		RevokedTokenHashes: []string{},
	}
	if input.Disabled {
		sessionRows, hashes, err := revokeAllUserSessions(
			ctx,
			tx,
			input.ID,
			input.ChangedAt,
			revocationReason,
		)
		if err != nil {
			return nil, err
		}
		result.RevokedSessions = revokedSessionModels(
			sessionRows,
			input.ChangedAt,
			revocationReason,
		)
		result.RevokedTokenHashes = hashes
	}

	encoded, appErr := model.EncodeAuditData(result.User.Auditable())
	if appErr != nil {
		return nil, appErr
	}
	if _, err := completeAuditEvent(
		ctx,
		tx,
		input.AuditEventID,
		model.AuditStatusSuccess,
		"",
		encoded,
		input.AuditAt,
	); err != nil {
		return nil, fmt.Errorf("complete user disabled state audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit audited user disabled state change: %w", err)
	}
	return result, nil
}

func setUserDisabled(
	ctx context.Context,
	tx *sqlxTxWrapper,
	id string,
	expectedRevision int64,
	disabledAt int64,
	updateAt int64,
) (*userRow, error) {
	if expectedRevision <= 0 || updateAt <= 0 || disabledAt < 0 ||
		(disabledAt != 0 && disabledAt != updateAt) {
		return nil, store.NewErrInvalidInput("user", "disabled_at", disabledAt)
	}
	updateTime := model.TimeFromMillis(updateAt)
	if disabledAt != 0 {
		disabledTime := model.TimeFromMillis(disabledAt)
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
				   AND r.name = $2 AND r.built_in = true AND r.archived_at IS NULL
				   AND rb.scope_type = 'institution' AND rb.archived_at IS NULL
				   AND rb.start_at <= $3
				   AND (rb.end_at IS NULL OR rb.end_at > $3)
			)`, id, model.SystemAdministratorRoleName, disabledTime); err != nil {
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
					   AND r.name = $2 AND r.built_in = true AND r.archived_at IS NULL
					   AND rb.scope_type = 'institution' AND rb.archived_at IS NULL
					   AND rb.start_at <= $3
					   AND (rb.end_at IS NULL OR rb.end_at > $3)
					   AND u.archived_at IS NULL AND u.disabled_at IS NULL
				)`, id, model.SystemAdministratorRoleName, disabledTime); err != nil {
				return nil, fmt.Errorf("check remaining administrator: %w", err)
			}
			if !remaining {
				return nil, store.NewErrConflict(
					"user", "users_last_system_admin", nil,
				)
			}
		}
	}
	disabledTime := sql.NullTime{}
	if disabledAt != 0 {
		disabledTime = sql.NullTime{Time: updateTime, Valid: true}
	}
	result, err := tx.Exec(ctx, `
		UPDATE users
		   SET updated_at = ?, disabled_at = ?, revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND revision = ?`,
		updateTime, disabledTime, id, expectedRevision,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"set user disabled state: %w",
			translateError("user", id, err),
		)
	}
	if err := requireUserRevisionAffected(ctx, tx, result, id); err != nil {
		return nil, err
	}
	updated, err := getUserByID(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	row := newUserRow(updated)
	return &row, nil
}

func (s SQLUserStore) UpdateLastLogin(ctx context.Context, id string, at int64) error {
	loginAt := model.TimeFromMillis(at)
	result, err := s.GetMaster().Exec(ctx, `
		UPDATE users
		   SET updated_at = GREATEST(updated_at, ?),
		       last_login_at = GREATEST(last_login_at, ?),
		       last_activity_at = GREATEST(last_activity_at, ?)
		 WHERE id = ? AND archived_at IS NULL`,
		loginAt,
		loginAt,
		loginAt,
		id,
	)
	if err != nil {
		return fmt.Errorf("update user last login: %w", err)
	}
	return requireAffected(result, "user", id)
}

func newUserRow(user *model.User) userRow {
	return userRow{
		ID:                          user.ID.String(),
		CreatedAt:                   UTCTime(user.CreatedAt),
		UpdatedAt:                   UTCTime(user.UpdatedAt),
		ArchivedAt:                  NullTimeFromOptional(user.ArchivedAt),
		Revision:                    user.Revision,
		Username:                    user.Username,
		Email:                       user.Email,
		EmailVerified:               user.EmailVerified,
		DisplayName:                 user.DisplayName,
		FirstName:                   user.FirstName,
		LastName:                    user.LastName,
		Locale:                      user.Locale,
		Timezone:                    user.Timezone,
		LastLoginAt:                 NullTimeFromOptional(user.LastLoginAt),
		LastActivityAt:              NullTimeFromOptional(user.LastActivityAt),
		DisabledAt:                  NullTimeFromOptional(user.DisabledAt),
		DefaultProfilePictureSeed:   user.DefaultProfilePictureSeed,
		DefaultProfilePictureFileID: nullableID(user.DefaultProfilePictureFileID.String()),
		CustomProfilePictureFileID:  nullableID(user.CustomProfilePictureFileID.String()),
		ProfilePictureChangedAt:     NullTimeFromOptional(user.ProfilePictureChangedAt),
	}
}

func (row userRow) model() *model.User {
	return &model.User{
		ID:                          model.UserID(row.ID),
		CreatedAt:                   row.CreatedAt.UTC(),
		UpdatedAt:                   row.UpdatedAt.UTC(),
		ArchivedAt:                  OptionalTimeFromNullTime(row.ArchivedAt),
		Revision:                    row.Revision,
		Username:                    row.Username,
		Email:                       row.Email,
		EmailVerified:               row.EmailVerified,
		DisplayName:                 row.DisplayName,
		FirstName:                   row.FirstName,
		LastName:                    row.LastName,
		Locale:                      row.Locale,
		Timezone:                    row.Timezone,
		LastLoginAt:                 OptionalTimeFromNullTime(row.LastLoginAt),
		LastActivityAt:              OptionalTimeFromNullTime(row.LastActivityAt),
		DisabledAt:                  OptionalTimeFromNullTime(row.DisabledAt),
		DefaultProfilePictureSeed:   row.DefaultProfilePictureSeed,
		DefaultProfilePictureFileID: model.FileEntryID(row.DefaultProfilePictureFileID.String),
		CustomProfilePictureFileID:  model.FileEntryID(row.CustomProfilePictureFileID.String),
		ProfilePictureChangedAt:     OptionalTimeFromNullTime(row.ProfilePictureChangedAt),
	}
}

func nullableID(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

var _ store.UserStore = (*SQLUserStore)(nil)
