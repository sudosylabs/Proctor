// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLExternalIdentityStore struct {
	*SQLStore
	identitiesQuery sq.SelectBuilder
}

type externalIdentityRow struct {
	ID         string       `db:"id"`
	CreatedAt  time.Time    `db:"created_at"`
	UpdatedAt  time.Time    `db:"updated_at"`
	ArchivedAt sql.NullTime `db:"archived_at"`
	UserID     string       `db:"user_id"`
	Provider   string       `db:"provider"`
	Subject    string       `db:"subject"`
	LastSeenAt sql.NullTime `db:"last_seen_at"`
}

func externalIdentitySliceColumns() []string {
	return []string{
		"external_identities.id",
		"external_identities.created_at",
		"external_identities.updated_at",
		"external_identities.archived_at",
		"external_identities.user_id",
		"external_identities.provider",
		"external_identities.subject",
		"external_identities.last_seen_at",
	}
}

func newSQLExternalIdentityStore(sqlStore *SQLStore) store.ExternalIdentityStore {
	s := &SQLExternalIdentityStore{SQLStore: sqlStore}
	s.identitiesQuery = s.getQueryBuilder().
		Select(externalIdentitySliceColumns()...).
		From("external_identities")
	return s
}

func (s SQLExternalIdentityStore) Save(
	ctx context.Context,
	identity *model.ExternalIdentity,
) (*model.ExternalIdentity, error) {
	if identity == nil {
		return nil, store.NewErrInvalidInput("external_identity", "value", nil)
	}
	if !identity.ID.IsZero() {
		return nil, store.NewErrInvalidInput("external_identity", "id", identity.ID.String())
	}
	candidate := *identity
	candidate.PrepareCreate(model.NewExternalIdentityID(), model.NowUTC())
	if err := candidate.Validate(); err != nil {
		return nil, err
	}
	if err := insertExternalIdentity(ctx, s.GetMaster(), &candidate); err != nil {
		return nil, err
	}
	return &candidate, nil
}

func (s SQLExternalIdentityStore) Get(
	ctx context.Context,
	id string,
) (*model.ExternalIdentity, error) {
	query := s.identitiesQuery.Where(sq.Eq{
		"external_identities.id":          id,
		"external_identities.archived_at": nil,
	})
	return s.get(ctx, query, id)
}

func (s SQLExternalIdentityStore) GetByProviderSubject(
	ctx context.Context,
	provider string,
	subject string,
) (*model.ExternalIdentity, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	query := s.identitiesQuery.Where(sq.Eq{
		"external_identities.provider":    provider,
		"external_identities.subject":     subject,
		"external_identities.archived_at": nil,
	})
	return s.get(ctx, query, provider)
}

func (s SQLExternalIdentityStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.ExternalIdentity, error) {
	query := s.identitiesQuery.
		Where(sq.Eq{
			"external_identities.user_id":     userID,
			"external_identities.archived_at": nil,
		}).
		OrderBy("external_identities.provider", "external_identities.id")
	rows := []externalIdentityRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list external identities by user: %w", err)
	}
	identities := make([]*model.ExternalIdentity, 0, len(rows))
	for _, row := range rows {
		identity, err := row.model()
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

func (s SQLExternalIdentityStore) ResolveOrProvision(
	ctx context.Context,
	request *store.ExternalIdentityResolutionRequest,
) (*store.ExternalIdentityResolution, error) {
	if request == nil {
		return nil, store.NewErrInvalidInput("external_identity", "resolution", nil)
	}
	identity := request.Identity
	if identity == nil || !identity.ID.IsZero() ||
		identity.Provider == "" || identity.Subject == "" ||
		!identity.LastSeenAt.Valid || !validAccessDeploymentCapabilities(request.Capabilities) {
		return nil, store.NewErrInvalidInput("external_identity", "resolution", nil)
	}
	provider := strings.ToLower(strings.TrimSpace(identity.Provider))
	capability, configured := request.Capabilities.Providers[provider]
	if !configured {
		return nil, store.ErrAuthenticationMethodDisabled
	}
	lastSeenAt := identity.LastSeenAt.Millis()
	return runSQLTransaction(ctx, s.GetMaster().Begin, "external identity resolution", func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExternalIdentityResolution, error) {
		policy, err := getAccessPolicy(ctx, tx, "FOR SHARE")
		if err != nil {
			return nil, err
		}
		admission, allowed := policy.ProviderAdmissions[provider]
		if !allowed {
			return nil, store.ErrAuthenticationMethodDisabled
		}
		if err := lockExternalIdentitySubject(ctx, tx, provider, identity.Subject); err != nil {
			return nil, err
		}

		resolvedIdentity, resolvedUser, err := resolveExternalIdentity(
			ctx,
			tx,
			provider,
			identity.Subject,
			lastSeenAt,
		)
		if err == nil {
			return &store.ExternalIdentityResolution{
				Identity: resolvedIdentity,
				User:     resolvedUser,
			}, nil
		}
		if !store.IsNotFound(err) {
			return nil, err
		}
		switch admission {
		case model.ProviderAdmissionAutoProvision:
			if !capability.AutoProvision {
				return nil, store.ErrAuthenticationMethodDisabled
			}
		case model.ProviderAdmissionInvitationRequired:
			return nil, store.NewErrNotFound("external_identity", provider)
		case model.ProviderAdmissionLinkedOnly:
			return nil, store.NewErrNotFound("external_identity", provider)
		default:
			return nil, store.ErrAuthenticationMethodDisabled
		}
		if request.User == nil || request.Settings == nil || request.ProvisionAudit == nil ||
			!request.ProvisionAudit.ID.IsZero() || request.DefaultProfilePictureJob == nil {
			return nil, store.NewErrInvalidInput("external_identity", "provisioning", nil)
		}
		at := model.TimeFromMillis(lastSeenAt)
		if at.IsZero() {
			at = model.NowUTC()
		}
		userCandidate := *request.User
		if err := userCandidate.Validate(); err != nil {
			return nil, err
		}
		if err := validateUserDefaultProfilePictureJob(&userCandidate, request.DefaultProfilePictureJob); err != nil {
			return nil, err
		}
		settingsCandidate := request.Settings.Clone()
		if err := validateInitialUserSettingsDocument(&userCandidate, settingsCandidate); err != nil {
			return nil, err
		}
		identityCandidate := *identity
		identityCandidate.Provider = provider
		identityCandidate.UserID = userCandidate.ID
		identityCandidate.PrepareCreate(model.NewExternalIdentityID(), at)
		if err := identityCandidate.Validate(); err != nil {
			return nil, err
		}
		if err := insertUser(ctx, tx, &userCandidate); err != nil {
			return nil, err
		}
		if err := insertUserSettingsDocument(ctx, tx, settingsCandidate); err != nil {
			return nil, err
		}
		if err := insertExternalIdentity(ctx, tx, &identityCandidate); err != nil {
			return nil, err
		}
		if _, err := insertQueuedJob(ctx, tx, request.DefaultProfilePictureJob, false); err != nil {
			return nil, fmt.Errorf("enqueue external user default profile picture generation: %w", translateError("job", request.DefaultProfilePictureJob.ID.String(), err))
		}
		auditCandidate := request.ProvisionAudit.Clone()
		auditCandidate.ActorID = userCandidate.ID
		auditCandidate.Resource = model.Resource{
			Type: model.ResourceUser,
			ID:   userCandidate.ID.String(),
		}
		if _, err := insertAuditEvent(ctx, tx, auditCandidate); err != nil {
			return nil, err
		}
		return &store.ExternalIdentityResolution{
			Identity:    &identityCandidate,
			User:        &userCandidate,
			Provisioned: true,
		}, nil
	})
}

func lockExternalIdentitySubject(ctx context.Context, tx *sqlxTxWrapper, provider, subject string) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
		strconv.Itoa(len(provider))+":"+provider+subject); err != nil {
		return fmt.Errorf("lock external identity resolution: %w", err)
	}
	return nil
}

func resolveExternalIdentity(
	ctx context.Context,
	tx *sqlxTxWrapper,
	provider string,
	subject string,
	lastSeenAt int64,
) (*model.ExternalIdentity, *model.User, error) {
	at := model.TimeFromMillis(lastSeenAt)
	var identityRow externalIdentityRow
	err := tx.Get(ctx, &identityRow, `
		SELECT id, created_at, updated_at, archived_at, user_id, provider, subject, last_seen_at
		  FROM external_identities
		 WHERE provider = ? AND subject = ? AND archived_at IS NULL
		 FOR UPDATE`,
		provider,
		subject,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, store.NewErrNotFound("external_identity", provider)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("resolve external identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE external_identities
		   SET updated_at = GREATEST(updated_at, ?),
		       last_seen_at = GREATEST(last_seen_at, ?)
		 WHERE id = ? AND archived_at IS NULL`,
		at,
		at,
		identityRow.ID,
	); err != nil {
		return nil, nil, fmt.Errorf("update external identity last seen: %w", err)
	}
	if identityRow.UpdatedAt.Before(at) {
		identityRow.UpdatedAt = at
	}
	if !identityRow.LastSeenAt.Valid || identityRow.LastSeenAt.Time.Before(at) {
		identityRow.LastSeenAt = sql.NullTime{Time: at, Valid: true}
	}

	var row userRow
	if err := tx.Get(ctx, &row, `
		SELECT id, created_at, updated_at, archived_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		       , default_profile_picture_seed, default_profile_picture_file_id,
		       custom_profile_picture_file_id, profile_picture_changed_at
		  FROM users
		 WHERE id = ? AND archived_at IS NULL`,
		identityRow.UserID,
	); err != nil {
		return nil, nil, translateError("user", identityRow.UserID, err)
	}
	user, err := row.model()
	if err != nil {
		return nil, nil, err
	}
	identity, err := identityRow.model()
	if err != nil {
		return nil, nil, err
	}
	return identity, user, nil
}

func insertExternalIdentity(
	ctx context.Context,
	executor sqlxExecutor,
	identity *model.ExternalIdentity,
) error {
	row := newExternalIdentityRow(identity)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO external_identities (
			id, created_at, updated_at, archived_at, user_id, provider, subject,
			last_seen_at
		) VALUES (
			:id, :created_at, :updated_at, :archived_at, :user_id, :provider,
			:subject, :last_seen_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save external identity: %w",
			translateError("external_identity", identity.ID.String(), err),
		)
	}
	return nil
}

func (s SQLExternalIdentityStore) get(
	ctx context.Context,
	query sq.SelectBuilder,
	key string,
) (*model.ExternalIdentity, error) {
	var row externalIdentityRow
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("external_identity", key, err)
	}
	return row.model()
}

func newExternalIdentityRow(identity *model.ExternalIdentity) externalIdentityRow {
	return externalIdentityRow{
		ID:         identity.ID.String(),
		CreatedAt:  UTCTime(identity.CreatedAt),
		UpdatedAt:  UTCTime(identity.UpdatedAt),
		ArchivedAt: NullTimeFromOptional(identity.ArchivedAt),
		UserID:     identity.UserID.String(),
		Provider:   identity.Provider,
		Subject:    identity.Subject,
		LastSeenAt: NullTimeFromOptional(identity.LastSeenAt),
	}
}

func (row externalIdentityRow) model() (*model.ExternalIdentity, error) {
	id, err := parsePersistedID("external_identity", "id", row.ID, model.ParseExternalIdentityID)
	if err != nil {
		return nil, err
	}
	userID, err := parsePersistedID("external_identity", "user_id", row.UserID, model.ParseUserID)
	if err != nil {
		return nil, err
	}
	value := &model.ExternalIdentity{
		ID:         id,
		CreatedAt:  row.CreatedAt.UTC(),
		UpdatedAt:  row.UpdatedAt.UTC(),
		ArchivedAt: OptionalTimeFromNullTime(row.ArchivedAt),
		UserID:     userID,
		Provider:   row.Provider,
		Subject:    row.Subject,
		LastSeenAt: OptionalTimeFromNullTime(row.LastSeenAt),
	}
	if err := validatePersistedModel("external_identity", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.ExternalIdentityStore = (*SQLExternalIdentityStore)(nil)
