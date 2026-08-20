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
	"errors"
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
	if input == nil || input.User == nil || input.Settings == nil || input.DefaultProfilePictureJob == nil {
		return nil, store.NewErrInvalidInput("user", "creation", nil)
	}
	user := *input.User
	settings := input.Settings.Clone()
	job := *input.DefaultProfilePictureJob
	if err := user.Validate(); err != nil {
		return nil, err
	}
	if err := validateUserDefaultProfilePictureJob(&user, &job); err != nil {
		return nil, err
	}
	if err := validateInitialUserSettingsDocument(&user, settings); err != nil {
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

	return runSQLTransaction(ctx, s.GetMaster().Begin, "user creation", func(ctx context.Context, tx *sqlxTxWrapper) (*store.UserCreationResult, error) {
		if err := insertUser(ctx, tx, &user); err != nil {
			return nil, err
		}
		if err := insertUserSettingsDocument(ctx, tx, settings); err != nil {
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
		return &store.UserCreationResult{User: &user, PasswordCredential: credential}, nil
	})
}

const (
	publicLocalRegistrationAuditAction     = "authentication.public_registration"
	publicRegistrationTokenLifetimeMinimum = 5 * time.Minute
	publicRegistrationTokenLifetimeMaximum = 30 * 24 * time.Hour
	publicRegistrationMailLifetimeMinimum  = time.Minute
	publicRegistrationMailLifetimeMaximum  = 30 * 24 * time.Hour
)

func (s SQLUserStore) RegisterLocal(ctx context.Context, input *store.PublicLocalUserRegistration) (*store.PublicLocalUserRegistrationResult, error) {
	if input == nil || input.User == nil || input.Settings == nil || input.PasswordCredential == nil ||
		input.DefaultProfilePictureJob == nil || input.VerificationToken == nil || input.AuditEvent == nil {
		return nil, store.NewErrInvalidInput("user", "public_registration", nil)
	}
	user := *input.User
	settings := input.Settings.Clone()
	credential := *input.PasswordCredential
	defaultJob := *input.DefaultProfilePictureJob
	token := *input.VerificationToken
	audit := input.AuditEvent.Clone()
	if input.TokenLifetime < publicRegistrationTokenLifetimeMinimum || input.TokenLifetime > publicRegistrationTokenLifetimeMaximum ||
		input.MailLifetime < publicRegistrationMailLifetimeMinimum || input.MailLifetime > publicRegistrationMailLifetimeMaximum ||
		input.MailLifetime > input.TokenLifetime || input.TokenLifetime%time.Millisecond != 0 || input.MailLifetime%time.Millisecond != 0 ||
		user.Validate() != nil || user.EmailVerified || user.Revision != 1 || user.ArchivedAt.Valid || user.DisabledAt.Valid ||
		user.LastLoginAt.Valid || user.LastActivityAt.Valid || user.ProfilePictureChangedAt.Valid ||
		user.DefaultProfilePictureFileID.IsValid() || user.CustomProfilePictureFileID.IsValid() ||
		credential.Validate() != nil || credential.UserID != user.ID || credential.ArchivedAt.Valid ||
		validateInitialUserSettingsDocument(&user, settings) != nil || validateUserDefaultProfilePictureJob(&user, &defaultJob) != nil ||
		token.Validate() != nil || token.UserID != user.ID || token.Purpose != model.UserTokenEmailVerification || token.Target != user.Email ||
		audit == nil || !audit.ID.IsZero() || !audit.CreatedAt.IsZero() || !audit.UpdatedAt.IsZero() ||
		audit.Action != publicLocalRegistrationAuditAction || audit.ActorID.IsValid() ||
		audit.Resource.Type != model.ResourceUser || audit.Resource.ID != user.ID.String() ||
		audit.ScopeType != model.RoleScopeInstitution || !model.IsValidId(audit.ScopeID) || audit.Status != model.AuditStatusSuccess {
		return nil, store.NewErrInvalidInput("user", "public_registration", nil)
	}
	payloadKeyID, err := validateUserEmailMail(user.ID, input.VerificationOccurrence, input.VerificationDelivery,
		input.VerificationJob, model.MailOccurrenceAccountToken, model.MailTemplateIdentityVerifyEmail)
	if err != nil || input.VerificationOccurrence.ID.String() != token.ID.String() {
		return nil, store.NewErrInvalidInput("user", "public_registration_mail", err)
	}

	return runSQLTransaction(ctx, s.GetMaster().Begin, "public local user registration", func(ctx context.Context, tx *sqlxTxWrapper) (*store.PublicLocalUserRegistrationResult, error) {
		policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
		if err != nil {
			return nil, err
		}
		if !policy.PublicRegistrationEnabled || !policy.LocalLoginEnabled {
			return nil, store.ErrAuthenticationMethodDisabled
		}
		var databaseNow time.Time
		if err = tx.Get(ctx, &databaseNow, `SELECT clock_timestamp()`); err != nil {
			return nil, fmt.Errorf("read public registration database time: %w", err)
		}
		at := model.TimeUTC(databaseNow).Truncate(time.Millisecond)
		user.CreatedAt, user.UpdatedAt = at, at
		settings.CreatedAt, settings.UpdatedAt = at, at
		credential.CreatedAt, credential.UpdatedAt, credential.PasswordChangedAt = at, at, at
		defaultJob.CreatedAt, defaultJob.UpdatedAt, defaultJob.AvailableAt = at, at, at
		token.CreatedAt, token.UpdatedAt, token.ExpiresAt = at, at, at.Add(input.TokenLifetime)
		occurrence, delivery, deliveryJob, rebaseErr := recoveryMailAtWithLifetime(
			input.VerificationOccurrence, input.VerificationDelivery, input.VerificationJob, at, input.MailLifetime,
		)
		if rebaseErr != nil || user.Validate() != nil || settings.Validate() != nil || credential.Validate() != nil ||
			validateUserDefaultProfilePictureJob(&user, &defaultJob) != nil || token.Validate() != nil {
			return nil, store.NewErrInvalidInput("user", "public_registration_lifecycle", rebaseErr)
		}
		if payloadKeyID != "" {
			if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
		}
		if err = insertUser(ctx, tx, &user); err != nil {
			return nil, err
		}
		if err = insertUserSettingsDocument(ctx, tx, settings); err != nil {
			return nil, err
		}
		if err = insertPasswordCredential(ctx, tx, &credential); err != nil {
			return nil, err
		}
		if _, err = insertQueuedJob(ctx, tx, &defaultJob, false); err != nil {
			return nil, fmt.Errorf("enqueue public registration profile-picture generation: %w", translateError("job", defaultJob.ID.String(), err))
		}
		if err = insertUserToken(ctx, tx, &token); err != nil {
			return nil, err
		}
		if err = insertRecoveryMail(ctx, tx, occurrence, delivery, deliveryJob, payloadKeyID); err != nil {
			return nil, err
		}
		persistedAudit, err := insertAuditEventAt(ctx, tx, audit, at)
		if err != nil {
			return nil, fmt.Errorf("audit public local registration: %w", err)
		}
		return &store.PublicLocalUserRegistrationResult{User: &user, Token: &token, AuditEvent: persistedAudit}, nil
	})
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
		(options.ID != "" && !model.IsValidId(options.ID)) ||
		(options.AfterUsername == "") != (options.AfterId == "") ||
		(options.AfterId != "" && !model.IsValidId(options.AfterId)) ||
		len(options.Visibility.ClassIDs)+len(options.Visibility.AcademicUnitRootIDs)+
			len(options.Visibility.ClassMemberAcademicUnitRootIDs) > 256 ||
		!validVisibilityIDs(options.Visibility.ClassIDs) ||
		!validVisibilityIDs(options.Visibility.AcademicUnitRootIDs) ||
		!validVisibilityIDs(options.Visibility.ClassMemberAcademicUnitRootIDs) {
		return nil, store.NewErrInvalidInput("user", "list_options", nil)
	}
	query := s.usersQuery.Where(sq.Eq{"users.archived_at": nil})
	if options.ID != "" {
		query = query.Where(sq.Eq{"users.id": options.ID})
	}
	if !options.Visibility.InstitutionWide {
		if len(options.Visibility.ClassIDs) == 0 && len(options.Visibility.AcademicUnitRootIDs) == 0 &&
			len(options.Visibility.ClassMemberAcademicUnitRootIDs) == 0 && !options.Visibility.ClassMemberInstitutionWide {
			query = query.Where("FALSE")
		} else {
			if options.Visibility.ActiveAt <= 0 {
				return nil, store.NewErrInvalidInput("user", "visibility_active_at", nil)
			}
			activeAt := model.TimeFromMillis(options.Visibility.ActiveAt)
			query = query.Prefix(`WITH RECURSIVE user_allowed_units AS (
				SELECT id FROM academic_units WHERE id = ANY(?) AND archived_at IS NULL
				UNION ALL SELECT child.id FROM academic_units child
				JOIN user_allowed_units parent ON child.parent_id = parent.id
				WHERE child.archived_at IS NULL
			), class_member_allowed_units AS (
				SELECT id FROM academic_units WHERE id = ANY(?) AND archived_at IS NULL
				UNION ALL SELECT child.id FROM academic_units child
				JOIN class_member_allowed_units parent ON child.parent_id = parent.id
				WHERE child.archived_at IS NULL
			)`, pq.Array(options.Visibility.AcademicUnitRootIDs), pq.Array(options.Visibility.ClassMemberAcademicUnitRootIDs)).Where(`(
			EXISTS (
				SELECT 1 FROM academic_unit_members aum
				WHERE aum.user_id = users.id AND aum.archived_at IS NULL
				AND aum.start_at <= ? AND (aum.end_at IS NULL OR aum.end_at > ?)
				AND aum.academic_unit_id IN (SELECT id FROM user_allowed_units)
			) OR EXISTS (
				SELECT 1 FROM class_members cm
				JOIN classes c ON c.id = cm.class_id AND c.archived_at IS NULL
				JOIN programme_levels pl ON pl.id = c.programme_level_id AND pl.archived_at IS NULL
				JOIN programmes p ON p.id = pl.programme_id AND p.archived_at IS NULL
				WHERE cm.user_id = users.id AND cm.archived_at IS NULL
				AND cm.start_at <= ? AND (cm.end_at IS NULL OR cm.end_at > ?)
				AND (p.academic_unit_id IN (SELECT id FROM user_allowed_units)
					OR cm.class_id = ANY(?) OR ?
					OR p.academic_unit_id IN (SELECT id FROM class_member_allowed_units))
			) OR EXISTS (
				SELECT 1 FROM role_bindings rb
				JOIN roles r ON r.id = rb.role_id AND r.archived_at IS NULL
				LEFT JOIN classes c ON rb.scope_type = 'class' AND c.id = rb.scope_id AND c.archived_at IS NULL
				LEFT JOIN programme_levels pl ON pl.id = c.programme_level_id AND pl.archived_at IS NULL
				LEFT JOIN programmes p ON p.id = pl.programme_id AND p.archived_at IS NULL
				WHERE rb.user_id = users.id AND rb.archived_at IS NULL
				AND rb.start_at <= ? AND (rb.end_at IS NULL OR rb.end_at > ?)
				AND ((rb.scope_type = 'academic_unit' AND rb.scope_id IN (SELECT id FROM user_allowed_units))
					OR (rb.scope_type = 'class' AND p.academic_unit_id IN (SELECT id FROM user_allowed_units)))
			))`, activeAt, activeAt, activeAt, activeAt, pq.Array(options.Visibility.ClassIDs),
				options.Visibility.ClassMemberInstitutionWide, activeAt, activeAt)
		}
	}
	if !options.IncludeDisabled || !options.Visibility.InstitutionWide {
		query = query.Where(sq.Eq{"users.disabled_at": nil})
	}
	if options.MissingDefaultProfilePicture {
		query = query.Where(sq.Eq{"users.default_profile_picture_file_id": nil})
	}
	term := strings.TrimSpace(options.Query)
	if term != "" {
		pattern := "%" + term + "%"
		if options.Visibility.InstitutionWide {
			query = query.Where(`(
				users.username ILIKE ? OR users.email ILIKE ? OR
				users.display_name ILIKE ? OR users.first_name ILIKE ? OR
				users.last_name ILIKE ?
			)`, pattern, pattern, pattern, pattern, pattern)
		} else {
			query = query.Where(`(
				users.username ILIKE ? OR users.display_name ILIKE ? OR
				users.first_name ILIKE ? OR users.last_name ILIKE ?
			)`, pattern, pattern, pattern, pattern)
		}
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
		user, err := row.model()
		if err != nil {
			return nil, err
		}
		users = append(users, user)
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

func (s SQLUserStore) MatchVisibility(
	ctx context.Context,
	userID string,
	visibility store.UserVisibilityScope,
) (store.UserVisibilityMatch, error) {
	if !model.IsValidId(userID) || visibility.InstitutionWide || visibility.ActiveAt <= 0 ||
		len(visibility.ClassIDs)+len(visibility.AcademicUnitRootIDs)+len(visibility.ClassMemberAcademicUnitRootIDs) > 256 ||
		!validVisibilityIDs(visibility.ClassIDs) ||
		!validVisibilityIDs(visibility.AcademicUnitRootIDs) ||
		!validVisibilityIDs(visibility.ClassMemberAcademicUnitRootIDs) {
		return store.UserVisibilityMatch{}, store.NewErrInvalidInput("user", "visibility_match", nil)
	}
	if len(visibility.ClassIDs) == 0 && len(visibility.AcademicUnitRootIDs) == 0 &&
		len(visibility.ClassMemberAcademicUnitRootIDs) == 0 && !visibility.ClassMemberInstitutionWide {
		return store.UserVisibilityMatch{}, nil
	}

	var match struct {
		ScopeType model.RoleScopeType `db:"scope_type"`
		ScopeID   string              `db:"scope_id"`
	}
	err := s.GetMaster().Get(ctx, &match, `
		WITH RECURSIVE input AS (
			SELECT ?::varchar AS user_id, ?::timestamptz AS active_at
		), user_allowed_units(root_id, id) AS (
			SELECT id, id FROM academic_units
			WHERE id = ANY(?::varchar[]) AND archived_at IS NULL
			UNION ALL
			SELECT parent.root_id, child.id
			FROM academic_units child
			JOIN user_allowed_units parent ON child.parent_id = parent.id
			WHERE child.archived_at IS NULL
		), class_member_allowed_units(root_id, id) AS (
			SELECT id, id FROM academic_units
			WHERE id = ANY(?::varchar[]) AND archived_at IS NULL
			UNION ALL
			SELECT parent.root_id, child.id
			FROM academic_units child
			JOIN class_member_allowed_units parent ON child.parent_id = parent.id
			WHERE child.archived_at IS NULL
		), matches(scope_type, scope_id, priority) AS (
			SELECT 'class', cm.class_id, 0
			FROM class_members cm
			JOIN classes c ON c.id = cm.class_id AND c.archived_at IS NULL
			CROSS JOIN input i
			WHERE cm.user_id = i.user_id AND cm.archived_at IS NULL
			  AND cm.start_at <= i.active_at AND (cm.end_at IS NULL OR cm.end_at > i.active_at)
			  AND cm.class_id = ANY(?::varchar[])
			UNION ALL
			SELECT 'class', cm.class_id, 1
			FROM class_members cm
			JOIN classes c ON c.id = cm.class_id AND c.archived_at IS NULL
			JOIN programme_levels pl ON pl.id = c.programme_level_id AND pl.archived_at IS NULL
			JOIN programmes p ON p.id = pl.programme_id AND p.archived_at IS NULL
			CROSS JOIN input i
			WHERE cm.user_id = i.user_id AND cm.archived_at IS NULL
			  AND cm.start_at <= i.active_at AND (cm.end_at IS NULL OR cm.end_at > i.active_at)
			  AND ?
			UNION ALL
			SELECT 'academic_unit', au.root_id, 2
			FROM class_members cm
			JOIN classes c ON c.id = cm.class_id AND c.archived_at IS NULL
			JOIN programme_levels pl ON pl.id = c.programme_level_id AND pl.archived_at IS NULL
			JOIN programmes p ON p.id = pl.programme_id AND p.archived_at IS NULL
			JOIN class_member_allowed_units au ON au.id = p.academic_unit_id
			CROSS JOIN input i
			WHERE cm.user_id = i.user_id AND cm.archived_at IS NULL
			  AND cm.start_at <= i.active_at AND (cm.end_at IS NULL OR cm.end_at > i.active_at)
			UNION ALL
			SELECT 'academic_unit', au.root_id, 2
			FROM academic_unit_members aum
			JOIN user_allowed_units au ON au.id = aum.academic_unit_id
			CROSS JOIN input i
			WHERE aum.user_id = i.user_id AND aum.archived_at IS NULL
			  AND aum.start_at <= i.active_at AND (aum.end_at IS NULL OR aum.end_at > i.active_at)
			UNION ALL
			SELECT 'academic_unit', au.root_id, 2
			FROM class_members cm
			JOIN classes c ON c.id = cm.class_id AND c.archived_at IS NULL
			JOIN programme_levels pl ON pl.id = c.programme_level_id AND pl.archived_at IS NULL
			JOIN programmes p ON p.id = pl.programme_id AND p.archived_at IS NULL
			JOIN user_allowed_units au ON au.id = p.academic_unit_id
			CROSS JOIN input i
			WHERE cm.user_id = i.user_id AND cm.archived_at IS NULL
			  AND cm.start_at <= i.active_at AND (cm.end_at IS NULL OR cm.end_at > i.active_at)
			UNION ALL
			SELECT 'academic_unit', au.root_id, 2
			FROM role_bindings rb
			JOIN roles r ON r.id = rb.role_id AND r.archived_at IS NULL
			JOIN user_allowed_units au ON rb.scope_type = 'academic_unit' AND au.id = rb.scope_id
			CROSS JOIN input i
			WHERE rb.user_id = i.user_id AND rb.archived_at IS NULL
			  AND rb.start_at <= i.active_at AND (rb.end_at IS NULL OR rb.end_at > i.active_at)
			UNION ALL
			SELECT 'academic_unit', au.root_id, 2
			FROM role_bindings rb
			JOIN roles r ON r.id = rb.role_id AND r.archived_at IS NULL
			JOIN classes c ON rb.scope_type = 'class' AND c.id = rb.scope_id AND c.archived_at IS NULL
			JOIN programme_levels pl ON pl.id = c.programme_level_id AND pl.archived_at IS NULL
			JOIN programmes p ON p.id = pl.programme_id AND p.archived_at IS NULL
			JOIN user_allowed_units au ON au.id = p.academic_unit_id
			CROSS JOIN input i
			WHERE rb.user_id = i.user_id AND rb.archived_at IS NULL
			  AND rb.start_at <= i.active_at AND (rb.end_at IS NULL OR rb.end_at > i.active_at)
		)
		SELECT matches.scope_type, matches.scope_id
		FROM matches CROSS JOIN input i
		JOIN users u ON u.id = i.user_id AND u.archived_at IS NULL AND u.disabled_at IS NULL
		ORDER BY matches.priority, matches.scope_id
		LIMIT 1`, userID, model.TimeFromMillis(visibility.ActiveAt),
		pq.Array(visibility.AcademicUnitRootIDs), pq.Array(visibility.ClassMemberAcademicUnitRootIDs),
		pq.Array(visibility.ClassIDs), visibility.ClassMemberInstitutionWide)
	if errors.Is(err, sql.ErrNoRows) {
		return store.UserVisibilityMatch{}, nil
	}
	if err != nil {
		return store.UserVisibilityMatch{}, fmt.Errorf("match user visibility: %w", err)
	}
	return store.UserVisibilityMatch{ScopeType: match.ScopeType, ScopeID: match.ScopeID}, nil
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
	return row.model()
}

func (s SQLUserStore) UpdateProfileWithAudit(ctx context.Context, input *store.UserProfileUpdate) (*model.User, error) {
	if input == nil || !input.UserID.IsValid() || input.ExpectedRevision <= 0 ||
		!model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("user", "profile_update", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "audited user profile update", func(ctx context.Context, tx *sqlxTxWrapper) (*model.User, error) {
		current, err := getUserByIDForUpdate(ctx, tx, input.UserID.String())
		if err != nil {
			return nil, err
		}
		if current.Revision != input.ExpectedRevision {
			return nil, store.NewErrConflict("user", "user_changed", nil)
		}
		candidate := *current
		candidate.ApplyProfileChanges(&input.Changes)
		candidate.PrepareUpdate(model.TimeFromMillis(input.AuditAt))
		candidate.Revision = input.ExpectedRevision + 1
		if err := candidate.Validate(); err != nil {
			return nil, store.NewErrInvalidInput("user", "value", nil).Wrap(err)
		}
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
		return updated, nil
	})
}

func getUserByIDForUpdate(ctx context.Context, executor sqlxExecutor, id string) (*model.User, error) {
	var row userRow
	if err := executor.Get(ctx, &row, `
		SELECT id, created_at, updated_at, archived_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at,
		       default_profile_picture_seed, default_profile_picture_file_id,
		       custom_profile_picture_file_id, profile_picture_changed_at
		  FROM users
		 WHERE id = ? AND archived_at IS NULL
		 FOR UPDATE`, id); err != nil {
		return nil, translateError("user", id, err)
	}
	return row.model()
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
	return row.model()
}

func updateUserProfile(ctx context.Context, executor sqlxExecutor, candidate *model.User, expectedRevision int64) error {
	row := newUserRow(candidate)
	row.ExpectedRevision = expectedRevision
	result, err := executor.NamedExec(ctx, `
		UPDATE users
		   SET updated_at = GREATEST(updated_at, :updated_at),
		       revision = :revision,
		       username = :username,
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
		input.ChangedAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 ||
		!validAccessDeploymentCapabilities(input.Capabilities) {
		return nil, store.NewErrInvalidInput("user", "disabled_state_change", nil)
	}
	revocationReason := model.SanitizeUnicode(input.RevocationReason)
	if input.Disabled && utf8.RuneCountInString(revocationReason) > model.SessionRevocationMaxRunes {
		return nil, store.NewErrInvalidInput("session", "revocation_reason", nil)
	}
	templateKey := model.MailTemplateIdentityAccountEnabled
	if input.Disabled {
		templateKey = model.MailTemplateIdentityAccountDisabled
	}
	mailUnprepared := input.Occurrence == nil && input.Delivery == nil && input.DeliveryJob == nil
	if mailUnprepared && input.Command == nil {
		return nil, store.NewErrInvalidInput("user", "disabled_state_notice", nil)
	}
	payloadKeyID := ""
	if !mailUnprepared {
		var err error
		payloadKeyID, err = validateSecurityNoticeMail(model.UserID(input.ID), input.Occurrence, input.Delivery, input.DeliveryJob, templateKey, input.ChangedAt)
		if err != nil {
			return nil, err
		}
	}
	execute := func(ctx context.Context, tx *sqlxTxWrapper) (*userDisabledMutationResult, error) {
		if payloadKeyID != "" {
			if err := requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
		}
		// Security-sensitive account disablement takes the installation-wide
		// authentication-path fence before any User row. User eligibility changes
		// then follow the shared User -> mail-eligibility singleton order.
		if input.Disabled {
			if err := lockSystemAdministratorAuthenticationPaths(ctx, tx); err != nil {
				return nil, err
			}
		}
		var lockedUserID string
		if err := tx.Get(ctx, &lockedUserID, `SELECT id FROM users WHERE id=? AND archived_at IS NULL FOR UPDATE`, input.ID); err != nil {
			return nil, translateError("user", input.ID, err)
		}
		currentUser, err := getUserByID(ctx, tx, input.ID)
		if err != nil {
			return nil, err
		}
		if currentUser.DisabledAt.Valid == input.Disabled {
			if input.Command == nil {
				return nil, store.NewErrConflict("user", "disabled_state", nil)
			}
			if err = completeAdministrativeNoOpAudit(ctx, tx, input.AuditEventID, input.AuditAt, "user_id", currentUser.ID.String()); err != nil {
				return nil, err
			}
			return &userDisabledMutationResult{Value: &store.UserDisabledStateResult{User: currentUser, RevokedSessions: []*model.Session{}, RevokedTokenHashes: []string{}}, NoOp: true}, nil
		}
		if mailUnprepared {
			return nil, store.NewErrInvalidInput("user", "disabled_state_notice", nil)
		}
		// Serialize disabling with login and refresh rotation before changing the
		// user row. A login that commits first is included in the revocation; one
		// that follows observes the disabled account.
		if input.Disabled {
			policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
			if err != nil {
				return nil, err
			}
			at := model.TimeFromMillis(input.ChangedAt)
			administrator, err := isActiveSystemAdministrator(ctx, tx, input.ID, at)
			if err != nil {
				return nil, err
			}
			if administrator {
				remaining, pathErr := hasUsableSystemAdministratorAuthenticationPath(
					ctx, tx, policy.Settings(), input.Capabilities, at,
					systemAdministratorAuthenticationPathScope{ExcludedUserID: input.ID},
				)
				if pathErr != nil {
					return nil, pathErr
				}
				if !remaining {
					return nil, store.NewErrConflict("user", "users_last_system_admin", nil)
				}
			}
			if err := lockUserSessions(ctx, tx, input.ID); err != nil {
				return nil, err
			}
		}
		disabledAt := int64(0)
		if input.Disabled {
			disabledAt = input.ChangedAt
		}
		mailEligibilityRevision, err := advanceUserMailEligibilityRevision(ctx, tx)
		if err != nil {
			return nil, err
		}
		row, err := setUserDisabled(
			ctx,
			tx,
			input.ID,
			input.ExpectedRevision,
			disabledAt,
			input.ChangedAt,
			mailEligibilityRevision,
		)
		if err != nil {
			return nil, err
		}

		user, err := row.model()
		if err != nil {
			return nil, err
		}
		result := &store.UserDisabledStateResult{
			User:               user,
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
			result.RevokedSessions, err = revokedSessionModels(
				sessionRows,
				input.ChangedAt,
				revocationReason,
			)
			if err != nil {
				return nil, err
			}
			result.RevokedTokenHashes = hashes
		}
		if err := insertSecurityNoticeMail(ctx, tx, input.Occurrence, input.Delivery, input.DeliveryJob, payloadKeyID); err != nil {
			return nil, err
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
		return &userDisabledMutationResult{Value: result}, nil
	}
	if input.Command == nil {
		result, err := runSQLTransaction(ctx, s.GetMaster().Begin, "audited user disabled state change", execute)
		if err != nil {
			return nil, err
		}
		input.NoOp = result.NoOp
		return result.Value, nil
	}
	result, err := runIdempotentMutation(ctx, s.SQLStore, "idempotent user disabled state change", idempotentMutation[*userDisabledMutationResult]{
		command: input.Command, auditEventID: input.AuditEventID, execute: execute,
		encode: encodeUserDisabledMutationOutcome, decode: decodeUserDisabledMutationOutcome,
		onboardingOutcome: func(value *userDisabledMutationResult) (onboardingImportCommandResult, error) {
			return administrativeOnboardingOutcome(value.Value.User.ID.String(), value.NoOp)
		},
		hydrateReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *userDisabledMutationResult) (*userDisabledMutationResult, error) {
			user, hydrateErr := getUserByID(ctx, tx, value.Value.User.ID.String())
			if hydrateErr != nil {
				return nil, hydrateErr
			}
			value.Value = &store.UserDisabledStateResult{User: user, RevokedSessions: []*model.Session{}, RevokedTokenHashes: []string{}}
			return value, nil
		},
		completeReplay: func(ctx context.Context, tx *sqlxTxWrapper, value *userDisabledMutationResult, original string) error {
			return completeAdministrativeReplayAudit(ctx, tx, input.AuditEventID, input.AuditAt, "user_id", value.Value.User.ID.String(), value.NoOp, original)
		},
	})
	if err != nil {
		return nil, err
	}
	input.Replayed, input.NoOp = result.Replayed, result.Value.NoOp
	return result.Value.Value, nil
}

type userDisabledMutationResult struct {
	Value *store.UserDisabledStateResult
	NoOp  bool
}

type userDisabledCommandOutcome struct {
	UserID string `json:"user_id"`
	NoOp   bool   `json:"no_op,omitempty"`
}

func encodeUserDisabledMutationOutcome(value *userDisabledMutationResult) ([]byte, error) {
	if value == nil || value.Value == nil || value.Value.User == nil || !value.Value.User.ID.IsValid() {
		return nil, store.NewErrInvalidInput("user", "command_outcome", nil)
	}
	return encodeCommandOutcome(userDisabledCommandOutcome{UserID: value.Value.User.ID.String(), NoOp: value.NoOp})
}
func decodeUserDisabledMutationOutcome(version int, data []byte) (*userDisabledMutationResult, error) {
	if version != 1 {
		return nil, fmt.Errorf("unsupported user disabled outcome version %d", version)
	}
	var outcome userDisabledCommandOutcome
	if err := decodeCommandOutcome(data, &outcome); err != nil {
		return nil, err
	}
	id, err := model.ParseUserID(outcome.UserID)
	if err != nil {
		return nil, invalidPersistedState("command_outcome", "user_id", err)
	}
	return &userDisabledMutationResult{Value: &store.UserDisabledStateResult{User: &model.User{ID: id}}, NoOp: outcome.NoOp}, nil
}

func setUserDisabled(
	ctx context.Context,
	tx *sqlxTxWrapper,
	id string,
	expectedRevision int64,
	disabledAt int64,
	updateAt int64,
	mailEligibilityRevision int64,
) (*userRow, error) {
	if expectedRevision <= 0 || updateAt <= 0 || disabledAt < 0 ||
		(disabledAt != 0 && disabledAt != updateAt) || mailEligibilityRevision < 1 {
		return nil, store.NewErrInvalidInput("user", "disabled_at", disabledAt)
	}
	updateTime := model.TimeFromMillis(updateAt)
	disabledTime := sql.NullTime{}
	if disabledAt != 0 {
		disabledTime = sql.NullTime{Time: updateTime, Valid: true}
	}
	result, err := tx.Exec(ctx, `
		UPDATE users
		   SET updated_at = ?, disabled_at = ?, mail_eligibility_revision = ?, revision = revision + 1
		 WHERE id = ? AND archived_at IS NULL AND revision = ?
		   AND ((? AND disabled_at IS NULL) OR (NOT ? AND disabled_at IS NOT NULL))`,
		updateTime, disabledTime, mailEligibilityRevision, id, expectedRevision, disabledAt != 0, disabledAt != 0,
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

func (row userRow) model() (*model.User, error) {
	id, err := parsePersistedID("user", "id", row.ID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	defaultPictureID, err := parseNullablePersistedID("user", "default_profile_picture_file_id", row.DefaultProfilePictureFileID, model.ParseFileEntryID)
	if err != nil {
		return nil, err
	}
	customPictureID, err := parseNullablePersistedID("user", "custom_profile_picture_file_id", row.CustomProfilePictureFileID, model.ParseFileEntryID)
	if err != nil {
		return nil, err
	}
	value := &model.User{
		ID:                          id,
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
		DefaultProfilePictureFileID: defaultPictureID,
		CustomProfilePictureFileID:  customPictureID,
		ProfilePictureChangedAt:     OptionalTimeFromNullTime(row.ProfilePictureChangedAt),
	}
	if err := validatePersistedModel("user", value); err != nil {
		return nil, err
	}
	return value, nil
}

func nullableID(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

var _ store.UserStore = (*SQLUserStore)(nil)
