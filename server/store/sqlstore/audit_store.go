// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// Adapted from Mattermost server/channels/store/sqlstore/audit_store.go and
// server/public/model/audit_record.go. Proctor persists the richer record,
// uses keyset pagination, and enforces an attempt-only terminal transition.

package sqlstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

type SqlAuditStore struct {
	*SqlStore
	auditsQuery sq.SelectBuilder
}

type auditRow struct {
	ID           string              `db:"id"`
	CreateAt     int64               `db:"create_at"`
	UpdateAt     int64               `db:"update_at"`
	ActorID      sql.NullString      `db:"actor_id"`
	SessionID    sql.NullString      `db:"session_id"`
	Action       string              `db:"action"`
	ResourceType model.ResourceType  `db:"resource_type"`
	ResourceID   string              `db:"resource_id"`
	ScopeType    model.RoleScopeType `db:"scope_type"`
	ScopeID      string              `db:"scope_id"`
	Status       model.AuditStatus   `db:"status"`
	RequestID    string              `db:"request_id"`
	NodeID       string              `db:"node_id"`
	ClientType   string              `db:"client_type"`
	AuthMethod   string              `db:"authentication_method"`
	IPAddress    string              `db:"ip_address"`
	UserAgent    string              `db:"user_agent"`
	ErrorCode    string              `db:"error_code"`
	Parameters   jsonValue           `db:"parameters"`
	PriorState   jsonValue           `db:"prior_state"`
	Result       jsonValue           `db:"result"`
}

func auditSliceColumns() []string {
	return []string{
		"audit_events.id", "audit_events.create_at", "audit_events.update_at",
		"audit_events.actor_id", "audit_events.session_id", "audit_events.action",
		"audit_events.resource_type", "audit_events.resource_id",
		"audit_events.scope_type", "audit_events.scope_id", "audit_events.status",
		"audit_events.request_id", "audit_events.node_id", "audit_events.client_type",
		"audit_events.authentication_method", "audit_events.ip_address",
		"audit_events.user_agent", "audit_events.error_code",
		"audit_events.parameters", "audit_events.prior_state", "audit_events.result",
	}
}

func newSqlAuditStore(sqlStore *SqlStore) store.AuditStore {
	s := &SqlAuditStore{SqlStore: sqlStore}
	s.auditsQuery = s.getQueryBuilder().Select(auditSliceColumns()...).From("audit_events")
	return s
}

func (s SqlAuditStore) Save(
	ctx context.Context,
	event *model.AuditEvent,
) (*model.AuditEvent, error) {
	return insertAuditEvent(ctx, s.GetMaster(), event)
}

func insertAuditEvent(
	ctx context.Context,
	executor sqlxExecutor,
	event *model.AuditEvent,
) (*model.AuditEvent, error) {
	if event == nil {
		return nil, store.NewErrInvalidInput("audit_event", "value", nil)
	}
	if event.Id != "" {
		return nil, store.NewErrInvalidInput("audit_event", "id", event.Id)
	}
	candidate := event.Clone()
	candidate.PreSave()
	if appErr := candidate.IsValid(); appErr != nil {
		return nil, appErr
	}
	row := newAuditRow(candidate)
	if _, err := executor.NamedExec(ctx, `
		INSERT INTO audit_events (
			id, create_at, update_at, actor_id, session_id, action,
			resource_type, resource_id, scope_type, scope_id, status,
			request_id, node_id, client_type, authentication_method,
			ip_address, user_agent, error_code, parameters, prior_state, result
		) VALUES (
			:id, :create_at, :update_at, :actor_id, :session_id, :action,
			:resource_type, :resource_id, :scope_type, :scope_id, :status,
			:request_id, :node_id, :client_type, :authentication_method,
			:ip_address, :user_agent, :error_code, :parameters, :prior_state, :result
		)`, &row); err != nil {
		return nil, fmt.Errorf("save audit event: %w", translateError("audit_event", candidate.Id, err))
	}
	return candidate, nil
}

func (s SqlAuditStore) Get(ctx context.Context, id string) (*model.AuditEvent, error) {
	var row auditRow
	if err := s.GetMaster().GetBuilder(
		ctx, &row, s.auditsQuery.Where(sq.Eq{"audit_events.id": id}),
	); err != nil {
		return nil, translateError("audit_event", id, err)
	}
	return row.model(), nil
}

func (s SqlAuditStore) Complete(
	ctx context.Context,
	id string,
	status model.AuditStatus,
	errorCode string,
	result []byte,
	updateAt int64,
) (*model.AuditEvent, error) {
	return completeAuditEvent(
		ctx, s.GetMaster(), id, status, errorCode, result, updateAt,
	)
}

func completeAuditEvent(
	ctx context.Context,
	executor sqlxExecutor,
	id string,
	status model.AuditStatus,
	errorCode string,
	result []byte,
	updateAt int64,
) (*model.AuditEvent, error) {
	if status != model.AuditStatusSuccess && status != model.AuditStatusFail {
		return nil, store.NewErrInvalidInput("audit_event", "status", status)
	}
	if updateAt <= 0 || len(result) > model.AuditJSONMaxBytes ||
		(len(result) > 0 && !json.Valid(result)) {
		return nil, store.NewErrInvalidInput("audit_event", "completion", nil)
	}
	var row auditRow
	err := executor.Get(ctx, &row, `
		UPDATE audit_events
		   SET update_at = $1, status = $2, error_code = $3, result = $4
		 WHERE id = $5 AND status = 'attempt'
		RETURNING id, create_at, update_at, actor_id, session_id, action,
		          resource_type, resource_id, scope_type, scope_id, status,
		          request_id, node_id, client_type, authentication_method,
		          ip_address, user_agent, error_code, parameters, prior_state, result`,
		updateAt, status, errorCode, nullableJSON(result), id)
	if err != nil {
		return nil, translateError("audit_event", id, err)
	}
	return row.model(), nil
}

func (s SqlAuditStore) List(
	ctx context.Context,
	options store.AuditListOptions,
) ([]*model.AuditEvent, error) {
	if options.Limit < 1 || options.Limit > 200 {
		return nil, store.NewErrInvalidInput("audit_event", "limit", options.Limit)
	}
	query := s.auditsQuery.
		OrderBy("audit_events.create_at DESC", "audit_events.id DESC").
		Limit(uint64(options.Limit))
	if options.ActorId != "" {
		query = query.Where(sq.Eq{"audit_events.actor_id": options.ActorId})
	}
	if options.Action != "" {
		query = query.Where(sq.Eq{"audit_events.action": options.Action})
	}
	if options.Resource != nil {
		query = query.Where(sq.Eq{
			"audit_events.resource_type": options.Resource.Type,
			"audit_events.resource_id":   options.Resource.Id,
		})
	}
	if options.BeforeTime > 0 {
		if options.BeforeId == "" {
			query = query.Where(sq.Lt{"audit_events.create_at": options.BeforeTime})
		} else {
			query = query.Where(sq.Or{
				sq.Lt{"audit_events.create_at": options.BeforeTime},
				sq.And{
					sq.Eq{"audit_events.create_at": options.BeforeTime},
					sq.Lt{"audit_events.id": options.BeforeId},
				},
			})
		}
	}
	rows := []auditRow{}
	if err := s.GetMaster().SelectBuilder(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	events := make([]*model.AuditEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, row.model())
	}
	return events, nil
}

func newAuditRow(event *model.AuditEvent) auditRow {
	return auditRow{
		ID: event.Id, CreateAt: event.CreateAt, UpdateAt: event.UpdateAt,
		ActorID: nullableAuditString(event.ActorId), SessionID: nullableAuditString(event.SessionId),
		Action: event.Action, ResourceType: event.Resource.Type, ResourceID: event.Resource.Id,
		ScopeType: event.ScopeType, ScopeID: event.ScopeId, Status: event.Status,
		RequestID: event.RequestId, NodeID: event.NodeId, ClientType: event.ClientType,
		AuthMethod: event.AuthMethod, IPAddress: event.IPAddress, UserAgent: event.UserAgent,
		ErrorCode: event.ErrorCode, Parameters: jsonValue(event.Parameters),
		PriorState: jsonValue(event.PriorState), Result: jsonValue(event.Result),
	}
}

func (row auditRow) model() *model.AuditEvent {
	return &model.AuditEvent{
		Id: row.ID, CreateAt: row.CreateAt, UpdateAt: row.UpdateAt,
		ActorId: row.ActorID.String, SessionId: row.SessionID.String, Action: row.Action,
		Resource:  model.Resource{Type: row.ResourceType, Id: row.ResourceID},
		ScopeType: row.ScopeType, ScopeId: row.ScopeID, Status: row.Status,
		RequestId: row.RequestID, NodeId: row.NodeID, ClientType: row.ClientType,
		AuthMethod: row.AuthMethod, IPAddress: row.IPAddress, UserAgent: row.UserAgent,
		ErrorCode: row.ErrorCode, Parameters: append([]byte(nil), row.Parameters...),
		PriorState: append([]byte(nil), row.PriorState...), Result: append([]byte(nil), row.Result...),
	}
}

func nullableAuditString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

type jsonValue []byte

func (value jsonValue) Value() (driver.Value, error) {
	if len(value) == 0 {
		return nil, nil
	}
	return string(value), nil
}

func (value *jsonValue) Scan(source any) error {
	switch typed := source.(type) {
	case nil:
		*value = nil
	case []byte:
		*value = append((*value)[:0], typed...)
	case string:
		*value = append((*value)[:0], typed...)
	default:
		return fmt.Errorf("scan JSON value from %T", source)
	}
	return nil
}

var _ store.AuditStore = (*SqlAuditStore)(nil)
