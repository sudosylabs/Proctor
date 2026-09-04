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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SQLDesktopRegistrationStore struct {
	*SQLStore
	query sq.SelectBuilder
}

type desktopRegistrationRow struct {
	ID               string       `db:"id"`
	CreatedAt        time.Time    `db:"created_at"`
	UpdatedAt        time.Time    `db:"updated_at"`
	UserID           string       `db:"user_id"`
	InstitutionID    string       `db:"institution_id"`
	PublicJWK        []byte       `db:"public_jwk"`
	KeyThumbprint    string       `db:"key_thumbprint"`
	DisplayName      string       `db:"display_name"`
	DesktopRelease   string       `db:"desktop_release"`
	DesktopBuildID   string       `db:"desktop_build_id"`
	Platform         string       `db:"platform"`
	Architecture     string       `db:"architecture"`
	RealtimeProtocol int          `db:"realtime_protocol"`
	LastUsedAt       time.Time    `db:"last_used_at"`
	RevokedAt        sql.NullTime `db:"revoked_at"`
}

func desktopRegistrationColumns() []string {
	return []string{
		"id", "created_at", "updated_at", "user_id", "institution_id",
		"public_jwk", "key_thumbprint", "display_name", "desktop_release",
		"desktop_build_id", "platform", "architecture", "realtime_protocol",
		"last_used_at", "revoked_at",
	}
}

func newSQLDesktopRegistrationStore(sqlStore *SQLStore) store.DesktopRegistrationStore {
	value := &SQLDesktopRegistrationStore{SQLStore: sqlStore}
	value.query = value.getQueryBuilder().Select(desktopRegistrationColumns()...).From("desktop_registrations")
	return value
}

func (s SQLDesktopRegistrationStore) Get(ctx context.Context, id string) (*model.DesktopRegistration, error) {
	var row desktopRegistrationRow
	if err := s.GetMaster().GetBuilder(ctx, &row, s.query.Where(sq.Eq{"id": id})); err != nil {
		return nil, translateError("desktop_registration", id, err)
	}
	return row.model()
}

func (s SQLDesktopRegistrationStore) ListByUser(
	ctx context.Context,
	userID string,
) ([]*model.DesktopRegistration, error) {
	if !model.IsValidId(userID) {
		return nil, store.NewErrInvalidInput("desktop_registration", "user_id", userID)
	}
	rows := []desktopRegistrationRow{}
	query := s.query.Where(sq.Eq{"user_id": userID}).
		OrderBy("last_used_at DESC", "created_at DESC", "id")
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list Desktop Registrations: %w", err)
	}
	result := make([]*model.DesktopRegistration, 0, len(rows))
	for _, row := range rows {
		registration, err := row.model()
		if err != nil {
			return nil, err
		}
		result = append(result, registration)
	}
	return result, nil
}

func getOrCreateDesktopRegistration(
	ctx context.Context,
	tx *sqlxTxWrapper,
	transaction *model.BrowserAuthenticationTransaction,
	now time.Time,
) (*model.DesktopRegistration, error) {
	var row desktopRegistrationRow
	err := tx.Get(ctx, &row, `SELECT `+strings.Join(desktopRegistrationColumns(), ",")+`
	  FROM desktop_registrations
	 WHERE user_id=? AND institution_id=? AND key_thumbprint=?
	 FOR UPDATE`, transaction.UserID.String(), transaction.InstitutionID.String(), transaction.ProposedKeyThumbprint)
	if err == nil {
		registration, modelErr := row.model()
		if modelErr != nil {
			return nil, modelErr
		}
		if registration.RevokedAt.Valid || registration.PublicJWK != transaction.ProposedPublicJWK {
			return nil, store.NewErrConflict("desktop_registration", "desktop_registration_revoked", nil)
		}
		registration.DisplayName = transaction.DeviceName
		registration.DesktopRelease = transaction.DesktopRelease
		registration.DesktopBuildID = transaction.DesktopBuildID
		registration.Platform = transaction.DesktopPlatform
		registration.Architecture = transaction.DesktopArchitecture
		registration.RealtimeProtocol = transaction.DesktopRealtimeProtocol
		registration.LastUsedAt = now
		registration.UpdatedAt = now
		if registration.Validate() != nil {
			return nil, store.NewErrInvalidInput("desktop_registration", "metadata", nil)
		}
		if _, err = tx.Exec(ctx, `UPDATE desktop_registrations
		   SET updated_at=?,display_name=?,desktop_release=?,desktop_build_id=?,platform=?,architecture=?,
		       realtime_protocol=?,last_used_at=?
		 WHERE id=? AND revoked_at IS NULL`, now, registration.DisplayName, registration.DesktopRelease,
			registration.DesktopBuildID, registration.Platform, registration.Architecture,
			registration.RealtimeProtocol, now, registration.ID.String()); err != nil {
			return nil, translateError("desktop_registration", registration.ID.String(), err)
		}
		return registration, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, translateError("desktop_registration", transaction.ProposedKeyThumbprint, err)
	}
	candidate := &model.DesktopRegistration{
		UserID: transaction.UserID, InstitutionID: transaction.InstitutionID,
		PublicJWK: transaction.ProposedPublicJWK, KeyThumbprint: transaction.ProposedKeyThumbprint,
		DisplayName: transaction.DeviceName, DesktopRelease: transaction.DesktopRelease,
		DesktopBuildID: transaction.DesktopBuildID, Platform: transaction.DesktopPlatform,
		Architecture: transaction.DesktopArchitecture, RealtimeProtocol: transaction.DesktopRealtimeProtocol,
	}
	candidate.PrepareCreate(model.NewDesktopRegistrationID(), now)
	if err = candidate.Validate(); err != nil {
		return nil, err
	}
	encodedJWK, err := json.Marshal(candidate.PublicJWK)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO desktop_registrations (
		id,created_at,updated_at,user_id,institution_id,public_jwk,key_thumbprint,display_name,
		desktop_release,desktop_build_id,platform,architecture,realtime_protocol,last_used_at,revoked_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL)`,
		candidate.ID.String(), now, now, candidate.UserID.String(), candidate.InstitutionID.String(),
		string(encodedJWK), candidate.KeyThumbprint, candidate.DisplayName, candidate.DesktopRelease,
		candidate.DesktopBuildID, candidate.Platform, candidate.Architecture,
		candidate.RealtimeProtocol, now); err != nil {
		return nil, translateError("desktop_registration", candidate.ID.String(), err)
	}
	return candidate, nil
}

func (s SQLDesktopRegistrationStore) RevokeWithAudit(
	ctx context.Context,
	input *store.DesktopRegistrationRevocation,
) (*store.DesktopRegistrationRevocationResult, error) {
	if input == nil || !input.RegistrationID.IsValid() || !input.UserID.IsValid() ||
		input.RevokedAt <= 0 || !model.IsValidId(input.AuditEventID) || input.AuditAt <= 0 {
		return nil, store.NewErrInvalidInput("desktop_registration", "revocation", nil)
	}
	return runSQLTransaction(ctx, s.GetMaster().Begin, "revoke Desktop Registration", func(
		ctx context.Context,
		tx *sqlxTxWrapper,
	) (*store.DesktopRegistrationRevocationResult, error) {
		if err := lockUserSessions(ctx, tx, input.UserID.String()); err != nil {
			return nil, err
		}
		var row desktopRegistrationRow
		if err := tx.Get(ctx, &row, `SELECT `+strings.Join(desktopRegistrationColumns(), ",")+`
		  FROM desktop_registrations WHERE id=? AND user_id=? FOR UPDATE`,
			input.RegistrationID.String(), input.UserID.String()); err != nil {
			return nil, translateError("desktop_registration", input.RegistrationID.String(), err)
		}
		registration, err := row.model()
		if err != nil {
			return nil, err
		}
		at := model.TimeFromMillis(input.RevokedAt)
		alreadyRevoked := registration.RevokedAt.Valid
		sessionRows := []sessionRow{}
		hashes := []string{}
		if !alreadyRevoked {
			if err = tx.Select(ctx, &sessionRows, `SELECT `+strings.Join(sessionSliceColumns(), ",")+`
			  FROM sessions WHERE desktop_registration_id=? AND user_id=? AND archived_at IS NULL
			    AND revoked_at IS NULL FOR UPDATE`, input.RegistrationID.String(), input.UserID.String()); err != nil {
				return nil, fmt.Errorf("select Desktop Registration Sessions: %w", err)
			}
			if err = tx.Select(ctx, &hashes, `SELECT credential.token_hash
			  FROM session_credentials credential
			  JOIN sessions session ON session.id=credential.session_id
			 WHERE session.desktop_registration_id=? AND session.user_id=?
			   AND credential.archived_at IS NULL AND credential.revoked_at IS NULL
			 FOR UPDATE OF credential`, input.RegistrationID.String(), input.UserID.String()); err != nil {
				return nil, fmt.Errorf("select Desktop Registration credentials: %w", err)
			}
			if _, err = tx.Exec(ctx, `UPDATE session_credentials credential
			   SET updated_at=GREATEST(credential.updated_at,?),revoked_at=?
			  FROM sessions session
			 WHERE session.id=credential.session_id AND session.desktop_registration_id=? AND session.user_id=?
			   AND credential.archived_at IS NULL AND credential.revoked_at IS NULL`,
				at, at, input.RegistrationID.String(), input.UserID.String()); err != nil {
				return nil, fmt.Errorf("revoke Desktop Registration credentials: %w", err)
			}
			if _, err = tx.Exec(ctx, `UPDATE sessions SET updated_at=GREATEST(updated_at,?),revoked_at=?,
				revocation_reason=? WHERE desktop_registration_id=? AND user_id=? AND archived_at IS NULL AND revoked_at IS NULL`,
				at, at, model.SessionRevocationDesktopRegistration, input.RegistrationID.String(), input.UserID.String()); err != nil {
				return nil, fmt.Errorf("revoke Desktop Registration Sessions: %w", err)
			}
			if _, err = tx.Exec(ctx, `UPDATE desktop_registrations SET updated_at=GREATEST(updated_at,?),
				revoked_at=? WHERE id=? AND user_id=? AND revoked_at IS NULL`,
				at, at, input.RegistrationID.String(), input.UserID.String()); err != nil {
				return nil, fmt.Errorf("revoke Desktop Registration: %w", err)
			}
			registration.UpdatedAt = at
			registration.RevokedAt = model.OptionalTimeFrom(at)
		}
		encoded, err := model.EncodeAuditData(registration.Auditable())
		if err != nil {
			return nil, err
		}
		if _, err = completeAuditEvent(ctx, tx, input.AuditEventID, model.AuditStatusSuccess, "", encoded, input.AuditAt); err != nil {
			return nil, err
		}
		sessions, err := revokedSessionModelsAt(sessionRows, at, model.SessionRevocationDesktopRegistration)
		if err != nil {
			return nil, err
		}
		return &store.DesktopRegistrationRevocationResult{
			Registration: registration, Sessions: sessions, TokenHashes: hashes, AlreadyRevoked: alreadyRevoked,
		}, nil
	})
}

func (row desktopRegistrationRow) model() (*model.DesktopRegistration, error) {
	id, err := model.ParseDesktopRegistrationID(row.ID)
	if err != nil {
		return nil, store.NewErrInvalidInput("desktop_registration", "id", row.ID)
	}
	userID, err := model.ParseUserID(row.UserID)
	if err != nil {
		return nil, store.NewErrInvalidInput("desktop_registration", "user_id", row.UserID)
	}
	institutionID, err := model.ParseInstitutionID(row.InstitutionID)
	if err != nil {
		return nil, store.NewErrInvalidInput("desktop_registration", "institution_id", row.InstitutionID)
	}
	value := &model.DesktopRegistration{
		ID: id, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
		UserID: userID, InstitutionID: institutionID, KeyThumbprint: row.KeyThumbprint,
		DisplayName: row.DisplayName, DesktopRelease: row.DesktopRelease, DesktopBuildID: row.DesktopBuildID,
		Platform: model.DesktopPlatform(row.Platform), Architecture: model.DesktopArchitecture(row.Architecture),
		RealtimeProtocol: row.RealtimeProtocol, LastUsedAt: row.LastUsedAt.UTC(),
		RevokedAt: OptionalTimeFromNullTime(row.RevokedAt),
	}
	if err = json.Unmarshal(row.PublicJWK, &value.PublicJWK); err != nil {
		return nil, store.NewErrInvalidInput("desktop_registration", "public_jwk", nil)
	}
	if err = validatePersistedModel("desktop_registration", value); err != nil {
		return nil, err
	}
	return value, nil
}

var _ store.DesktopRegistrationStore = (*SQLDesktopRegistrationStore)(nil)
