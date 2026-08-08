// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlExternalIdentityStore struct {
	*SqlStore
	identitiesQuery sq.SelectBuilder
}

type externalIdentityRow struct {
	ID         string `db:"id"`
	CreateAt   int64  `db:"create_at"`
	UpdateAt   int64  `db:"update_at"`
	DeleteAt   int64  `db:"delete_at"`
	UserID     string `db:"user_id"`
	Provider   string `db:"provider"`
	Subject    string `db:"subject"`
	LastSeenAt int64  `db:"last_seen_at"`
}

func externalIdentitySliceColumns() []string {
	return []string{
		"external_identities.id",
		"external_identities.create_at",
		"external_identities.update_at",
		"external_identities.delete_at",
		"external_identities.user_id",
		"external_identities.provider",
		"external_identities.subject",
		"external_identities.last_seen_at",
	}
}

func newSqlExternalIdentityStore(sqlStore *SqlStore) store.ExternalIdentityStore {
	s := &SqlExternalIdentityStore{SqlStore: sqlStore}
	s.identitiesQuery = s.getQueryBuilder().
		Select(externalIdentitySliceColumns()...).
		From("external_identities")
	return s
}

func (s SqlExternalIdentityStore) Save(
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

func (s SqlExternalIdentityStore) Get(
	ctx context.Context,
	id string,
) (*model.ExternalIdentity, error) {
	query := s.identitiesQuery.Where(sq.Eq{
		"external_identities.id":        id,
		"external_identities.delete_at": int64(0),
	})
	return s.get(ctx, query, id)
}

func (s SqlExternalIdentityStore) GetByProviderSubject(
	ctx context.Context,
	provider string,
	subject string,
) (*model.ExternalIdentity, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	query := s.identitiesQuery.Where(sq.Eq{
		"external_identities.provider":  provider,
		"external_identities.subject":   subject,
		"external_identities.delete_at": int64(0),
	})
	return s.get(ctx, query, provider)
}

func (s SqlExternalIdentityStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.ExternalIdentity, error) {
	query := s.identitiesQuery.
		Where(sq.Eq{
			"external_identities.user_id":   userID,
			"external_identities.delete_at": int64(0),
		}).
		OrderBy("external_identities.provider", "external_identities.id")
	rows := []externalIdentityRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list external identities by user: %w", err)
	}
	identities := make([]*model.ExternalIdentity, 0, len(rows))
	for _, row := range rows {
		identities = append(identities, row.model())
	}
	return identities, nil
}

func (s SqlExternalIdentityStore) ResolveOrProvision(
	ctx context.Context,
	identity *model.ExternalIdentity,
	user *model.User,
	autoProvision bool,
	provisionAudit *model.AuditEvent,
) (*store.ExternalIdentityResolution, error) {
	if identity == nil || !identity.ID.IsZero() ||
		identity.Provider == "" || identity.Subject == "" ||
		!identity.LastSeenAt.Valid {
		return nil, store.NewErrInvalidInput("external_identity", "resolution", nil)
	}
	provider := strings.ToLower(strings.TrimSpace(identity.Provider))
	lastSeenAt := identity.LastSeenAt.Millis()
	tx, err := s.GetMaster().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin external identity resolution: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
		strconv.Itoa(len(provider))+":"+provider+identity.Subject,
	); err != nil {
		return nil, fmt.Errorf("lock external identity resolution: %w", err)
	}

	resolvedIdentity, resolvedUser, err := resolveExternalIdentity(
		ctx,
		tx,
		provider,
		identity.Subject,
		lastSeenAt,
	)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit external identity resolution: %w", err)
		}
		return &store.ExternalIdentityResolution{
			Identity: resolvedIdentity,
			User:     resolvedUser,
		}, nil
	}
	if !store.IsNotFound(err) {
		return nil, err
	}
	if !autoProvision {
		return nil, store.NewErrNotFound("external_identity", provider)
	}
	if user == nil || !user.ID.IsZero() || provisionAudit == nil ||
		!provisionAudit.ID.IsZero() {
		return nil, store.NewErrInvalidInput("external_identity", "provisioning", nil)
	}

	at := model.TimeFromMillis(lastSeenAt)
	if at.IsZero() {
		at = model.NowUTC()
	}
	userCandidate := *user
	userCandidate.PrepareCreate(model.NewUserID(), at)
	if err := userCandidate.Validate(); err != nil {
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
	if err := insertExternalIdentity(ctx, tx, &identityCandidate); err != nil {
		return nil, err
	}
	auditCandidate := provisionAudit.Clone()
	auditCandidate.ActorID = userCandidate.ID
	auditCandidate.Resource = model.Resource{
		Type: model.ResourceUser,
		Id:   userCandidate.ID.String(),
	}
	if _, err := insertAuditEvent(ctx, tx, auditCandidate); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit external identity provisioning: %w", err)
	}
	return &store.ExternalIdentityResolution{
		Identity:    &identityCandidate,
		User:        &userCandidate,
		Provisioned: true,
	}, nil
}

func resolveExternalIdentity(
	ctx context.Context,
	tx *sqlxTxWrapper,
	provider string,
	subject string,
	lastSeenAt int64,
) (*model.ExternalIdentity, *model.User, error) {
	var identityRow externalIdentityRow
	err := tx.Get(ctx, &identityRow, `
		SELECT id, create_at, update_at, delete_at, user_id, provider, subject, last_seen_at
		  FROM external_identities
		 WHERE provider = ? AND subject = ? AND delete_at = 0
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
		   SET update_at = GREATEST(update_at, ?),
		       last_seen_at = GREATEST(last_seen_at, ?)
		 WHERE id = ? AND delete_at = 0`,
		lastSeenAt,
		lastSeenAt,
		identityRow.ID,
	); err != nil {
		return nil, nil, fmt.Errorf("update external identity last seen: %w", err)
	}
	identityRow.UpdateAt = max(identityRow.UpdateAt, lastSeenAt)
	identityRow.LastSeenAt = max(identityRow.LastSeenAt, lastSeenAt)

	var row userRow
	if err := tx.Get(ctx, &row, `
		SELECT id, create_at, update_at, delete_at, revision, username, email,
		       email_verified, display_name, first_name, last_name, locale,
		       timezone, last_login_at, last_activity_at, disabled_at
		  FROM users
		 WHERE id = ? AND delete_at = 0`,
		identityRow.UserID,
	); err != nil {
		return nil, nil, translateError("user", identityRow.UserID, err)
	}
	return identityRow.model(), row.model(), nil
}

func insertExternalIdentity(
	ctx context.Context,
	executor sqlxExecutor,
	identity *model.ExternalIdentity,
) error {
	row := newExternalIdentityRow(identity)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO external_identities (
			id, create_at, update_at, delete_at, user_id, provider, subject,
			last_seen_at
		) VALUES (
			:id, :create_at, :update_at, :delete_at, :user_id, :provider,
			:subject, :last_seen_at
		)`, &row); err != nil {
		return fmt.Errorf(
			"save external identity: %w",
			translateError("external_identity", identity.ID.String(), err),
		)
	}
	return nil
}

func (s SqlExternalIdentityStore) get(
	ctx context.Context,
	query sq.SelectBuilder,
	key string,
) (*model.ExternalIdentity, error) {
	var row externalIdentityRow
	if err := s.GetMaster().GetBuilder(ctx, &row, query); err != nil {
		return nil, translateError("external_identity", key, err)
	}
	return row.model(), nil
}

func newExternalIdentityRow(identity *model.ExternalIdentity) externalIdentityRow {
	return externalIdentityRow{
		ID:         identity.ID.String(),
		CreateAt:   model.MillisFromTime(identity.CreatedAt),
		UpdateAt:   model.MillisFromTime(identity.UpdatedAt),
		DeleteAt:   identity.ArchivedAt.Millis(),
		UserID:     identity.UserID.String(),
		Provider:   identity.Provider,
		Subject:    identity.Subject,
		LastSeenAt: identity.LastSeenAt.Millis(),
	}
}

func (row externalIdentityRow) model() *model.ExternalIdentity {
	return &model.ExternalIdentity{
		ID:         model.ExternalIdentityID(row.ID),
		CreatedAt:  model.TimeFromMillis(row.CreateAt),
		UpdatedAt:  model.TimeFromMillis(row.UpdateAt),
		ArchivedAt: model.OptionalTimeFromMillis(row.DeleteAt),
		UserID:     model.UserID(row.UserID),
		Provider:   row.Provider,
		Subject:    row.Subject,
		LastSeenAt: model.OptionalTimeFromMillis(row.LastSeenAt),
	}
}

var _ store.ExternalIdentityStore = (*SqlExternalIdentityStore)(nil)
