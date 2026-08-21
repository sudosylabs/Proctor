// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

// Package mail owns server-specific transactional-mail composition.
//
// It validates each catalog family's meaning, renders only bounded safe data,
// freezes and encrypts recoverable payloads, records disabled or ineligible
// suppression, and prepares direct or Sitting fan-out outbox records. It does
// not authorize callers, resolve recipients, persist records, send mail during
// a business transaction, select infrastructure, or import its parent app.
// Callers commit prepared records with the originating fact and audit through
// a named Store aggregate operation.
package mail
