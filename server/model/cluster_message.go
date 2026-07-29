// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only
//
// This file contains a substantially modified adaptation of Mattermost's
// public cluster-message contract. See server/NOTICE for exact provenance.

package model

import (
	"errors"
	"fmt"
)

const (
	MaxClusterEventBytes     = 128
	MaxClusterMessageBytes   = 1 << 20
	MaxClusterMessageProps   = 32
	MaxClusterPropKeyBytes   = 64
	MaxClusterPropValueBytes = 1024
)

type ClusterEvent string

const ClusterEventNone ClusterEvent = "none"

type ClusterSendType string

const (
	ClusterSendBestEffort ClusterSendType = "best_effort"
	ClusterSendReliable   ClusterSendType = "reliable"
)

// ClusterMessage is the application-facing inter-node message. Transports own
// wire metadata such as protocol version, message ID, source, and target node.
type ClusterMessage struct {
	Event    ClusterEvent      `json:"event"`
	SendType ClusterSendType   `json:"send_type"`
	Data     []byte            `json:"data,omitempty"`
	Props    map[string]string `json:"props,omitempty"`
}

func (m *ClusterMessage) Clone() *ClusterMessage {
	if m == nil {
		return nil
	}
	cloned := *m
	cloned.Data = append([]byte(nil), m.Data...)
	if m.Props != nil {
		cloned.Props = make(map[string]string, len(m.Props))
		for key, value := range m.Props {
			cloned.Props[key] = value
		}
	}
	return &cloned
}

func (m *ClusterMessage) Validate() error {
	if m == nil {
		return errors.New("cluster message is nil")
	}
	if !validClusterEvent(m.Event) {
		return fmt.Errorf("invalid cluster event %q", m.Event)
	}
	switch m.SendType {
	case ClusterSendBestEffort, ClusterSendReliable:
	default:
		return fmt.Errorf("invalid cluster send type %q", m.SendType)
	}
	if len(m.Data) > MaxClusterMessageBytes {
		return fmt.Errorf("cluster message data exceeds %d bytes", MaxClusterMessageBytes)
	}
	if len(m.Props) > MaxClusterMessageProps {
		return fmt.Errorf("cluster message properties exceed %d entries", MaxClusterMessageProps)
	}
	for key, value := range m.Props {
		if len(key) == 0 || len(key) > MaxClusterPropKeyBytes || !validClusterName(key) {
			return fmt.Errorf("invalid cluster message property key %q", key)
		}
		if len(value) > MaxClusterPropValueBytes {
			return fmt.Errorf("cluster message property %q exceeds %d bytes", key, MaxClusterPropValueBytes)
		}
	}
	return nil
}

func validClusterEvent(event ClusterEvent) bool {
	value := string(event)
	return event != ClusterEventNone &&
		len(value) > 0 &&
		len(value) <= MaxClusterEventBytes &&
		validClusterName(value)
}

func validClusterName(value string) bool {
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' && character != ':' {
			return false
		}
	}
	return true
}
