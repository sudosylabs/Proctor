// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package model

import (
	"time"
)

// SuspendExamAttemptByManager applies an Exam Manager's kick to an active
// Attempt and its exact active Participation: the Attempt becomes Suspended,
// the generation ends as kicked, and an open Connection, when present, closes
// as kicked. A reconnecting candidate has no open Connection to close. No
// input is mutated unless every ownership and transition check succeeds.
func SuspendExamAttemptByManager(attempt *ExamAttempt, participation *AttemptParticipation,
	connection *AttemptConnection, at time.Time,
) error {
	if attempt == nil || participation == nil || attempt.Validate() != nil || participation.Validate() != nil ||
		participation.AttemptID != attempt.ID || attempt.State != ExamAttemptActive || participation.State != AttemptParticipationActive ||
		(connection != nil && (connection.Validate() != nil || connection.AttemptID != attempt.ID ||
			connection.ParticipationID != participation.ID || connection.State != AttemptConnectionOpen)) {
		return invalidModelError("SuspendExamAttemptByManager", "exam_attempt_manual_suspension", "", "invalid manual Exam Attempt suspension aggregate", "")
	}
	at = TimeUTC(at)
	if at.IsZero() || at.Before(attempt.UpdatedAt) || at.Before(participation.UpdatedAt) ||
		(connection != nil && at.Before(connection.OpenedAt)) {
		return invalidModelError("SuspendExamAttemptByManager", "exam_attempt_manual_suspension", "", "invalid manual Exam Attempt suspension time", "")
	}
	attemptCandidate, participationCandidate := *attempt, *participation
	if err := attemptCandidate.Suspend(at); err != nil {
		return err
	}
	if err := participationCandidate.End(AttemptParticipationEndKicked, at); err != nil {
		return err
	}
	var connectionCandidate AttemptConnection
	if connection != nil {
		connectionCandidate = *connection
		if err := connectionCandidate.Close(AttemptConnectionCloseKicked, at); err != nil {
			return err
		}
	}
	*attempt, *participation = attemptCandidate, participationCandidate
	if connection != nil {
		*connection = connectionCandidate
	}
	return nil
}
