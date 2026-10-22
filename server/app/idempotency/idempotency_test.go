// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package idempotency

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/app/invocation"
	"github.com/sudosylabs/proctor/server/model"
)

func caller(userID model.UserID) invocation.Invocation {
	return invocation.New(model.Principal{UserID: userID}, model.RequestMetadata{})
}

func TestPreparePinsCommandIdentity(t *testing.T) {
	t.Parallel()
	type semantics struct {
		ExamID string `json:"exam_id"`
		Title  string `json:"title"`
	}
	userID := model.NewUserID()
	got, err := Prepare(caller(userID), "exam.create.v1", "retry-key", semantics{ExamID: "exam-1", Title: "Networks"})
	require.NoError(t, err)
	wantKey := sha256.Sum256([]byte("retry-key"))
	wantFingerprint := sha256.Sum256([]byte("exam.create.v1\x00{\"exam_id\":\"exam-1\",\"title\":\"Networks\"}"))
	require.Equal(t, userID, got.UserID)
	require.Equal(t, "exam.create.v1", got.Operation)
	require.Equal(t, wantKey, got.KeyDigest)
	require.Equal(t, wantFingerprint, got.Fingerprint)
	require.Equal(t, 24*time.Hour, got.Retention)
	require.Equal(t, 2*time.Second, got.Wait)
}

func TestPrepareEmptyKey(t *testing.T) {
	t.Parallel()
	got, err := Prepare(caller("invalid is irrelevant"), "operation", "", make(chan int))
	require.NoError(t, err, "Prepare empty key")
	require.Nil(t, got, "Prepare empty key")
}

func TestPrepareRejectsInvalidPrincipal(t *testing.T) {
	t.Parallel()
	_, err := Prepare(caller(""), "operation", "key", struct{}{})
	require.True(t, apperror.Is(err, "idempotency.key_invalid"), "Prepare error = %v; want idempotency.key_invalid", err)
}

func TestPrepareWrapsSemanticEncodingFailure(t *testing.T) {
	t.Parallel()
	_, err := Prepare(caller(model.NewUserID()), "operation", "key", struct {
		Unsupported chan int `json:"unsupported"`
	}{Unsupported: make(chan int)})
	require.True(t, apperror.Is(err, "request.invalid"), "Prepare error = %v; want request.invalid", err)
}

func TestRequireRejectsEmptyKeyBeforeEncoding(t *testing.T) {
	t.Parallel()
	got, err := Require(caller(""), "operation", "", make(chan int))
	require.Nil(t, got, "Require empty key = %#v, %v; want idempotency.key_required", got, err)
	require.True(t, apperror.Is(err, "idempotency.key_required"), "Require empty key = %#v, %v; want idempotency.key_required", got, err)
}
