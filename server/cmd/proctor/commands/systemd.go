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
	"fmt"
	"io"
	"net"
	"time"
)

const (
	systemdReadyMessage             = "READY=1"
	systemdReadyNotificationTimeout = 5 * time.Second
)

type systemdNotificationConnection interface {
	Write([]byte) (int, error)
	SetWriteDeadline(time.Time) error
	Close() error
}

func systemdReadyNotifier(socketPath string) func(context.Context) error {
	return func(ctx context.Context) error {
		connection, err := (&net.Dialer{}).DialContext(ctx, "unixgram", socketPath)
		if err != nil {
			return fmt.Errorf("connect to systemd notification socket: %w", err)
		}
		return sendSystemdReadyNotification(ctx, connection, systemdReadyNotificationTimeout)
	}
}

func sendSystemdReadyNotification(
	ctx context.Context,
	connection systemdNotificationConnection,
	timeout time.Duration,
) (resultErr error) {
	defer func() {
		if err := connection.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close systemd notification socket: %w", err))
		}
	}()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("send systemd readiness notification: %w", err)
	}
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("bound systemd readiness notification: %w", err)
	}

	written, err := connection.Write([]byte(systemdReadyMessage))
	if err != nil {
		return fmt.Errorf("send systemd readiness notification: %w", err)
	}
	if written != len(systemdReadyMessage) {
		return fmt.Errorf("send systemd readiness notification: %w", io.ErrShortWrite)
	}
	return nil
}
