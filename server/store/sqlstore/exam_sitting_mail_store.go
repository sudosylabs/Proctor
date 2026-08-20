// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

func (s sqlExamSittingStore) GetMailFanout(ctx context.Context, occurrenceID model.MailOccurrenceID) (*store.ExamSittingMailFanoutSnapshot, error) {
	if !occurrenceID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_sitting_mail_fanout", "id", nil)
	}
	var row struct {
		Kind            string         `db:"kind"`
		TemplateKey     string         `db:"template_key"`
		ActorUserID     string         `db:"actor_user_id"`
		CreatedAt       time.Time      `db:"created_at"`
		BundleID        sql.NullString `db:"bundle_id"`
		Payload         jsonValue      `db:"encrypted_payload"`
		BundleCreatedAt sql.NullTime   `db:"bundle_created_at"`
		BundleRevision  sql.NullInt64  `db:"bundle_revision"`
		SittingID       string         `db:"exam_sitting_id"`
		SittingRevision int64          `db:"sitting_revision"`
		PriorClassID    sql.NullString `db:"prior_class_id"`
		ChangeKind      string         `db:"change_kind"`
		Deadline        time.Time      `db:"deadline"`
		CompletedAt     sql.NullTime   `db:"completed_at"`
	}
	if err := s.GetMaster().Get(ctx, &row, `SELECT o.kind,o.template_key,o.actor_user_id,o.created_at,
		f.bundle_id,b.encrypted_payload,b.created_at bundle_created_at,b.revision bundle_revision,
		f.exam_sitting_id,f.sitting_revision,f.prior_class_id,f.change_kind,f.deadline,f.completed_at
		FROM exam_sitting_mail_fanouts f JOIN mail_occurrences o ON o.id=f.occurrence_id
		LEFT JOIN mail_fanout_bundles b ON b.id=f.bundle_id WHERE f.occurrence_id=?`, occurrenceID.String()); err != nil {
		return nil, translateError("exam_sitting_mail_fanout", occurrenceID.String(), err)
	}
	actorID, err := model.ParseUserID(row.ActorUserID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting_mail_fanout", "actor_user_id", err)
	}
	sittingID, err := model.ParseExamSittingID(row.SittingID)
	if err != nil {
		return nil, invalidPersistedState("exam_sitting_mail_fanout", "exam_sitting_id", err)
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceKind(row.Kind),
		TemplateKey: model.MailTemplateKey(row.TemplateKey), ActorUserID: actorID, CreatedAt: model.TimeUTC(row.CreatedAt)}
	if err = occurrence.Validate(); err != nil {
		return nil, invalidPersistedState("exam_sitting_mail_fanout", "occurrence", err)
	}
	snapshot := &store.ExamSittingMailFanoutSnapshot{Occurrence: occurrence, SittingID: sittingID,
		SittingRevision: row.SittingRevision, PriorClassID: model.ClassID(row.PriorClassID.String),
		ChangeKind: store.ExamSittingMailChangeKind(row.ChangeKind), Deadline: model.TimeUTC(row.Deadline),
		CompletedAt: optionalTime(row.CompletedAt)}
	if row.BundleID.Valid {
		if !row.BundleCreatedAt.Valid || !row.BundleRevision.Valid || row.CompletedAt.Valid {
			return nil, invalidPersistedState("exam_sitting_mail_fanout", "bundle", errors.New("active bundle lifecycle is invalid"))
		}
		bundleID := model.MailOccurrenceID(row.BundleID.String)
		snapshot.Bundle = &model.MailFanoutBundle{ID: bundleID, EncryptedPayload: append([]byte(nil), row.Payload...),
			CreatedAt: model.TimeUTC(row.BundleCreatedAt.Time), Revision: row.BundleRevision.Int64}
		if snapshot.Bundle.Validate() != nil || bundleID != occurrenceID {
			return nil, invalidPersistedState("exam_sitting_mail_fanout", "bundle", errors.New("bundle identity is invalid"))
		}
	}
	if snapshot.CompletedAt.Valid != (snapshot.Bundle == nil) || snapshot.SittingRevision < 1 || snapshot.Deadline.IsZero() {
		return nil, invalidPersistedState("exam_sitting_mail_fanout", "value", errors.New("fan-out lifecycle is invalid"))
	}
	return snapshot, nil
}

func (s sqlExamSittingStore) ListMailRecipients(ctx context.Context, request store.ExamSittingMailRecipientPageRequest) (*store.ExamSittingMailRecipientPage, error) {
	if !request.OccurrenceID.IsValid() || request.Limit < 1 || request.Limit > model.SittingMailExpansionPageSize ||
		(!request.AfterUserID.IsZero() && !request.AfterUserID.IsValid()) {
		return nil, store.NewErrInvalidInput("exam_sitting_mail_fanout", "recipient_page", nil)
	}
	fanout, err := s.GetMailFanout(ctx, request.OccurrenceID)
	if err != nil {
		return nil, err
	}
	page := &store.ExamSittingMailRecipientPage{Fanout: fanout, Recipients: []store.ExamSittingMailRecipient{}}
	if fanout.CompletedAt.Valid || fanout.Bundle == nil {
		return page, nil
	}
	columns := userSliceColumns()
	for index := range columns {
		columns[index] = "u." + columns[index][len("users."):]
	}
	query := `WITH current_sitting AS (
		SELECT s.id,s.class_id,s.scheduled_start_at,s.state,s.revision,s.mail_disabled_suppressed_revision,
			s.mail_disabled_suppressed_audience_revision,s.mail_disabled_suppressed_eligibility_revision,statement_timestamp() now
		FROM exam_sittings s WHERE s.id=?
	), audience AS (
		SELECT cm.user_id,TRUE eligible FROM class_members cm,current_sitting s
		WHERE cm.class_id=s.class_id AND cm.archived_at IS NULL AND cm.start_at<=s.scheduled_start_at
		  AND (cm.end_at IS NULL OR cm.end_at>s.scheduled_start_at)
		UNION
		SELECT p.user_id,FALSE eligible FROM exam_sitting_mail_recipients p WHERE p.exam_sitting_id=?
	), candidates AS (
		SELECT user_id,bool_or(eligible) eligible FROM audience GROUP BY user_id
	)
	SELECT ` + strings.Join(columns, ",") + `,c.eligible,u.mail_eligibility_revision,COALESCE((SELECT max(history.mail_audience_revision)
		FROM class_members history WHERE history.class_id=s.class_id AND history.user_id=c.user_id),0) mail_audience_revision,
		p.communicated_sitting_revision,p.communicated_template_key,p.communicated_class_id,
		s.class_id current_class_id,s.state sitting_state,s.revision sitting_revision,s.scheduled_start_at,
		s.mail_disabled_suppressed_revision,s.mail_disabled_suppressed_audience_revision,
		s.mail_disabled_suppressed_eligibility_revision,s.now
		FROM candidates c JOIN users u ON u.id=c.user_id
		JOIN current_sitting s ON TRUE
		LEFT JOIN exam_sitting_mail_recipients p ON p.exam_sitting_id=s.id AND p.user_id=c.user_id
		WHERE c.user_id>? ORDER BY c.user_id LIMIT ?`
	var rows []struct {
		userRow
		Eligible                    bool           `db:"eligible"`
		MailAudienceRevision        int64          `db:"mail_audience_revision"`
		MailEligibilityRevision     int64          `db:"mail_eligibility_revision"`
		CommunicatedSittingRevision sql.NullInt64  `db:"communicated_sitting_revision"`
		CommunicatedTemplateKey     sql.NullString `db:"communicated_template_key"`
		CommunicatedClassID         sql.NullString `db:"communicated_class_id"`
		CurrentClassID              string         `db:"current_class_id"`
		SittingState                string         `db:"sitting_state"`
		SittingRevision             int64          `db:"sitting_revision"`
		DisabledSuppressedRevision  sql.NullInt64  `db:"mail_disabled_suppressed_revision"`
		DisabledAudienceRevision    sql.NullInt64  `db:"mail_disabled_suppressed_audience_revision"`
		DisabledEligibilityRevision sql.NullInt64  `db:"mail_disabled_suppressed_eligibility_revision"`
		ScheduledStartAt            time.Time      `db:"scheduled_start_at"`
		Now                         time.Time      `db:"now"`
	}
	if err = s.GetMaster().Select(ctx, &rows, query, fanout.SittingID.String(), fanout.SittingID.String(),
		request.AfterUserID.String(), request.Limit+1); err != nil {
		return nil, fmt.Errorf("list Sitting mail recipients: %w", err)
	}
	if len(rows) > request.Limit {
		page.More = true
		rows = rows[:request.Limit]
	}
	for index := range rows {
		row := rows[index]
		user, modelErr := row.userRow.model()
		if modelErr != nil {
			return nil, modelErr
		}
		key := sittingMailRecipientTemplate(fanout, row.SittingState, row.SittingRevision,
			model.TimeUTC(row.ScheduledStartAt), model.TimeUTC(row.Now), row.Eligible,
			row.MailAudienceRevision, row.MailEligibilityRevision, row.DisabledSuppressedRevision,
			row.DisabledAudienceRevision, row.DisabledEligibilityRevision,
			row.CommunicatedSittingRevision, model.MailTemplateKey(row.CommunicatedTemplateKey.String))
		page.Recipients = append(page.Recipients, store.ExamSittingMailRecipient{User: user, TemplateKey: key})
	}
	return page, nil
}

func sittingMailRecipientTemplate(fanout *store.ExamSittingMailFanoutSnapshot, sittingState string, sittingRevision int64,
	startAt, now time.Time, eligible bool, mailAudienceRevision, mailEligibilityRevision int64,
	disabledRevision, disabledAudienceRevision, disabledEligibilityRevision sql.NullInt64,
	communicatedRevision sql.NullInt64, communicatedKey model.MailTemplateKey,
) model.MailTemplateKey {
	if fanout == nil || sittingRevision != fanout.SittingRevision || !now.Before(startAt) {
		return ""
	}
	if disabledRevision.Valid && disabledAudienceRevision.Valid && disabledEligibilityRevision.Valid &&
		disabledRevision.Int64 == sittingRevision && mailAudienceRevision <= disabledAudienceRevision.Int64 &&
		mailEligibilityRevision <= disabledEligibilityRevision.Int64 {
		return ""
	}
	if fanout.ChangeKind == store.ExamSittingMailCancelled {
		if sittingState == string(model.ExamSittingCanceled) && communicatedRevision.Valid &&
			(communicatedKey == model.MailTemplateExamSittingScheduled || communicatedKey == model.MailTemplateExamSittingRescheduled) {
			return model.MailTemplateExamSittingCancelled
		}
		return ""
	}
	if sittingState != string(model.ExamSittingScheduled) {
		return ""
	}
	if eligible {
		if !communicatedRevision.Valid || communicatedKey == model.MailTemplateExamSittingAssignmentRemoved ||
			communicatedKey == model.MailTemplateExamSittingCancelled {
			return model.MailTemplateExamSittingScheduled
		}
		if communicatedRevision.Int64 < sittingRevision {
			return model.MailTemplateExamSittingRescheduled
		}
		return ""
	}
	if communicatedRevision.Valid &&
		(communicatedKey == model.MailTemplateExamSittingScheduled || communicatedKey == model.MailTemplateExamSittingRescheduled) {
		return model.MailTemplateExamSittingAssignmentRemoved
	}
	return ""
}

func (s sqlExamSittingStore) CommitMailRecipient(ctx context.Context, input *store.ExamSittingMailRecipientCommit) (*store.ExamSittingMailRecipientResult, error) {
	if input == nil || !input.OccurrenceID.IsValid() || input.SittingRevision < 1 || input.Recipient == nil ||
		input.Recipient.Validate() != nil {
		return nil, store.NewErrInvalidInput("exam_sitting_mail_recipient", "value", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*store.ExamSittingMailRecipientResult](true,
		func(_ *store.ExamSittingMailRecipientResult, err error) error {
			return fmt.Errorf("commit Sitting mail recipient: %w", err)
		}),
		func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSittingMailRecipientResult, error) {
			user, err := lockSittingMailUser(ctx, tx, input.Recipient.ID)
			if err != nil {
				return nil, err
			}
			if err := lockAffiliationLifecycle(ctx, tx); err != nil {
				return nil, err
			}
			if err := lockClassLifecycle(ctx, tx); err != nil {
				return nil, err
			}
			fanout, sitting, now, err := lockSittingMailFanout(ctx, tx, input.OccurrenceID)
			if err != nil {
				return nil, err
			}
			if fanout.SittingRevision != input.SittingRevision {
				return nil, store.NewErrConflict("exam_sitting_mail_fanout", "stale_revision", nil)
			}
			if fanout.CompletedAt.Valid {
				return &store.ExamSittingMailRecipientResult{Suppressed: true}, nil
			}
			if !sameSittingMailRecipient(user, input.Recipient) {
				return nil, store.NewErrConflict("exam_sitting_mail_recipient", "recipient_changed", nil)
			}
			projection, err := lockSittingMailProjection(ctx, tx, sitting.ID, user.ID)
			if err != nil {
				return nil, err
			}
			audience, err := loadSittingMailAudienceState(ctx, tx, sitting, user)
			if err != nil {
				return nil, err
			}
			expectedKey := sittingMailRecipientTemplate(fanout, string(sitting.State), sitting.Revision,
				sitting.ScheduledStartAt, now, audience.Eligible, audience.Revision, audience.EligibilityRevision,
				audience.DisabledSittingRevision, audience.DisabledAudienceRevision, audience.DisabledEligibilityRevision,
				projection.CommunicatedRevision,
				model.MailTemplateKey(projection.CommunicatedTemplateKey.String))
			if existing, findErr := getSittingOccurrenceDelivery(ctx, tx, input.OccurrenceID, user.ID); findErr == nil {
				if expectedKey != "" || existing.State == model.MailDeliveryAccepted || existing.State == model.MailDeliverySuppressed || existing.State == model.MailDeliveryCanceled {
					return &store.ExamSittingMailRecipientResult{Delivery: existing, Inserted: false,
						Suppressed: existing.State != model.MailDeliveryAccepted}, nil
				}
			} else if !store.IsNotFound(findErr) {
				return nil, findErr
			}
			if expectedKey == "" || !user.IsActive() || !user.EmailVerified {
				if err = suppressSittingProjectionDelivery(ctx, tx, projection, now); err != nil {
					return nil, err
				}
				if err = clearSittingDesiredProjection(ctx, tx, sitting.ID, user.ID, now); err != nil {
					return nil, err
				}
				return &store.ExamSittingMailRecipientResult{Suppressed: true}, nil
			}
			if input.Delivery == nil || input.DeliveryJob == nil || input.Delivery.Validate() != nil || input.DeliveryJob.Validate() != nil ||
				input.Delivery.TargetUserID != user.ID || input.Delivery.TargetInvitationID.IsValid() ||
				input.Delivery.OccurrenceID != input.OccurrenceID || input.Delivery.TemplateKey != expectedKey ||
				input.Delivery.State != model.MailDeliveryQueued || !input.Delivery.CreatedAt.Equal(fanout.Occurrence.CreatedAt) ||
				!input.Delivery.Deadline.Equal(fanout.Deadline) || input.Delivery.JobID != input.DeliveryJob.ID ||
				input.DeliveryJob.Type != model.JobTypeMailDeliver || input.DeliveryJob.Status != model.JobStatusQueued {
				return nil, store.NewErrInvalidInput("exam_sitting_mail_recipient", "delivery", nil)
			}
			if err = suppressSittingProjectionDelivery(ctx, tx, projection, now); err != nil {
				return nil, err
			}
			payloadKeyID, err := mailPayloadKeyID(input.Delivery.EncryptedPayload)
			if err != nil {
				return nil, store.NewErrInvalidInput("mail_delivery", "encrypted_payload", err)
			}
			if err = requireMailPayloadPrimary(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
			if err = insertPreparedMailJob(ctx, tx, input.DeliveryJob); err != nil {
				return nil, fmt.Errorf("insert Sitting mail delivery Job: %w", translateError("job", input.DeliveryJob.ID.String(), err))
			}
			if _, err = tx.Exec(ctx, `INSERT INTO mail_deliveries (`+mailDeliveryColumns+`) VALUES
				(?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,NULL,NULL,'',?,?,1)`, input.Delivery.ID.String(), input.Delivery.OccurrenceID.String(),
				input.Delivery.JobID.String(), user.ID.String(), nil, input.Delivery.TemplateKey, input.Delivery.TemplateDigest,
				input.Delivery.MaskedRecipient, input.Delivery.State, input.Delivery.CreatedAt, input.Delivery.UpdatedAt,
				input.Delivery.MessageDate, input.Delivery.Deadline, input.Delivery.MessageID, payloadKeyID,
				input.Delivery.EncryptedPayload); err != nil {
				return nil, fmt.Errorf("insert Sitting mail delivery: %w", translateError("mail_delivery", input.Delivery.ID.String(), err))
			}
			if err = incrementMailPayloadKeyReference(ctx, tx, payloadKeyID); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO exam_sitting_mail_recipients
				(exam_sitting_id,user_id,desired_occurrence_id,desired_delivery_id,desired_sitting_revision,desired_template_key,updated_at)
				VALUES(?,?,?,?,?,?,?) ON CONFLICT(exam_sitting_id,user_id) DO UPDATE SET
				desired_occurrence_id=EXCLUDED.desired_occurrence_id,desired_delivery_id=EXCLUDED.desired_delivery_id,
				desired_sitting_revision=EXCLUDED.desired_sitting_revision,desired_template_key=EXCLUDED.desired_template_key,
				updated_at=EXCLUDED.updated_at`, sitting.ID.String(), user.ID.String(), input.OccurrenceID.String(),
				input.Delivery.ID.String(), sitting.Revision, expectedKey, now); err != nil {
				return nil, fmt.Errorf("update Sitting mail recipient projection: %w", err)
			}
			return &store.ExamSittingMailRecipientResult{Delivery: input.Delivery.Clone(), Inserted: true}, nil
		})
}

type sittingMailProjectionRow struct {
	DesiredOccurrenceID     sql.NullString `db:"desired_occurrence_id"`
	DesiredDeliveryID       sql.NullString `db:"desired_delivery_id"`
	DesiredRevision         sql.NullInt64  `db:"desired_sitting_revision"`
	DesiredTemplateKey      sql.NullString `db:"desired_template_key"`
	CommunicatedRevision    sql.NullInt64  `db:"communicated_sitting_revision"`
	CommunicatedTemplateKey sql.NullString `db:"communicated_template_key"`
	CommunicatedClassID     sql.NullString `db:"communicated_class_id"`
}

func lockSittingMailFanout(ctx context.Context, tx *sqlxTxWrapper, occurrenceID model.MailOccurrenceID) (*store.ExamSittingMailFanoutSnapshot, *model.ExamSitting, time.Time, error) {
	var raw struct {
		ExamID    string `db:"exam_id"`
		SittingID string `db:"exam_sitting_id"`
	}
	if err := tx.Get(ctx, &raw, `SELECT s.exam_id,f.exam_sitting_id FROM exam_sitting_mail_fanouts f JOIN exam_sittings s ON s.id=f.exam_sitting_id
		WHERE f.occurrence_id=?`, occurrenceID.String()); err != nil {
		return nil, nil, time.Time{}, translateError("exam_sitting_mail_fanout", occurrenceID.String(), err)
	}
	examID, err := model.ParseExamID(raw.ExamID)
	if err != nil {
		return nil, nil, time.Time{}, invalidPersistedState("exam_sitting_mail_fanout", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(raw.SittingID)
	if err != nil {
		return nil, nil, time.Time{}, invalidPersistedState("exam_sitting_mail_fanout", "exam_sitting_id", err)
	}
	// Sitting transitions lock the Exam and Sitting before inserting their
	// fan-out. Expansion takes the same order so a concurrent reschedule cannot
	// deadlock with recipient relevance evaluation.
	sitting, err := lockExamSitting(ctx, tx, examID, sittingID)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	var fanoutRow struct {
		SittingID       string         `db:"exam_sitting_id"`
		SittingRevision int64          `db:"sitting_revision"`
		ChangeKind      string         `db:"change_kind"`
		Deadline        time.Time      `db:"deadline"`
		CompletedAt     sql.NullTime   `db:"completed_at"`
		BundleID        sql.NullString `db:"bundle_id"`
		Kind            string         `db:"kind"`
		TemplateKey     string         `db:"template_key"`
		ActorUserID     string         `db:"actor_user_id"`
		CreatedAt       time.Time      `db:"created_at"`
	}
	if err = tx.Get(ctx, &fanoutRow, `SELECT f.exam_sitting_id,f.sitting_revision,f.change_kind,f.deadline,f.completed_at,f.bundle_id,
		o.kind,o.template_key,o.actor_user_id,o.created_at FROM exam_sitting_mail_fanouts f
		JOIN mail_occurrences o ON o.id=f.occurrence_id WHERE f.occurrence_id=? FOR UPDATE OF f`, occurrenceID.String()); err != nil {
		return nil, nil, time.Time{}, translateError("exam_sitting_mail_fanout", occurrenceID.String(), err)
	}
	if fanoutRow.SittingID != sittingID.String() {
		return nil, nil, time.Time{}, invalidPersistedState("exam_sitting_mail_fanout", "exam_sitting_id", errors.New("fan-out Sitting changed while locking"))
	}
	actorID, err := model.ParseUserID(fanoutRow.ActorUserID)
	if err != nil {
		return nil, nil, time.Time{}, invalidPersistedState("exam_sitting_mail_fanout", "actor_user_id", err)
	}
	occurrence := &model.MailOccurrence{ID: occurrenceID, Kind: model.MailOccurrenceKind(fanoutRow.Kind),
		TemplateKey: model.MailTemplateKey(fanoutRow.TemplateKey), ActorUserID: actorID, CreatedAt: model.TimeUTC(fanoutRow.CreatedAt)}
	if occurrence.Validate() != nil {
		return nil, nil, time.Time{}, invalidPersistedState("exam_sitting_mail_fanout", "occurrence", errors.New("invalid occurrence"))
	}
	now, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	fanout := &store.ExamSittingMailFanoutSnapshot{Occurrence: occurrence, SittingID: sittingID,
		SittingRevision: fanoutRow.SittingRevision, ChangeKind: store.ExamSittingMailChangeKind(fanoutRow.ChangeKind),
		Deadline: model.TimeUTC(fanoutRow.Deadline), CompletedAt: optionalTime(fanoutRow.CompletedAt)}
	if fanoutRow.BundleID.Valid {
		fanout.Bundle = &model.MailFanoutBundle{ID: occurrenceID, CreatedAt: occurrence.CreatedAt, Revision: 1, EncryptedPayload: []byte(`{}`)}
	}
	return fanout, sitting, now, nil
}

func lockSittingMailUser(ctx context.Context, tx *sqlxTxWrapper, userID model.UserID) (*model.User, error) {
	columns := userSliceColumns()
	for index := range columns {
		columns[index] = columns[index][len("users."):]
	}
	var row userRow
	if err := tx.Get(ctx, &row, `SELECT `+strings.Join(columns, ",")+` FROM users WHERE id=? FOR SHARE`, userID.String()); err != nil {
		return nil, translateError("user", userID.String(), err)
	}
	return row.model()
}

func sameSittingMailRecipient(current, prepared *model.User) bool {
	return current != nil && prepared != nil && current.ID == prepared.ID && current.Revision == prepared.Revision &&
		current.Email == prepared.Email && current.EmailVerified == prepared.EmailVerified && current.DisplayName == prepared.DisplayName &&
		current.Locale == prepared.Locale && current.Timezone == prepared.Timezone && current.ArchivedAt == prepared.ArchivedAt &&
		current.DisabledAt == prepared.DisabledAt
}

func lockSittingMailProjection(ctx context.Context, tx *sqlxTxWrapper, sittingID model.ExamSittingID, userID model.UserID) (sittingMailProjectionRow, error) {
	var row sittingMailProjectionRow
	err := tx.Get(ctx, &row, `SELECT desired_occurrence_id,desired_delivery_id,desired_sitting_revision,desired_template_key,
		communicated_sitting_revision,communicated_template_key,communicated_class_id
		FROM exam_sitting_mail_recipients WHERE exam_sitting_id=? AND user_id=? FOR UPDATE`, sittingID.String(), userID.String())
	if err == sql.ErrNoRows {
		return sittingMailProjectionRow{}, nil
	}
	if err != nil {
		return row, fmt.Errorf("lock Sitting mail recipient projection: %w", err)
	}
	return row, nil
}

type sittingMailDeliveryFence struct {
	Sitting              *model.ExamSitting
	User                 *model.User
	Projection           sittingMailProjectionRow
	OccurrenceID         model.MailOccurrenceID
	OccurrenceRevision   int64
	TemplateKey          model.MailTemplateKey
	CommunicatedClassID  model.ClassID
	FanoutTerminalReason string
	Now                  time.Time
}

func lockSittingMailDeliveryFence(ctx context.Context, tx *sqlxTxWrapper, deliveryID model.MailDeliveryID) (*sittingMailDeliveryFence, error) {
	var route struct {
		ExamID          string         `db:"exam_id"`
		SittingID       string         `db:"exam_sitting_id"`
		UserID          string         `db:"target_user_id"`
		OccurrenceID    string         `db:"occurrence_id"`
		SittingRevision int64          `db:"sitting_revision"`
		TemplateKey     string         `db:"template_key"`
		PriorClassID    sql.NullString `db:"prior_class_id"`
	}
	if err := tx.Get(ctx, &route, `SELECT s.exam_id,f.exam_sitting_id,d.target_user_id,d.occurrence_id,
		f.sitting_revision,d.template_key,f.prior_class_id FROM mail_deliveries d
		JOIN exam_sitting_mail_fanouts f ON f.occurrence_id=d.occurrence_id
		JOIN exam_sittings s ON s.id=f.exam_sitting_id WHERE d.id=?`, deliveryID.String()); err != nil {
		return nil, translateError("mail_delivery", deliveryID.String(), err)
	}
	examID, err := model.ParseExamID(route.ExamID)
	if err != nil {
		return nil, invalidPersistedState("mail_delivery", "exam_id", err)
	}
	sittingID, err := model.ParseExamSittingID(route.SittingID)
	if err != nil {
		return nil, invalidPersistedState("mail_delivery", "exam_sitting_id", err)
	}
	userID, err := model.ParseUserID(route.UserID)
	if err != nil {
		return nil, invalidPersistedState("mail_delivery", "target_user_id", err)
	}
	user, err := lockSittingMailUser(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if err = lockAffiliationLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	if err = lockClassLifecycle(ctx, tx); err != nil {
		return nil, err
	}
	occurrenceID := model.MailOccurrenceID(route.OccurrenceID)
	if !occurrenceID.IsValid() {
		return nil, invalidPersistedState("mail_delivery", "occurrence_id", errors.New("invalid Sitting mail occurrence"))
	}
	sitting, err := lockExamSitting(ctx, tx, examID, sittingID)
	if err != nil {
		return nil, err
	}
	var fanoutTerminalReason string
	if err = tx.Get(ctx, &fanoutTerminalReason, `SELECT terminal_reason FROM exam_sitting_mail_fanouts
		WHERE occurrence_id=? FOR UPDATE`, occurrenceID.String()); err != nil {
		return nil, translateError("exam_sitting_mail_fanout", occurrenceID.String(), err)
	}
	projection, err := lockSittingMailProjection(ctx, tx, sittingID, userID)
	if err != nil {
		return nil, err
	}
	now, err := jobDatabaseNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	communicatedClassID := sitting.ClassID
	if model.MailTemplateKey(route.TemplateKey) == model.MailTemplateExamSittingAssignmentRemoved && route.PriorClassID.Valid {
		communicatedClassID = model.ClassID(route.PriorClassID.String)
	}
	return &sittingMailDeliveryFence{Sitting: sitting, User: user, Projection: projection,
		OccurrenceID: occurrenceID, OccurrenceRevision: route.SittingRevision,
		TemplateKey: model.MailTemplateKey(route.TemplateKey), CommunicatedClassID: communicatedClassID,
		FanoutTerminalReason: fanoutTerminalReason, Now: now}, nil
}

func (fence *sittingMailDeliveryFence) relevant(ctx context.Context, tx *sqlxTxWrapper, deliveryID model.MailDeliveryID) (bool, error) {
	if fence == nil || fence.Sitting == nil || fence.User == nil || !fence.Now.Before(fence.Sitting.ScheduledStartAt) ||
		(fence.FanoutTerminalReason != "" && fence.FanoutTerminalReason != "completed") ||
		!fence.Projection.DesiredDeliveryID.Valid || fence.Projection.DesiredDeliveryID.String != deliveryID.String() ||
		!fence.Projection.DesiredOccurrenceID.Valid || fence.Projection.DesiredOccurrenceID.String != fence.OccurrenceID.String() ||
		!fence.Projection.DesiredRevision.Valid || fence.Projection.DesiredRevision.Int64 != fence.OccurrenceRevision ||
		!fence.Projection.DesiredTemplateKey.Valid || model.MailTemplateKey(fence.Projection.DesiredTemplateKey.String) != fence.TemplateKey ||
		fence.Sitting.Revision != fence.OccurrenceRevision {
		return false, nil
	}
	audience, err := loadSittingMailAudienceState(ctx, tx, fence.Sitting, fence.User)
	if err != nil {
		return false, err
	}
	switch fence.TemplateKey {
	case model.MailTemplateExamSittingScheduled, model.MailTemplateExamSittingRescheduled:
		return fence.Sitting.State == model.ExamSittingScheduled && audience.Eligible, nil
	case model.MailTemplateExamSittingAssignmentRemoved:
		return fence.Sitting.State == model.ExamSittingScheduled && fence.User.IsActive() && fence.User.EmailVerified && !audience.Eligible, nil
	case model.MailTemplateExamSittingCancelled:
		communicated := model.MailTemplateKey(fence.Projection.CommunicatedTemplateKey.String)
		return fence.Sitting.State == model.ExamSittingCanceled && fence.User.IsActive() && fence.User.EmailVerified &&
			fence.Projection.CommunicatedTemplateKey.Valid &&
			(communicated == model.MailTemplateExamSittingScheduled || communicated == model.MailTemplateExamSittingRescheduled), nil
	default:
		return false, invalidPersistedState("mail_delivery", "sitting_template", errors.New("unsupported Sitting mail template"))
	}
}

func markSittingMailCommunicated(ctx context.Context, tx *sqlxTxWrapper, fence *sittingMailDeliveryFence, at time.Time) error {
	if fence == nil || fence.Sitting == nil || fence.User == nil {
		return invalidPersistedState("mail_delivery", "sitting_projection", errors.New("missing Sitting delivery fence"))
	}
	_, err := tx.Exec(ctx, `INSERT INTO exam_sitting_mail_recipients
		(exam_sitting_id,user_id,communicated_sitting_revision,communicated_template_key,communicated_class_id,updated_at)
		VALUES(?,?,?,?,?,?) ON CONFLICT(exam_sitting_id,user_id) DO UPDATE SET
		communicated_sitting_revision=EXCLUDED.communicated_sitting_revision,
		communicated_template_key=EXCLUDED.communicated_template_key,
		communicated_class_id=EXCLUDED.communicated_class_id,updated_at=EXCLUDED.updated_at`,
		fence.Sitting.ID.String(), fence.User.ID.String(), fence.OccurrenceRevision, fence.TemplateKey,
		fence.CommunicatedClassID.String(), model.TimeUTC(at))
	return err
}

func isSittingScheduleMailTemplate(key model.MailTemplateKey) bool {
	switch key {
	case model.MailTemplateExamSittingScheduled, model.MailTemplateExamSittingRescheduled,
		model.MailTemplateExamSittingCancelled, model.MailTemplateExamSittingAssignmentRemoved:
		return true
	default:
		return false
	}
}

type sittingMailAudienceState struct {
	Eligible                    bool
	Revision                    int64
	DisabledSittingRevision     sql.NullInt64
	DisabledAudienceRevision    sql.NullInt64
	EligibilityRevision         int64
	DisabledEligibilityRevision sql.NullInt64
}

func loadSittingMailAudienceState(ctx context.Context, tx *sqlxTxWrapper, sitting *model.ExamSitting,
	user *model.User,
) (sittingMailAudienceState, error) {
	if sitting == nil || user == nil {
		return sittingMailAudienceState{}, store.NewErrInvalidInput("exam_sitting_mail_recipient", "audience", nil)
	}
	var result struct {
		Eligible                    bool          `db:"eligible"`
		MailAudienceRevision        int64         `db:"mail_audience_revision"`
		MailEligibilityRevision     int64         `db:"mail_eligibility_revision"`
		DisabledSittingRevision     sql.NullInt64 `db:"mail_disabled_suppressed_revision"`
		DisabledAudienceRevision    sql.NullInt64 `db:"mail_disabled_suppressed_audience_revision"`
		DisabledEligibilityRevision sql.NullInt64 `db:"mail_disabled_suppressed_eligibility_revision"`
	}
	if err := tx.Get(ctx, &result, `SELECT EXISTS(SELECT 1 FROM class_members active WHERE active.class_id=s.class_id
		AND active.user_id=? AND active.archived_at IS NULL AND active.start_at<=s.scheduled_start_at
		AND (active.end_at IS NULL OR active.end_at>s.scheduled_start_at)) eligible,
		u.mail_eligibility_revision,COALESCE((SELECT max(history.mail_audience_revision) FROM class_members history
			WHERE history.class_id=s.class_id AND history.user_id=?),0) mail_audience_revision,
		s.mail_disabled_suppressed_revision,s.mail_disabled_suppressed_audience_revision,
		s.mail_disabled_suppressed_eligibility_revision
		FROM exam_sittings s JOIN users u ON u.id=? WHERE s.id=?`, user.ID.String(), user.ID.String(), user.ID.String(), sitting.ID.String()); err != nil {
		return sittingMailAudienceState{}, fmt.Errorf("check Sitting mail recipient audience: %w", err)
	}
	return sittingMailAudienceState{
		Eligible: result.Eligible && user.IsActive() && user.EmailVerified && sitting.State == model.ExamSittingScheduled,
		Revision: result.MailAudienceRevision, DisabledSittingRevision: result.DisabledSittingRevision,
		DisabledAudienceRevision: result.DisabledAudienceRevision, EligibilityRevision: result.MailEligibilityRevision,
		DisabledEligibilityRevision: result.DisabledEligibilityRevision,
	}, nil
}

func getSittingOccurrenceDelivery(ctx context.Context, tx *sqlxTxWrapper, occurrenceID model.MailOccurrenceID, userID model.UserID) (*model.MailDelivery, error) {
	var row mailDeliveryRow
	if err := tx.Get(ctx, &row, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE occurrence_id=? AND target_user_id=?`,
		occurrenceID.String(), userID.String()); err != nil {
		return nil, translateError("mail_delivery", occurrenceID.String(), err)
	}
	return row.model()
}

func suppressSittingProjectionDelivery(ctx context.Context, tx *sqlxTxWrapper, projection sittingMailProjectionRow, at time.Time) error {
	if !projection.DesiredDeliveryID.Valid {
		return nil
	}
	id := model.MailDeliveryID(projection.DesiredDeliveryID.String)
	var row mailDeliveryRow
	if err := tx.Get(ctx, &row, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE id=? FOR UPDATE`, id.String()); err != nil {
		return translateError("mail_delivery", id.String(), err)
	}
	current, err := row.model()
	if err != nil {
		return err
	}
	if current.State == model.MailDeliveryAccepted || current.State == model.MailDeliverySuppressed || current.State == model.MailDeliveryCanceled {
		return nil
	}
	transitionAt := model.TimeUTC(at)
	if transitionAt.Before(current.UpdatedAt) {
		transitionAt = current.UpdatedAt
	}
	updated, err := current.Suppress(model.MailDeliveryObsoleteCode, transitionAt)
	if err != nil {
		return invalidPersistedState("mail_delivery", "sitting_suppression", err)
	}
	if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
		return err
	}
	job, err := getJob(ctx, tx, current.JobID, true)
	if err != nil {
		return err
	}
	if job.Status == model.JobStatusQueued || job.Status == model.JobStatusRunning {
		jobAt := transitionAt
		if jobAt.Before(job.UpdatedAt) {
			jobAt = job.UpdatedAt
		}
		updatedJob, cancelErr := job.RequestCancellation(jobAt)
		if cancelErr != nil {
			return invalidPersistedState("job", "sitting_mail_cancellation", cancelErr)
		}
		return updateJob(ctx, tx, updatedJob)
	}
	return nil
}

func clearSittingDesiredProjection(ctx context.Context, tx *sqlxTxWrapper, sittingID model.ExamSittingID, userID model.UserID, at time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE exam_sitting_mail_recipients SET desired_occurrence_id=NULL,desired_delivery_id=NULL,
		desired_sitting_revision=NULL,desired_template_key=NULL,updated_at=? WHERE exam_sitting_id=? AND user_id=?`,
		model.TimeUTC(at), sittingID.String(), userID.String())
	return err
}

func clearExactSittingDesiredDelivery(ctx context.Context, tx *sqlxTxWrapper, fence *sittingMailDeliveryFence,
	deliveryID model.MailDeliveryID, at time.Time,
) error {
	if fence == nil || fence.Sitting == nil || fence.User == nil || !deliveryID.IsValid() || !fence.OccurrenceID.IsValid() {
		return invalidPersistedState("mail_delivery", "sitting_projection", errors.New("missing Sitting delivery fence"))
	}
	_, err := tx.Exec(ctx, `UPDATE exam_sitting_mail_recipients SET desired_occurrence_id=NULL,desired_delivery_id=NULL,
		desired_sitting_revision=NULL,desired_template_key=NULL,updated_at=?
		WHERE exam_sitting_id=? AND user_id=? AND desired_occurrence_id=? AND desired_delivery_id=?`,
		model.TimeUTC(at), fence.Sitting.ID.String(), fence.User.ID.String(), fence.OccurrenceID.String(), deliveryID.String())
	if err != nil {
		return fmt.Errorf("clear obsolete Sitting mail recipient projection: %w", err)
	}
	return nil
}

func (s sqlExamSittingStore) CompleteMailExpansion(ctx context.Context, input *store.ExamSittingMailExpansionCompletion) (*store.ExamSittingMailFanoutSnapshot, error) {
	if input == nil || !input.OccurrenceID.IsValid() {
		return nil, store.NewErrInvalidInput("exam_sitting_mail_fanout", "completion", nil)
	}
	result, err := executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*store.ExamSittingMailFanoutSnapshot](true,
		func(_ *store.ExamSittingMailFanoutSnapshot, err error) error {
			return fmt.Errorf("complete Sitting mail expansion: %w", err)
		}),
		func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSittingMailFanoutSnapshot, error) {
			fanout, _, now, err := lockSittingMailFanout(ctx, tx, input.OccurrenceID)
			if err != nil {
				return nil, err
			}
			if fanout.CompletedAt.Valid {
				return fanout, nil
			}
			var bundle struct {
				ID           string `db:"id"`
				PayloadKeyID string `db:"payload_key_id"`
			}
			if err = tx.Get(ctx, &bundle, `SELECT id,payload_key_id FROM mail_fanout_bundles WHERE id=? FOR UPDATE`,
				input.OccurrenceID.String()); err != nil {
				return nil, translateError("mail_fanout_bundle", input.OccurrenceID.String(), err)
			}
			if _, err = tx.Exec(ctx, `UPDATE exam_sitting_mail_fanouts SET bundle_id=NULL,completed_at=?,terminal_reason='completed'
				WHERE occurrence_id=? AND bundle_id=? AND completed_at IS NULL`, now, input.OccurrenceID.String(), bundle.ID); err != nil {
				return nil, fmt.Errorf("complete Sitting mail fan-out: %w", err)
			}
			if _, err = tx.Exec(ctx, `DELETE FROM mail_fanout_bundles WHERE id=?`, bundle.ID); err != nil {
				return nil, fmt.Errorf("destroy Sitting mail fan-out bundle: %w", err)
			}
			if err = decrementMailPayloadKeyReference(ctx, tx, bundle.PayloadKeyID); err != nil {
				return nil, err
			}
			fanout.Bundle = nil
			fanout.CompletedAt = model.OptionalTimeFrom(now)
			return fanout, nil
		})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s sqlExamSittingStore) MaintainMailExpansions(ctx context.Context, limit int) (*store.ExamSittingMailMaintenanceResult, error) {
	if limit < 1 || limit > mailMaintenanceMaximumBatch {
		return nil, store.NewErrInvalidInput("exam_sitting_mail_fanout", "maintenance", nil)
	}
	return executeSQLTransaction(ctx, s.GetMaster().Begin,
		rawSQLTransactionPolicy[*store.ExamSittingMailMaintenanceResult](true,
			func(_ *store.ExamSittingMailMaintenanceResult, err error) error {
				return fmt.Errorf("maintain Sitting mail expansions: %w", err)
			}),
		func(ctx context.Context, tx *sqlxTxWrapper) (*store.ExamSittingMailMaintenanceResult, error) {
			now, err := jobDatabaseNow(ctx, tx)
			if err != nil {
				return nil, err
			}
			result := &store.ExamSittingMailMaintenanceResult{}
			var candidates []struct {
				OccurrenceID string    `db:"occurrence_id"`
				Deadline     time.Time `db:"deadline"`
			}
			if err = tx.Select(ctx, &candidates, `SELECT f.occurrence_id,f.deadline
				FROM exam_sitting_mail_fanouts f LEFT JOIN jobs j
				  ON j.type='mail.expand_sitting' AND j.dedupe_key='sitting-mail:'||f.occurrence_id
				WHERE f.completed_at IS NULL AND
				 (f.deadline<=? OR j.id IS NULL OR j.status IN ('succeeded','failed','canceled'))
				ORDER BY f.deadline,f.occurrence_id LIMIT ? FOR UPDATE OF f SKIP LOCKED`, now, limit+1); err != nil {
				return nil, fmt.Errorf("select terminal Sitting mail expansions: %w", err)
			}
			if len(candidates) > limit {
				result.More = true
				candidates = candidates[:limit]
			}
			for _, candidate := range candidates {
				terminalized, terminalErr := terminalizeSittingMailExpansion(ctx, tx,
					model.MailOccurrenceID(candidate.OccurrenceID), model.TimeUTC(candidate.Deadline), now)
				if terminalErr != nil {
					return nil, terminalErr
				}
				if terminalized {
					result.FanoutsTerminalized++
				}
			}
			remaining := limit - result.FanoutsTerminalized
			if remaining > 0 {
				suppressed, more, suppressErr := suppressTerminalSittingMailChildren(ctx, tx, now, remaining)
				if suppressErr != nil {
					return nil, suppressErr
				}
				result.DeliveriesSuppressed = suppressed
				result.More = result.More || more
			}
			if !result.More {
				if err = tx.Get(ctx, &result.More, `SELECT EXISTS(
					SELECT 1 FROM exam_sitting_mail_fanouts f LEFT JOIN jobs j
					  ON j.type='mail.expand_sitting' AND j.dedupe_key='sitting-mail:'||f.occurrence_id
					WHERE (f.completed_at IS NULL AND (f.deadline<=? OR j.id IS NULL OR j.status IN ('succeeded','failed','canceled')))
					   OR (f.terminal_reason IN ('expired','failed','orphaned') AND EXISTS(
					       SELECT 1 FROM mail_deliveries d WHERE d.occurrence_id=f.occurrence_id AND d.state IN ('queued','sending','failed')))
				)`, now); err != nil {
					return nil, fmt.Errorf("check remaining Sitting mail maintenance: %w", err)
				}
			}
			return result, nil
		})
}

func terminalizeSittingMailExpansion(ctx context.Context, tx *sqlxTxWrapper, occurrenceID model.MailOccurrenceID,
	deadline, now time.Time,
) (bool, error) {
	var bundle struct {
		ID           sql.NullString `db:"bundle_id"`
		PayloadKeyID sql.NullString `db:"payload_key_id"`
	}
	if err := tx.Get(ctx, &bundle, `SELECT f.bundle_id,b.payload_key_id FROM exam_sitting_mail_fanouts f
		LEFT JOIN mail_fanout_bundles b ON b.id=f.bundle_id WHERE f.occurrence_id=? AND f.completed_at IS NULL`,
		occurrenceID.String()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("read terminal Sitting mail expansion: %w", err)
	}
	var jobID string
	jobErr := tx.Get(ctx, &jobID, `SELECT id FROM jobs WHERE type='mail.expand_sitting'
		AND dedupe_key='sitting-mail:'||? FOR UPDATE`, occurrenceID.String())
	var job *model.Job
	if jobErr == nil {
		var err error
		job, err = getJob(ctx, tx, model.JobID(jobID), false)
		if err != nil {
			return false, err
		}
	} else if !errors.Is(jobErr, sql.ErrNoRows) {
		return false, fmt.Errorf("lock Sitting mail expansion Job: %w", jobErr)
	}
	reason := ""
	switch {
	case !now.Before(deadline):
		reason = "expired"
	case job == nil:
		reason = "orphaned"
	case job.Status == model.JobStatusFailed:
		reason = "failed"
	case job.Status == model.JobStatusSucceeded || job.Status == model.JobStatusCanceled:
		reason = "orphaned"
	default:
		return false, nil
	}
	if job != nil && (job.Status == model.JobStatusQueued || job.Status == model.JobStatusRunning) {
		jobAt := model.TimeUTC(now)
		if jobAt.Before(job.UpdatedAt) {
			jobAt = job.UpdatedAt
		}
		updated, err := job.RequestCancellation(jobAt)
		if err != nil {
			return false, invalidPersistedState("job", "sitting_mail_maintenance", err)
		}
		if err = updateJob(ctx, tx, updated); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE exam_sitting_mail_fanouts SET bundle_id=NULL,completed_at=?,terminal_reason=?
		WHERE occurrence_id=? AND completed_at IS NULL`, now, reason, occurrenceID.String()); err != nil {
		return false, fmt.Errorf("terminalize Sitting mail expansion: %w", err)
	}
	if bundle.ID.Valid {
		if _, err := tx.Exec(ctx, `DELETE FROM mail_fanout_bundles WHERE id=?`, bundle.ID.String); err != nil {
			return false, fmt.Errorf("destroy terminal Sitting mail bundle: %w", err)
		}
		if !bundle.PayloadKeyID.Valid {
			return false, invalidPersistedState("mail_fanout_bundle", "payload_key_id", errors.New("missing payload key"))
		}
		if err := decrementMailPayloadKeyReference(ctx, tx, bundle.PayloadKeyID.String); err != nil {
			return false, err
		}
	}
	return true, nil
}

func suppressTerminalSittingMailChildren(ctx context.Context, tx *sqlxTxWrapper, now time.Time, limit int) (int, bool, error) {
	var candidates []struct {
		DeliveryID string `db:"delivery_id"`
		Reason     string `db:"terminal_reason"`
	}
	if err := tx.Select(ctx, &candidates, `SELECT d.id delivery_id,f.terminal_reason
		FROM exam_sitting_mail_fanouts f JOIN mail_deliveries d ON d.occurrence_id=f.occurrence_id
		WHERE f.terminal_reason IN ('expired','failed','orphaned') AND d.state IN ('queued','sending','failed')
		ORDER BY f.completed_at,f.occurrence_id,d.id LIMIT ?`, limit+1); err != nil {
		return 0, false, fmt.Errorf("select terminal Sitting mail children: %w", err)
	}
	more := len(candidates) > limit
	if more {
		candidates = candidates[:limit]
	}
	suppressed := 0
	for _, candidate := range candidates {
		var projectionLock int
		if err := tx.Get(ctx, &projectionLock,
			`SELECT 1 FROM exam_sitting_mail_recipients WHERE desired_delivery_id=? FOR UPDATE`, candidate.DeliveryID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return suppressed, false, fmt.Errorf("lock terminal Sitting mail projection: %w", err)
		}
		var row mailDeliveryRow
		if err := tx.Get(ctx, &row, `SELECT `+mailDeliveryColumns+` FROM mail_deliveries WHERE id=? FOR UPDATE`,
			candidate.DeliveryID); err != nil {
			return suppressed, false, translateError("mail_delivery", candidate.DeliveryID, err)
		}
		current, err := row.model()
		if err != nil {
			return suppressed, false, err
		}
		if current.State != model.MailDeliveryQueued && current.State != model.MailDeliverySending && current.State != model.MailDeliveryFailed {
			continue
		}
		transitionAt := model.TimeUTC(now)
		if transitionAt.Before(current.UpdatedAt) {
			transitionAt = current.UpdatedAt
		}
		code := model.MailDeliveryObsoleteCode
		if candidate.Reason == "expired" {
			code = model.MailDeliveryExpiredCode
		}
		updated, err := current.Suppress(code, transitionAt)
		if err != nil {
			return suppressed, false, invalidPersistedState("mail_delivery", "sitting_mail_maintenance", err)
		}
		if err = updateMailDelivery(ctx, tx, current, updated); err != nil {
			return suppressed, false, err
		}
		if _, err = tx.Exec(ctx, `UPDATE exam_sitting_mail_recipients SET desired_occurrence_id=NULL,desired_delivery_id=NULL,
			desired_sitting_revision=NULL,desired_template_key=NULL,updated_at=? WHERE desired_delivery_id=?`,
			transitionAt, current.ID.String()); err != nil {
			return suppressed, false, fmt.Errorf("clear terminal Sitting mail projection: %w", err)
		}
		job, err := getJob(ctx, tx, current.JobID, true)
		if err != nil {
			return suppressed, false, err
		}
		if job.Status == model.JobStatusQueued || job.Status == model.JobStatusRunning {
			jobAt := transitionAt
			if jobAt.Before(job.UpdatedAt) {
				jobAt = job.UpdatedAt
			}
			updatedJob, cancelErr := job.RequestCancellation(jobAt)
			if cancelErr != nil {
				return suppressed, false, invalidPersistedState("job", "sitting_mail_maintenance", cancelErr)
			}
			if err = updateJob(ctx, tx, updatedJob); err != nil {
				return suppressed, false, err
			}
		}
		suppressed++
	}
	return suppressed, more, nil
}

func (s sqlExamSittingStore) ListMailReconciliationDue(ctx context.Context,
	options store.ExamSittingMailReconciliationOptions,
) ([]store.ExamSittingMailReconciliationCandidate, error) {
	if options.Limit < 1 || options.Limit > model.SittingMailExpansionPageSize ||
		(options.AfterScheduledStartAt.IsZero() != options.AfterSittingID.IsZero()) ||
		(!options.AfterSittingID.IsZero() && !options.AfterSittingID.IsValid()) {
		return nil, store.NewErrInvalidInput("exam_sitting_mail_reconciliation", "list", nil)
	}
	var rows []struct {
		examSittingRow
		ActorUserID string `db:"actor_user_id"`
	}
	query := `SELECT s.id,s.exam_id,s.exam_revision_id,s.class_id,s.scheduled_start_at,s.scheduled_end_at,
		s.state,s.created_at,s.updated_at,s.opened_at,s.paused_at,s.closing_at,s.closed_at,s.canceled_at,s.reason_code,s.revision,
		e.academic_unit_id,current_actor.user_id actor_user_id FROM exam_sittings s
		JOIN exams e ON e.id=s.exam_id
		JOIN LATERAL (SELECT manager.user_id FROM exam_managers manager JOIN users actor ON actor.id=manager.user_id
			WHERE manager.exam_id=e.id AND actor.archived_at IS NULL AND actor.disabled_at IS NULL
			ORDER BY CASE WHEN manager.user_id=s.mail_reconciliation_actor_user_id THEN 0
				WHEN manager.user_id=e.owner_user_id THEN 1 ELSE 2 END,manager.user_id LIMIT 1) current_actor ON TRUE
		WHERE s.state='scheduled' AND s.scheduled_start_at>statement_timestamp()
		AND NOT EXISTS (SELECT 1 FROM exam_sitting_mail_fanouts active WHERE active.exam_sitting_id=s.id AND active.completed_at IS NULL)
		AND (` + sittingMailReconciliationDueSQL("s") + `)
		AND (?::timestamptz IS NULL OR (s.scheduled_start_at,s.id)>(?,?))
		ORDER BY s.scheduled_start_at,s.id LIMIT ?`
	var cursor any
	if !options.AfterScheduledStartAt.IsZero() {
		cursor = model.TimeUTC(options.AfterScheduledStartAt)
	}
	if err := s.GetMaster().Select(ctx, &rows, query, cursor, cursor, nullableID(options.AfterSittingID.String()), options.Limit); err != nil {
		return nil, fmt.Errorf("list Sitting mail reconciliation: %w", err)
	}
	result := make([]store.ExamSittingMailReconciliationCandidate, 0, len(rows))
	for index := range rows {
		sitting, err := rows[index].examSittingRow.model()
		if err != nil {
			return nil, err
		}
		actorID, err := model.ParseUserID(rows[index].ActorUserID)
		if err != nil {
			return nil, invalidPersistedState("exam_sitting_mail_reconciliation", "actor_user_id", err)
		}
		result = append(result, store.ExamSittingMailReconciliationCandidate{Sitting: sitting, ActorUserID: actorID})
	}
	return result, nil
}

func sittingMailReconciliationDueSQL(alias string) string {
	return `EXISTS (
		SELECT 1 FROM class_members cm JOIN users u ON u.id=cm.user_id
		LEFT JOIN exam_sitting_mail_recipients p ON p.exam_sitting_id=` + alias + `.id AND p.user_id=cm.user_id
		WHERE cm.class_id=` + alias + `.class_id AND cm.archived_at IS NULL
		AND cm.start_at<=` + alias + `.scheduled_start_at AND (cm.end_at IS NULL OR cm.end_at>` + alias + `.scheduled_start_at)
		AND u.archived_at IS NULL AND u.disabled_at IS NULL AND u.email_verified=TRUE
		AND (` + alias + `.mail_disabled_suppressed_revision IS DISTINCT FROM ` + alias + `.revision
			OR cm.mail_audience_revision>` + alias + `.mail_disabled_suppressed_audience_revision
			OR u.mail_eligibility_revision>` + alias + `.mail_disabled_suppressed_eligibility_revision)
		AND (p.desired_sitting_revision IS NULL OR p.desired_sitting_revision<` + alias + `.revision
			OR p.desired_template_key NOT IN ('exam.sitting_scheduled','exam.sitting_rescheduled')
			OR EXISTS (SELECT 1 FROM exam_sitting_mail_fanouts stale
				WHERE stale.occurrence_id=p.desired_occurrence_id AND stale.terminal_reason IN ('expired','failed','orphaned')))
		AND (p.communicated_sitting_revision IS NULL OR p.communicated_sitting_revision<` + alias + `.revision
			OR p.communicated_template_key IN ('exam.sitting_assignment_removed','exam.sitting_cancelled')))
	OR EXISTS (SELECT 1 FROM exam_sitting_mail_recipients p JOIN users changed_user ON changed_user.id=p.user_id
		WHERE p.exam_sitting_id=` + alias + `.id AND p.communicated_template_key IN ('exam.sitting_scheduled','exam.sitting_rescheduled')
		AND (` + alias + `.mail_disabled_suppressed_revision IS DISTINCT FROM ` + alias + `.revision OR EXISTS (
			SELECT 1 FROM class_members changed WHERE changed.class_id=` + alias + `.class_id AND changed.user_id=p.user_id
			AND changed.mail_audience_revision>` + alias + `.mail_disabled_suppressed_audience_revision)
			OR changed_user.mail_eligibility_revision>` + alias + `.mail_disabled_suppressed_eligibility_revision)
		AND (p.desired_sitting_revision IS NULL OR p.desired_sitting_revision<` + alias + `.revision
			OR p.desired_template_key<>'exam.sitting_assignment_removed')
		AND NOT EXISTS (SELECT 1 FROM class_members cm JOIN users u ON u.id=cm.user_id
			WHERE cm.class_id=` + alias + `.class_id AND cm.user_id=p.user_id AND cm.archived_at IS NULL
			AND cm.start_at<=` + alias + `.scheduled_start_at AND (cm.end_at IS NULL OR cm.end_at>` + alias + `.scheduled_start_at)
			AND u.archived_at IS NULL AND u.disabled_at IS NULL AND u.email_verified=TRUE))`
}

func (s sqlExamSittingStore) ReconcileMail(ctx context.Context,
	input *store.ExamSittingMailReconciliation,
) (*store.ExamSittingMailFanoutSnapshot, error) {
	if input == nil || !input.SittingID.IsValid() || input.ExpectedRevision < 1 || !input.ActorUserID.IsValid() || input.Mail == nil {
		return nil, store.NewErrInvalidInput("exam_sitting_mail_reconciliation", "value", nil)
	}
	_, err := executeSQLTransaction(ctx, s.GetMaster().Begin, rawSQLTransactionPolicy[*model.ExamSitting](true,
		func(_ *model.ExamSitting, err error) error { return fmt.Errorf("reconcile Sitting mail: %w", err) }),
		func(ctx context.Context, tx *sqlxTxWrapper) (*model.ExamSitting, error) {
			examID, err := resolveExamSittingExamID(ctx, tx, input.SittingID)
			if err != nil {
				return nil, err
			}
			var activeActorID string
			if err = tx.Get(ctx, &activeActorID, `SELECT id FROM users WHERE id=? AND archived_at IS NULL AND disabled_at IS NULL FOR SHARE`,
				input.ActorUserID.String()); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return nil, store.NewErrConflict("exam_sitting_mail_reconciliation", "actor_unavailable", nil)
				}
				return nil, fmt.Errorf("lock Sitting mail reconciliation actor: %w", err)
			}
			disabledEligibilityRevision := int64(0)
			if input.Mail != nil && input.Mail.Bundle == nil {
				disabledEligibilityRevision, err = currentUserMailEligibilityRevision(ctx, tx)
				if err != nil {
					return nil, err
				}
			}
			// User rows precede the shared Class/hierarchy fence throughout
			// invitation acceptance and reconciliation; disabled mail also locks the
			// eligibility singleton between them. This prevents a manager who is also
			// accepting a Class Invitation from forming a User/singleton/Class cycle.
			if err = lockExamSittingLifecycle(ctx, tx); err != nil {
				return nil, err
			}
			if err = guardExamSittingExam(ctx, tx, examID, input.ActorUserID, false); err != nil {
				return nil, err
			}
			current, err := lockExamSitting(ctx, tx, examID, input.SittingID)
			if err != nil {
				return nil, err
			}
			now, err := jobDatabaseNow(ctx, tx)
			if err != nil {
				return nil, err
			}
			var due bool
			if err = tx.Get(ctx, &due, `SELECT state='scheduled' AND scheduled_start_at>? AND (`+
				sittingMailReconciliationDueSQL("exam_sittings")+`) FROM exam_sittings WHERE id=?`, now, current.ID.String()); err != nil {
				return nil, err
			}
			if current.Revision != input.ExpectedRevision || !due {
				return nil, store.NewErrConflict("exam_sitting_mail_reconciliation", "not_due", nil)
			}
			var active bool
			if err = tx.Get(ctx, &active, `SELECT EXISTS(SELECT 1 FROM exam_sitting_mail_fanouts WHERE exam_sitting_id=? AND completed_at IS NULL)`, current.ID.String()); err != nil {
				return nil, err
			}
			if active {
				return nil, store.NewErrConflict("exam_sitting_mail_reconciliation", "active", nil)
			}
			if err = insertExamSittingMailFanout(ctx, tx, input.Mail, current, current.ClassID, input.ActorUserID,
				disabledEligibilityRevision); err != nil {
				return nil, err
			}
			return current, nil
		})
	if err != nil {
		return nil, err
	}
	return s.GetMailFanout(ctx, input.Mail.Occurrence.ID)
}
