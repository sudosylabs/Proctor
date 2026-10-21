// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package commands

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type systemdNotificationConnectionFake struct {
	deadline       time.Time
	deadlineErr    error
	payload        []byte
	writeErr       error
	shortWrite     bool
	closeErr       error
	closeCallCount int
}

func (c *systemdNotificationConnectionFake) SetWriteDeadline(deadline time.Time) error {
	c.deadline = deadline
	return c.deadlineErr
}

func (c *systemdNotificationConnectionFake) Write(payload []byte) (int, error) {
	c.payload = append([]byte(nil), payload...)
	if c.shortWrite {
		return len(payload) - 1, c.writeErr
	}
	return len(payload), c.writeErr
}

func (c *systemdNotificationConnectionFake) Close() error {
	c.closeCallCount++
	return c.closeErr
}

func TestSystemdReadyNotifierSendsReadyDatagram(t *testing.T) {
	t.Parallel()

	temporaryDirectory, err := os.MkdirTemp("/tmp", "proctor-systemd-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(temporaryDirectory) })
	socketPath := filepath.Join(temporaryDirectory, "notify.sock")
	address := &net.UnixAddr{Name: socketPath, Net: "unixgram"}
	listener, err := net.ListenUnixgram("unixgram", address)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	err = systemdReadyNotifier(socketPath)(context.Background())
	require.NoError(t, err)
	err = listener.SetReadDeadline(time.Now().Add(time.Second))
	require.NoError(t, err)
	payload := make([]byte, 32)
	count, _, err := listener.ReadFromUnix(payload)
	require.NoError(t, err)
	require.Equal(t, systemdReadyMessage, string(payload[:count]), "notification")
}

func TestSystemdReadyNotifierHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, systemdReadyNotifier(filepath.Join(t.TempDir(), "missing.sock"))(ctx), "notification with canceled context succeeded")
}

func TestSendSystemdReadyNotificationUsesContextDeadlineAndPreservesCloseFailure(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")
	connection := &systemdNotificationConnectionFake{closeErr: closeErr}
	contextDeadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), contextDeadline)
	defer cancel()

	err := sendSystemdReadyNotification(ctx, connection, time.Minute)
	require.ErrorIs(t, err, closeErr, "notification error")
	require.True(t, connection.deadline.Equal(contextDeadline), "write deadline = %v, want context deadline", connection.deadline)
	require.Equal(t, systemdReadyMessage, string(connection.payload), "notification")
	require.Equal(t, 1, connection.closeCallCount, "Close calls")
}

func TestSendSystemdReadyNotificationJoinsDeadlineAndCloseFailures(t *testing.T) {
	t.Parallel()

	deadlineErr := errors.New("deadline failed")
	closeErr := errors.New("close failed")
	connection := &systemdNotificationConnectionFake{deadlineErr: deadlineErr, closeErr: closeErr}

	timeout := time.Second
	earliestDeadline := time.Now().Add(timeout)
	err := sendSystemdReadyNotification(context.Background(), connection, timeout)
	latestDeadline := time.Now().Add(timeout)
	for _, expected := range []error{deadlineErr, closeErr} {
		require.ErrorIs(t, err, expected, "notification error")
	}
	require.Empty(t, connection.payload, "notification payload")
	require.False(t, connection.deadline.Before(earliestDeadline), "write deadline = %v, want between %v and", connection.deadline, earliestDeadline)
	require.False(t, connection.deadline.After(latestDeadline), "write deadline = %v, want between %v and", connection.deadline, earliestDeadline)
}

func TestSendSystemdReadyNotificationRejectsShortWrite(t *testing.T) {
	t.Parallel()

	connection := &systemdNotificationConnectionFake{shortWrite: true}
	require.Error(t, sendSystemdReadyNotification(context.Background(), connection, time.Second), "short notification write succeeded")
}
