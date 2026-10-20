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
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	wantKey := sha256.Sum256([]byte("retry-key"))
	wantFingerprint := sha256.Sum256([]byte("exam.create.v1\x00v1\x00{\"exam_id\":\"exam-1\",\"title\":\"Networks\"}"))
	if got.UserID != userID || got.Operation != "exam.create.v1" || got.KeyDigest != wantKey || got.Fingerprint != wantFingerprint {
		t.Fatalf("unexpected command identity: %#v", got)
	}
	if got.FingerprintVersion != 1 || got.OutcomeVersion != 1 || got.Retention != 24*time.Hour || got.Wait != 2*time.Second {
		t.Fatalf("unexpected command policy: %#v", got)
	}
}

func TestPrepareEmptyKey(t *testing.T) {
	t.Parallel()
	got, err := Prepare(caller("invalid is irrelevant"), "operation", "", make(chan int))
	if err != nil || got != nil {
		t.Fatalf("Prepare empty key = %#v, %v; want nil, nil", got, err)
	}
}

func TestPrepareRejectsInvalidPrincipal(t *testing.T) {
	t.Parallel()
	_, err := Prepare(caller(""), "operation", "key", struct{}{})
	if !apperror.Is(err, "idempotency.key_invalid") {
		t.Fatalf("Prepare error = %v; want idempotency.key_invalid", err)
	}
}

func TestPrepareWrapsSemanticEncodingFailure(t *testing.T) {
	t.Parallel()
	_, err := Prepare(caller(model.NewUserID()), "operation", "key", struct {
		Unsupported chan int `json:"unsupported"`
	}{Unsupported: make(chan int)})
	if !apperror.Is(err, "request.invalid") {
		t.Fatalf("Prepare error = %v; want request.invalid", err)
	}
}

func TestRequireRejectsEmptyKeyBeforeEncoding(t *testing.T) {
	t.Parallel()
	got, err := Require(caller(""), "operation", "", make(chan int))
	if got != nil || !apperror.Is(err, "idempotency.key_required") {
		t.Fatalf("Require empty key = %#v, %v; want idempotency.key_required", got, err)
	}
}
