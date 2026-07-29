// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: Apache-2.0
//
// Adapted from Mattermost server/public/model/audit_record.go. Proctor makes
// the rich attempt/result record durable and adds explicit resource, academic
// scope, request, node, and authentication fields.

package model

import (
	"encoding/json"
	"net/netip"
	"strings"
)

const AuditJSONMaxBytes = 16 * 1024

type AuditStatus string

const (
	AuditStatusAttempt AuditStatus = "attempt"
	AuditStatusSuccess AuditStatus = "success"
	AuditStatusFail    AuditStatus = "fail"
)

// AuditEvent is an append-oriented security record. Critical mutations are
// inserted as attempts and may transition exactly once; point-in-time
// authorization decisions are inserted directly with their terminal result.
type AuditEvent struct {
	Id         string          `json:"id"`
	CreateAt   int64           `json:"create_at"`
	UpdateAt   int64           `json:"update_at"`
	ActorId    string          `json:"actor_id,omitempty"`
	SessionId  string          `json:"session_id,omitempty"`
	Action     string          `json:"action"`
	Resource   Resource        `json:"resource"`
	ScopeType  RoleScopeType   `json:"scope_type"`
	ScopeId    string          `json:"scope_id"`
	Status     AuditStatus     `json:"status"`
	RequestId  string          `json:"request_id,omitempty"`
	NodeId     string          `json:"node_id"`
	ClientType string          `json:"client_type,omitempty"`
	AuthMethod string          `json:"authentication_method,omitempty"`
	IPAddress  string          `json:"ip_address,omitempty"`
	UserAgent  string          `json:"user_agent,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	Parameters json.RawMessage `json:"parameters,omitempty"`
	PriorState json.RawMessage `json:"prior_state,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

type AuditQuery struct {
	ActorId    string
	Action     string
	Resource   *Resource
	BeforeTime int64
	BeforeId   string
	Limit      int
}

func (ae *AuditEvent) PreSave() {
	preSave(&ae.Id, &ae.CreateAt, &ae.UpdateAt)
	ae.Action = strings.TrimSpace(ae.Action)
	ae.RequestId = boundedString(strings.TrimSpace(ae.RequestId), 128)
	ae.NodeId = boundedString(strings.TrimSpace(ae.NodeId), 128)
	ae.ClientType = boundedString(strings.TrimSpace(ae.ClientType), 32)
	ae.AuthMethod = boundedString(strings.TrimSpace(ae.AuthMethod), 64)
	ae.IPAddress = boundedString(strings.TrimSpace(ae.IPAddress), 64)
	ae.UserAgent = boundedString(strings.TrimSpace(ae.UserAgent), 512)
	ae.ErrorCode = boundedString(strings.TrimSpace(ae.ErrorCode), 128)
	ae.Parameters = cloneRawMessage(ae.Parameters)
	ae.PriorState = cloneRawMessage(ae.PriorState)
	ae.Result = cloneRawMessage(ae.Result)
}

func (ae *AuditEvent) IsValid() *AppError {
	const where = "AuditEvent.IsValid"
	if appErr := validatePersistentFields(where, "audit_event", ae.Id, ae.CreateAt, ae.UpdateAt); appErr != nil {
		return appErr
	}
	details := "id=" + ae.Id
	if ae.ActorId != "" && !IsValidId(ae.ActorId) {
		return invalidModelError(where, "audit_event", "actor_id", "must be empty or a valid identifier", details)
	}
	if ae.SessionId != "" && !IsValidId(ae.SessionId) {
		return invalidModelError(where, "audit_event", "session_id", "must be empty or a valid identifier", details)
	}
	if len(ae.Action) == 0 || len(ae.Action) > RolePermissionMaxLength || !validPermission.MatchString(ae.Action) {
		return invalidModelError(where, "audit_event", "action", "must be a valid action", details)
	}
	if !ae.Resource.IsValid() {
		return invalidModelError(where, "audit_event", "resource", "must identify a valid resource", details)
	}
	if !ae.ScopeType.IsValid() || !IsValidId(ae.ScopeId) {
		return invalidModelError(where, "audit_event", "scope", "must identify a valid authorization scope", details)
	}
	if ae.Status != AuditStatusAttempt && ae.Status != AuditStatusSuccess && ae.Status != AuditStatusFail {
		return invalidModelError(where, "audit_event", "status", "has an unknown value", details)
	}
	if ae.NodeId == "" {
		return invalidModelError(where, "audit_event", "node_id", "must not be empty", details)
	}
	if ae.IPAddress != "" {
		if _, err := netip.ParseAddr(ae.IPAddress); err != nil {
			return invalidModelError(where, "audit_event", "ip_address", "must be a valid IP address", details)
		}
	}
	for field, value := range map[string]json.RawMessage{
		"parameters":  ae.Parameters,
		"prior_state": ae.PriorState,
		"result":      ae.Result,
	} {
		if len(value) > AuditJSONMaxBytes || (len(value) > 0 && !json.Valid(value)) {
			return invalidModelError(where, "audit_event", field, "must be bounded valid JSON", details)
		}
	}
	return nil
}

func (ae *AuditEvent) Clone() *AuditEvent {
	if ae == nil {
		return nil
	}
	cloned := *ae
	cloned.Parameters = cloneRawMessage(ae.Parameters)
	cloned.PriorState = cloneRawMessage(ae.PriorState)
	cloned.Result = cloneRawMessage(ae.Result)
	return &cloned
}

func EncodeAuditData(value any) (json.RawMessage, *AppError) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > AuditJSONMaxBytes {
		return nil, invalidModelError(
			"EncodeAuditData", "audit_event", "data", "must be JSON encodable and bounded", "",
		)
	}
	return encoded, nil
}

func boundedString(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "")
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
