// ---------------------------------------------------------------------------------------------
// Copyright (c) 2026 Sudosy Labs. All rights reserved.
// Licensed under the GNU Affero General Public License, version 3 only.
// See LICENSE in the server module root for license information.
// SPDX-License-Identifier: AGPL-3.0-only
// ---------------------------------------------------------------------------------------------

package websocket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/sudosylabs/proctor/server/model"
)

func TestTerminalFramesRejectRetiredIdentity(t *testing.T) {
	t.Parallel()
	current := newInboundTerminalFake()
	runtime := newInboundRuntime(&inboundTestApplication{}, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	runtime.terminal = current
	runtime.terminalID = model.NewId()
	retired := model.NewId()
	requests := []struct {
		action string
		data   any
	}{
		{examAttemptTerminalInputAction, examAttemptTerminalInputRequest{TerminalID: retired, Data: base64.StdEncoding.EncodeToString([]byte("stale"))}},
		{examAttemptTerminalResizeAction, examAttemptTerminalResizeRequest{TerminalID: retired, Cols: 99, Rows: 42}},
		{examAttemptTerminalCloseAction, examAttemptTerminalCloseRequest{TerminalID: retired}},
	}
	for i, request := range requests {
		runtime.handleRequest(context.Background(), requestWithData(t, int64(i+1), request.action, request.data))
		response := nextInboundResponse(t, runtime)
		if response.Status != "error" || response.Error.Code != "exam.attempt.terminal_closed" {
			t.Fatalf("stale %s accepted: %#v", request.action, response)
		}
	}
	current.mu.Lock()
	writes, cols := len(current.writes), current.window.Cols
	current.mu.Unlock()
	if writes != 0 || cols != 0 || runtime.terminal != current {
		t.Fatal("retired identity affected successor")
	}
	select {
	case <-current.closed:
		t.Fatal("retired close closed successor")
	default:
	}
	runtime.closeTerminal()
}

type delayedTerminalOutput struct {
	*inboundTerminalFake
	started chan struct{}
	release chan struct{}
}

func (t *delayedTerminalOutput) Read(body []byte) (int, error) {
	close(t.started)
	<-t.release
	return copy(body, []byte("late output")), io.EOF
}

func TestRetiredTerminalReaderCannotPublishOrClearSuccessor(t *testing.T) {
	t.Parallel()
	runtime := newInboundRuntime(&inboundTestApplication{}, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
	old := &delayedTerminalOutput{inboundTerminalFake: newInboundTerminalFake(), started: make(chan struct{}), release: make(chan struct{})}
	oldID := model.NewId()
	runtime.terminal = old
	runtime.terminalID = oldID
	done := make(chan struct{})
	go func() { runtime.readExamAttemptTerminal(old, oldID); close(done) }()
	<-old.started
	successor := newInboundTerminalFake()
	successorID := model.NewId()
	runtime.mu.Lock()
	runtime.terminal = successor
	runtime.terminalID = successorID
	runtime.mu.Unlock()
	close(old.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retired reader did not exit")
	}
	runtime.mu.Lock()
	current, id := runtime.terminal, runtime.terminalID
	runtime.mu.Unlock()
	if current != successor || id != successorID {
		t.Fatal("old reader cleared successor")
	}
	select {
	case <-successor.closed:
		t.Fatal("old reader closed successor")
	default:
	}
	select {
	case message := <-runtime.send:
		t.Fatalf("retired reader published a frame: %#v", message)
	default:
	}
	runtime.closeTerminal()
}

func TestTerminalOpenRequiresSafeWorkspaceCursor(t *testing.T) {
	t.Parallel()
	for _, cursor := range []string{"", "null", "-1", "9007199254740992", "0.5"} {
		t.Run(cursor, func(t *testing.T) {
			runtime := newInboundRuntime(&inboundTestApplication{}, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
			body := `{"generation":1,"continuity_credential":"` + model.NewCredentialToken() + `","cols":80,"rows":24`
			if cursor != "" {
				body += `,"expected_workspace_cursor":` + cursor
			}
			body += `}`
			runtime.handleExamAttemptTerminalOpen(context.Background(), &Request{Sequence: 1, Data: json.RawMessage(body)})
			response := nextInboundResponse(t, runtime)
			if response.Status != "error" || response.Error.Code != "websocket.request.invalid" {
				t.Fatalf("invalid cursor accepted: %#v", response)
			}
		})
	}
}
