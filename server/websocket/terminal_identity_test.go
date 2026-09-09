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

	"github.com/sudosylabs/proctor/server/app"
	appexecution "github.com/sudosylabs/proctor/server/app/execution"
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

// The application can reject a request while preserving the underlying PTY.
type gatedInboundTerminal struct {
	*inboundTerminalFake
	interactionErr error
}

func (terminal *gatedInboundTerminal) Write(data []byte) (int, error) {
	if terminal.interactionErr != nil {
		return 0, terminal.interactionErr
	}
	return terminal.inboundTerminalFake.Write(data)
}
func (terminal *gatedInboundTerminal) Resize(ctx context.Context, window app.CandidateExamTerminalWindow) error {
	if terminal.interactionErr != nil {
		return terminal.interactionErr
	}
	return terminal.inboundTerminalFake.Resize(ctx, window)
}
func TestTerminalInteractionDenialPreservesTransportIdentity(t *testing.T) {
	for _, action := range []string{examAttemptTerminalInputAction, examAttemptTerminalResizeAction} {
		t.Run(action, func(t *testing.T) {
			terminal := &gatedInboundTerminal{inboundTerminalFake: newInboundTerminalFake(), interactionErr: app.NewError("exam.attempt.terminal_unavailable").Wrap(appexecution.ErrInteractionBlocked)}
			runtime := newInboundRuntime(&inboundTestApplication{}, newInboundTestSocket(), newRuntimeTestClock(time.Now()))
			id := model.NewId()
			runtime.terminal, runtime.terminalID = terminal, id
			defer runtime.closeTerminal()
			var data any = examAttemptTerminalInputRequest{TerminalID: id, Data: base64.StdEncoding.EncodeToString([]byte("pwd\n"))}
			if action == examAttemptTerminalResizeAction {
				data = examAttemptTerminalResizeRequest{TerminalID: id, Cols: 90, Rows: 30}
			}
			runtime.handleRequest(context.Background(), requestWithData(t, 1, action, data))
			if runtime.terminal != terminal || runtime.terminalID != id {
				t.Fatal("interaction denial detached original PTY")
			}
			response := nextInboundResponse(t, runtime)
			if response.Status != "error" || response.Error.Code != "exam.attempt.terminal_unavailable" {
				t.Fatalf("denied response: %#v", response)
			}
			select {
			case <-terminal.closed:
				t.Fatal("interaction denial closed PTY")
			default:
			}
			select {
			case message := <-runtime.send:
				t.Fatalf("denial emitted extra terminal frame: %#v", message)
			default:
			}
			terminal.interactionErr = nil
			runtime.handleRequest(context.Background(), requestWithData(t, 2, action, data))
			if response = nextInboundResponse(t, runtime); response.Status != "ok" {
				t.Fatalf("recovery response: %#v", response)
			}
			if runtime.terminal != terminal || runtime.terminalID != id {
				t.Fatal("recovery replaced original PTY")
			}
			// An actual failure still detaches and closes this same terminal.
			terminal.interactionErr = app.NewError("exam.attempt.terminal_unavailable")
			runtime.handleRequest(context.Background(), requestWithData(t, 3, action, data))
			if runtime.terminal != nil || runtime.terminalID != "" {
				t.Fatal("terminal failure retained handle")
			}
			select {
			case <-terminal.closed:
			default:
				t.Fatal("terminal failure did not close PTY")
			}
		})
	}
}
