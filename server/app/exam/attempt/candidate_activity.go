// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package attempt

import (
	"context"
	"errors"
	"time"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
	"github.com/sudosylabs/proctor/server/store"
)

// CandidateActivityQuery selects one page of the caller's own exam activity.
// BeforeScheduledStart and BeforeSittingID are the keyset cursor of the last
// Sitting already seen; zero values start at the newest scheduled start.
type CandidateActivityQuery struct {
	BeforeScheduledStart time.Time
	BeforeSittingID      model.ExamSittingID
	Limit                int
}

// CandidateActivityPage is one page of the caller's own Sittings and Attempts,
// stamped with authoritative server time. HasMore reports that a later page
// exists.
type CandidateActivityPage struct {
	ServerTime time.Time
	Items      []store.CandidateExamActivityItem
	HasMore    bool
}

// ListCandidateExamActivity returns one page of the caller's own Sittings and
// Attempts, newest scheduled start first. On a registered desktop the page
// also reflects that desktop's Session and registration.
func (s *Service) ListCandidateExamActivity(ctx context.Context, invocation invocation.Invocation, query CandidateActivityQuery) (_ CandidateActivityPage, resultErr error) {
	defer func() { resultErr = publicFailures.Conceal(resultErr) }()
	principal := invocation.Principal()
	if principal.Validate() != nil || principal.CredentialType != model.CredentialSessionAccess || query.Limit < 1 || query.Limit > 200 ||
		(query.BeforeScheduledStart.IsZero() != query.BeforeSittingID.IsZero()) {
		return CandidateActivityPage{}, apperror.Invalid("exam.attempt.invalid", "candidate_activity")
	}
	options := store.CandidateExamActivityListOptions{CandidateUserID: principal.UserID,
		BeforeScheduledStart: model.TimeUTC(query.BeforeScheduledStart), BeforeSittingID: query.BeforeSittingID, Limit: query.Limit + 1}
	if principal.HasRegisteredDesktopKey() {
		options.DesktopSessionID = principal.SessionID
		options.DesktopRegistrationID = principal.DesktopRegistrationID
		options.DPoPKeyThumbprint = principal.DPoPKeyThumbprint
	}
	stored, err := s.deps.Persistence.ListCandidateActivity(ctx, options)
	if err != nil {
		return CandidateActivityPage{}, storeError(storeErrors, err)
	}
	if stored == nil || stored.ServerTime.IsZero() || stored.Items == nil {
		return CandidateActivityPage{}, apperror.New("exam.attempt.unavailable").Wrap(errors.New("incomplete candidate activity projection"))
	}
	page := CandidateActivityPage{ServerTime: model.TimeUTC(stored.ServerTime),
		Items: make([]store.CandidateExamActivityItem, 0, min(len(stored.Items), query.Limit)), HasMore: len(stored.Items) > query.Limit}
	for index, item := range stored.Items {
		if index == query.Limit {
			break
		}
		item.AllowedActions = append([]store.CandidateExamAllowedAction(nil), item.AllowedActions...)
		page.Items = append(page.Items, item)
	}
	return page, nil
}
