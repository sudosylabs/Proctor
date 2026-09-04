// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"time"

	gorilla "github.com/gorilla/websocket"
)

const (
	websocketCloseMessage = gorilla.CloseMessage
	websocketPingMessage  = gorilla.PingMessage
)

type connectionSocket interface {
	SetReadLimit(int64)
	SetReadDeadline(time.Time) error
	SetPongHandler(func(string) error)
	ReadJSON(any) error
	SetWriteDeadline(time.Time) error
	WriteJSON(any) error
	WriteMessage(int, []byte) error
	WriteControl(int, []byte, time.Time) error
	Close() error
}

type gorillaConnectionSocket struct {
	socket *gorilla.Conn
}

func newGorillaConnectionSocket(socket *gorilla.Conn) connectionSocket {
	return &gorillaConnectionSocket{socket: socket}
}

func (s *gorillaConnectionSocket) SetReadLimit(limit int64) {
	s.socket.SetReadLimit(limit)
}

func (s *gorillaConnectionSocket) SetReadDeadline(deadline time.Time) error {
	return s.socket.SetReadDeadline(deadline)
}

func (s *gorillaConnectionSocket) SetPongHandler(handler func(string) error) {
	s.socket.SetPongHandler(handler)
}

func (s *gorillaConnectionSocket) ReadJSON(value any) error {
	return s.socket.ReadJSON(value)
}

func (s *gorillaConnectionSocket) SetWriteDeadline(deadline time.Time) error {
	return s.socket.SetWriteDeadline(deadline)
}

func (s *gorillaConnectionSocket) WriteJSON(value any) error {
	return s.socket.WriteJSON(value)
}

func (s *gorillaConnectionSocket) WriteMessage(messageType int, data []byte) error {
	return s.socket.WriteMessage(messageType, data)
}

func (s *gorillaConnectionSocket) WriteControl(
	messageType int,
	data []byte,
	deadline time.Time,
) error {
	return s.socket.WriteControl(messageType, data, deadline)
}

func (s *gorillaConnectionSocket) Close() error {
	return s.socket.Close()
}
