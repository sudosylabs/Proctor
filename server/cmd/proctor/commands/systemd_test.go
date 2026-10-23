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
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

// notificationRecorder collects the messages a supervisor sends.
type notificationRecorder struct {
	mu       sync.Mutex
	messages []string
}

func (r *notificationRecorder) notify(_ context.Context, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, message)
	return nil
}

func (r *notificationRecorder) count(message string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, sent := range r.messages {
		if sent == message {
			count++
		}
	}
	return count
}

func (r *notificationRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.messages...)
}

func environment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestSystemdSupervisorSendsReadyDatagram(t *testing.T) {
	t.Parallel()

	temporaryDirectory, err := os.MkdirTemp("/tmp", "proctor-systemd-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(temporaryDirectory) })
	socketPath := filepath.Join(temporaryDirectory, "notify.sock")
	address := &net.UnixAddr{Name: socketPath, Net: "unixgram"}
	listener, err := net.ListenUnixgram("unixgram", address)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	supervisor := newSystemdSupervisor(environment(map[string]string{"NOTIFY_SOCKET": socketPath}), os.Getpid())
	require.NotNil(t, supervisor, "supervisor under NOTIFY_SOCKET")
	require.NoError(t, supervisor.ready(context.Background()))
	require.NoError(t, listener.SetReadDeadline(time.Now().Add(time.Second)))
	payload := make([]byte, 32)
	count, _, err := listener.ReadFromUnix(payload)
	require.NoError(t, err)
	require.Equal(t, systemdReadyMessage, string(payload[:count]), "notification")
}

func TestSystemdSupervisorHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	notify := systemdNotifier(filepath.Join(t.TempDir(), "missing.sock"))
	require.Error(t, notify(ctx, systemdReadyMessage), "notification with canceled context succeeded")
}

func TestSystemdSupervisorReadsTheWatchdogThisProcessOwns(t *testing.T) {
	t.Parallel()

	const pid = 4242
	for _, test := range []struct {
		name     string
		values   map[string]string
		interval time.Duration
	}{
		{"watchdog for this process", map[string]string{"WATCHDOG_USEC": "30000000", "WATCHDOG_PID": "4242"}, 30 * time.Second},
		{"watchdog without a pid", map[string]string{"WATCHDOG_USEC": "60000000"}, time.Minute},
		{"watchdog for another process", map[string]string{"WATCHDOG_USEC": "30000000", "WATCHDOG_PID": "1"}, 0},
		{"no watchdog", map[string]string{}, 0},
		{"malformed interval", map[string]string{"WATCHDOG_USEC": "soon"}, 0},
		{"zero interval", map[string]string{"WATCHDOG_USEC": "0"}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.values["NOTIFY_SOCKET"] = "/run/systemd/notify"
			supervisor := newSystemdSupervisor(environment(test.values), pid)
			require.NotNil(t, supervisor, "supervisor under NOTIFY_SOCKET")
			require.Equal(t, test.interval, supervisor.watchdogInterval, "watchdog interval")
		})
	}
}

func TestSystemdSupervisorIsAbsentWithoutANotificationSocket(t *testing.T) {
	t.Parallel()

	supervisor := newSystemdSupervisor(environment(map[string]string{"WATCHDOG_USEC": "30000000"}), os.Getpid())
	require.Nil(t, supervisor)
	reloaded := false
	require.NoError(t, supervisor.reload(context.Background(), func(context.Context) error {
		reloaded = true
		return nil
	}))
	require.True(t, reloaded, "reload without a supervisor did not run")
	supervisor.keepAlive(make(chan struct{}), func() bool { return true })
}

func TestSystemdSupervisorBracketsReloadAndReportsReadyAfterAFailure(t *testing.T) {
	t.Parallel()

	recorder := &notificationRecorder{}
	supervisor := &systemdSupervisor{
		notify:       recorder.notify,
		monotonicNow: func() (time.Duration, error) { return 1500 * time.Millisecond, nil },
	}
	reloadErr := errors.New("invalid configuration")
	err := supervisor.reload(context.Background(), func(ctx context.Context) error {
		_ = recorder.notify(ctx, "reload")
		return reloadErr
	})
	require.ErrorIs(t, err, reloadErr)
	require.Equal(t, []string{"RELOADING=1\nMONOTONIC_USEC=1500000", "reload", systemdReadyMessage}, recorder.snapshot())
}

func TestSystemdSupervisorReportsShutdownStart(t *testing.T) {
	t.Parallel()

	recorder := &notificationRecorder{}
	supervisor := &systemdSupervisor{notify: recorder.notify}
	require.NoError(t, supervisor.beginStopping(context.Background()))
	require.Equal(t, []string{systemdStoppingMessage}, recorder.snapshot())
	require.True(t, supervisor.stopping.Load(), "supervisor did not record shutdown")
}

func TestMonotonicClockAdvances(t *testing.T) {
	t.Parallel()

	first, err := monotonicClock()
	require.NoError(t, err)
	require.Positive(t, first)
	time.Sleep(time.Millisecond)
	second, err := monotonicClock()
	require.NoError(t, err)
	require.Greater(t, second, first)
}

func TestSystemdWatchdogKeepsAliveOnlyWhileReadyOrStopping(t *testing.T) {
	t.Parallel()

	const interval = 80 * time.Millisecond
	for _, test := range []struct {
		name     string
		ready    bool
		stopping bool
		wantPets bool
	}{
		{"ready node", true, false, true},
		{"unready node", false, false, false},
		{"draining node", false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &notificationRecorder{}
			supervisor := &systemdSupervisor{notify: recorder.notify, watchdogInterval: interval}
			supervisor.stopping.Store(test.stopping)
			done := make(chan struct{})
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				supervisor.keepAlive(done, func() bool { return test.ready })
			}()
			time.Sleep(3 * interval)
			close(done)
			<-stopped
			pets := recorder.count(systemdWatchdogMessage)
			if test.wantPets {
				// Six intervals elapse; a loaded host may coalesce ticks.
				require.GreaterOrEqual(t, pets, 2, "keep-alives at half the watchdog interval")
				return
			}
			require.Zero(t, pets, "keep-alives from a node that never became ready")
		})
	}
}

func TestSystemdWatchdogToleratesABriefReadinessDip(t *testing.T) {
	t.Parallel()

	gate := watchdogGate{}
	ready := func(value bool) func() bool { return func() bool { return value } }
	// A blip: unready, then ready again before the keep-alive.
	gate.sample(ready(false))
	gate.sample(ready(true))
	gate.sample(ready(false))
	require.True(t, gate.due(false), "keep-alive after one ready sample in the interval")
	// The next interval starts over; only failed samples withhold it.
	gate.sample(ready(false))
	gate.sample(ready(false))
	require.False(t, gate.due(false), "keep-alive for an interval with no ready sample")
	// A draining node keeps the watchdog fed whatever readiness says.
	gate.sample(ready(false))
	require.True(t, gate.due(true), "keep-alive while shutting down")
}

func TestSystemdWatchdogStopsWhenTheReadinessCheckWedges(t *testing.T) {
	t.Parallel()

	const interval = 80 * time.Millisecond
	recorder := &notificationRecorder{}
	supervisor := &systemdSupervisor{notify: recorder.notify, watchdogInterval: interval}
	wedged := make(chan struct{})
	var checks atomic.Int64
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		supervisor.keepAlive(done, func() bool {
			if checks.Add(1) > 1 {
				<-wedged
			}
			return true
		})
	}()
	time.Sleep(4 * interval)
	pets := recorder.count(systemdWatchdogMessage)
	close(wedged)
	close(done)
	<-stopped
	require.LessOrEqual(t, pets, 1, "keep-alives continued while readiness was wedged")
}

func TestSendSystemdNotificationUsesContextDeadlineAndPreservesCloseFailure(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("close failed")
	connection := &systemdNotificationConnectionFake{closeErr: closeErr}
	contextDeadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), contextDeadline)
	defer cancel()

	err := sendSystemdNotification(ctx, connection, time.Minute, systemdReadyMessage)
	require.ErrorIs(t, err, closeErr, "notification error")
	require.True(t, connection.deadline.Equal(contextDeadline), "write deadline = %v, want context deadline", connection.deadline)
	require.Equal(t, systemdReadyMessage, string(connection.payload), "notification")
	require.Equal(t, 1, connection.closeCallCount, "Close calls")
}

func TestSendSystemdNotificationJoinsDeadlineAndCloseFailures(t *testing.T) {
	t.Parallel()

	deadlineErr := errors.New("deadline failed")
	closeErr := errors.New("close failed")
	connection := &systemdNotificationConnectionFake{deadlineErr: deadlineErr, closeErr: closeErr}

	timeout := time.Second
	earliestDeadline := time.Now().Add(timeout)
	err := sendSystemdNotification(context.Background(), connection, timeout, systemdReadyMessage)
	latestDeadline := time.Now().Add(timeout)
	for _, expected := range []error{deadlineErr, closeErr} {
		require.ErrorIs(t, err, expected, "notification error")
	}
	require.Empty(t, connection.payload, "notification payload")
	require.False(t, connection.deadline.Before(earliestDeadline), "write deadline = %v, want between %v and", connection.deadline, earliestDeadline)
	require.False(t, connection.deadline.After(latestDeadline), "write deadline = %v, want between %v and", connection.deadline, earliestDeadline)
}

func TestSendSystemdNotificationRejectsShortWrite(t *testing.T) {
	t.Parallel()

	connection := &systemdNotificationConnectionFake{shortWrite: true}
	err := sendSystemdNotification(context.Background(), connection, time.Second, systemdStoppingMessage)
	require.ErrorIs(t, err, io.ErrShortWrite, "short notification write")
}
