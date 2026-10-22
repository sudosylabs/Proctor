// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sudosylabs/proctor/server/app/apperror"
	"github.com/sudosylabs/proctor/server/config"
)

func testPasswordPolicy() PasswordPolicy {
	settings := config.Default().Authentication.Password
	return PasswordPolicy{
		MinimumLength:               settings.MinimumLength,
		MaximumLength:               settings.MaximumLength,
		MaximumConcurrentOperations: 2,
		ArgonMemoryKiB:              19 * 1024,
		ArgonIterations:             1,
		ArgonParallelism:            1,
		ArgonSaltBytes:              settings.ArgonSaltBytes,
		ArgonKeyBytes:               settings.ArgonKeyBytes,
	}
}

func TestPasswordHasherRoundTripAndRehash(t *testing.T) {
	settings := testPasswordPolicy()
	hasher, err := newPasswordHasher(settings, nil)
	require.NoError(t, err)
	encoded, err := hasher.Hash(context.Background(), "correct horse battery staple")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$"), "Hash() = %q", encoded)
	require.NoError(t, hasher.Verify(t.Context(), encoded, "correct horse battery staple"))
	require.ErrorIs(t, hasher.Verify(t.Context(), encoded, "wrong password"), ErrPasswordMismatch)
	require.False(t, hasher.NeedsRehash(encoded), "fresh hash needs rehash")

	stronger := settings
	stronger.ArgonIterations = 2
	strongerHasher, err := newPasswordHasher(stronger, nil)
	require.NoError(t, err)
	require.True(t, strongerHasher.NeedsRehash(encoded), "changed parameters did not require rehash")
}

func TestPasswordHasherRejectsUnsafeInputsAndParameters(t *testing.T) {
	settings := testPasswordPolicy()
	hasher, err := newPasswordHasher(settings, nil)
	require.NoError(t, err)
	_, err = hasher.Hash(context.Background(), "short")
	require.Error(t, err, "Hash(short) succeeded")
	oversized := strings.Repeat("a", settings.MaximumLength+1)
	_, err = hasher.Hash(context.Background(), oversized)
	require.Error(t, err, "Hash(oversized) succeeded")
	hostile := "$argon2id$v=19$m=4294967295,t=20,p=64$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5"
	require.ErrorIs(t, hasher.Verify(context.Background(), hostile, "password"), ErrPasswordMismatch)
}

func TestPasswordHasherRequiresPositiveWorkCapacity(t *testing.T) {
	for _, maximum := range []int{0, -1} {
		policy := testPasswordPolicy()
		policy.MaximumConcurrentOperations = maximum
		_, err := newPasswordHasher(policy, nil)
		require.Error(t, err, "capacity %d was accepted", maximum)
	}
}

func TestPasswordHasherReleasesCapacityAfterMismatch(t *testing.T) {
	policy := testPasswordPolicy()
	policy.MaximumConcurrentOperations = 1
	hasher, err := newPasswordHasher(policy, nil)
	require.NoError(t, err)
	for range 2 {
		require.ErrorIs(t, hasher.Verify(context.Background(), hasher.dummyHash, "incorrect password"), ErrPasswordMismatch)
	}
	require.NoError(t, hasher.VerifyDummy(context.Background(), "incorrect password"))
}

func TestPasswordHasherSharesCapacityAndRetainsCanceledWorkUntilReturn(t *testing.T) {
	for _, operation := range []string{"hash", "verify", "verify mismatch", "dummy"} {
		t.Run(operation, func(t *testing.T) {
			entered, proceed := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(proceed) })
			t.Cleanup(release)
			announce := sync.OnceFunc(func() { close(entered) })
			recorder := &passwordWorkRecorderFake{onStart: func() { announce(); <-proceed }}
			policy := testPasswordPolicy()
			policy.MaximumConcurrentOperations = 1
			hasher, err := newPasswordHasher(policy, recorder)
			require.NoError(t, err)
			require.Equal(t, int64(0), recorder.started.Load(), "startup timing hash was counted as runtime work")
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() {
				switch operation {
				case "hash":
					_, err := hasher.Hash(ctx, "correct horse battery staple")
					done <- err
				case "verify":
					done <- hasher.Verify(ctx, hasher.dummyHash, "proctor-dummy-password-never-used")
				case "verify mismatch":
					done <- hasher.Verify(ctx, hasher.dummyHash, "incorrect password")
				case "dummy":
					done <- hasher.VerifyDummy(ctx, "incorrect password")
				}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				require.FailNow(t, "password work did not start")
			}
			checkBusy := func() {
				t.Helper()
				_, hashErr := hasher.Hash(context.Background(), "correct horse battery staple")
				verifyErr := hasher.Verify(context.Background(), hasher.dummyHash, "incorrect password")
				dummyErr := hasher.VerifyDummy(context.Background(), "incorrect password")
				for _, err := range []error{hashErr, verifyErr, dummyErr} {
					require.True(t, apperror.Is(err, "service.busy"), "saturated password operation = %v, want service.busy", err)
				}
			}
			checkBusy()
			cancel()
			select {
			case err := <-done:
				require.FailNow(t, fmt.Sprintf("canceled password work returned before synchronous work finished: %v", err))
			default:
			}
			checkBusy()
			_, hashErr := hasher.Hash(ctx, "short")
			verifyErr := hasher.Verify(ctx, "malformed", "incorrect password")
			dummyErr := hasher.VerifyDummy(ctx, "incorrect password")
			for _, err := range []error{hashErr, verifyErr, dummyErr} {
				require.ErrorIs(t, err, context.Canceled, "canceled request")
			}
			_, err = hasher.Hash(context.Background(), "short")
			require.True(t, apperror.Is(err, "authentication.password.invalid"), "invalid password consumed capacity: %v", err)
			require.ErrorIs(t, hasher.Verify(context.Background(), "malformed", "incorrect password"), ErrPasswordMismatch)
			release()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				require.FailNow(t, "canceled password work did not finish")
			}
			require.NoError(t, hasher.VerifyDummy(context.Background(), "incorrect password"))
			require.Equal(t, int64(2), recorder.started.Load(), "work metrics = started")
			require.Equal(t, int64(2), recorder.finished.Load(), "work metrics = started")
			require.Equal(t, int64(6), recorder.rejected.Load(), "work metrics = started")
			require.Positive(t, recorder.duration.Load(), "work metrics = started")
		})
	}
}

type passwordWorkRecorderFake struct {
	started, finished, rejected atomic.Int64
	duration                    atomic.Int64
	onStart                     func()
}

func (r *passwordWorkRecorderFake) Started() {
	r.started.Add(1)
	if r.onStart != nil {
		r.onStart()
	}
}

func (r *passwordWorkRecorderFake) Finished(elapsed time.Duration) {
	r.finished.Add(1)
	r.duration.Add(int64(elapsed))
}

func (r *passwordWorkRecorderFake) Rejected() { r.rejected.Add(1) }

func saturatePasswordWork(t *testing.T, hasher *passwordHasher) func() {
	t.Helper()
	var releases []func()
	for range cap(hasher.work) {
		release, err := hasher.admit(context.Background())
		require.NoError(t, err)
		releases = append(releases, release)
	}
	release := sync.OnceFunc(func() {
		for _, release := range releases {
			release()
		}
	})
	t.Cleanup(release)
	return release
}

type passwordHashFunc func(context.Context, string) (string, error)

func (h passwordHashFunc) Hash(ctx context.Context, password string) (string, error) {
	return h(ctx, password)
}

func testPasswordHashFailures(t *testing.T, unavailableCode string, invoke func(*testing.T, context.Context, passwordHash) error) {
	t.Helper()
	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "busy", err: apperror.New("service.busy"), code: "service.busy"},
		{name: "canceled", err: context.Canceled, code: unavailableCode},
		{name: "operational failure", err: errors.New("password work unavailable"), code: unavailableCode},
		{name: "invalid password", err: apperror.New("authentication.password.invalid"), code: "authentication.password.invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			hasher := passwordHashFunc(func(got context.Context, _ string) (string, error) {
				calls++
				require.Equal(t, ctx, got, "password work did not receive the request context")
				return "", test.err
			})
			err := invoke(t, ctx, hasher)
			require.True(t, apperror.Is(err, test.code), "password failure = %v, calls %d; want %s wrapping %v after one call", err, calls, test.code, test.err)
			require.Equal(t, 1, calls, "password failure = %v, calls %d; want %s wrapping %v after one call", err, calls, test.code, test.err)
			require.ErrorIs(t, err, test.err, "password failure = %v, calls %d; want %s wrapping %v after one call", err, calls, test.code, test.err)
		})
	}
}
