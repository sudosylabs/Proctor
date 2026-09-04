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
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(temporaryDirectory) })
	socketPath := filepath.Join(temporaryDirectory, "notify.sock")
	address := &net.UnixAddr{Name: socketPath, Net: "unixgram"}
	listener, err := net.ListenUnixgram("unixgram", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	if err = systemdReadyNotifier(socketPath)(context.Background()); err != nil {
		t.Fatalf("notify readiness: %v", err)
	}
	if err = listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 32)
	count, _, err := listener.ReadFromUnix(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(payload[:count]); got != systemdReadyMessage {
		t.Fatalf("notification = %q, want %q", got, systemdReadyMessage)
	}
}

func TestSystemdReadyNotifierHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := systemdReadyNotifier(filepath.Join(t.TempDir(), "missing.sock"))(ctx); err == nil {
		t.Fatal("notification with canceled context succeeded")
	}
}

func TestSendSystemdReadyNotificationUsesContextDeadlineAndPreservesCloseFailure(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")
	connection := &systemdNotificationConnectionFake{closeErr: closeErr}
	contextDeadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), contextDeadline)
	defer cancel()

	err := sendSystemdReadyNotification(ctx, connection, time.Minute)
	if !errors.Is(err, closeErr) {
		t.Fatalf("notification error = %v, want close failure", err)
	}
	if !connection.deadline.Equal(contextDeadline) {
		t.Fatalf("write deadline = %v, want context deadline %v", connection.deadline, contextDeadline)
	}
	if got := string(connection.payload); got != systemdReadyMessage {
		t.Fatalf("notification = %q, want %q", got, systemdReadyMessage)
	}
	if connection.closeCallCount != 1 {
		t.Fatalf("Close calls = %d, want 1", connection.closeCallCount)
	}
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
		if !errors.Is(err, expected) {
			t.Fatalf("notification error = %v, want %v", err, expected)
		}
	}
	if len(connection.payload) != 0 {
		t.Fatalf("notification payload = %q after deadline failure, want empty", connection.payload)
	}
	if connection.deadline.Before(earliestDeadline) || connection.deadline.After(latestDeadline) {
		t.Fatalf("write deadline = %v, want between %v and %v", connection.deadline, earliestDeadline, latestDeadline)
	}
}

func TestSendSystemdReadyNotificationRejectsShortWrite(t *testing.T) {
	t.Parallel()

	connection := &systemdNotificationConnectionFake{shortWrite: true}
	if err := sendSystemdReadyNotification(context.Background(), connection, time.Second); err == nil {
		t.Fatal("short notification write succeeded")
	}
}
