// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package realtime

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/sudosylabs/proctor/server/model"
)

type accessPolicyChangedData struct {
	Revision int64 `json:"revision"`
}

// NewAccessPolicyChangedEvent creates the content-free refetch signal emitted
// after a durable policy commit. No policy fields or provider facts fan out.
func NewAccessPolicyChangedEvent(institutionID model.InstitutionID, revision int64) (RealtimeEvent, error) {
	if !institutionID.IsValid() || revision < 1 {
		return RealtimeEvent{}, errors.New("Access Policy change event requires valid bounded metadata")
	}
	data, err := json.Marshal(accessPolicyChangedData{Revision: revision})
	if err != nil {
		return RealtimeEvent{}, fmt.Errorf("encode Access Policy change event: %w", err)
	}
	return RealtimeEvent{Name: "access_policy_changed", Action: model.ActionAccessPolicyView,
		Resource: model.Resource{Type: model.ResourceInstitution, ID: institutionID.String()}, Data: data}, nil
}
