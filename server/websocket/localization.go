// Copyright 2026 SudoSylabs
// SPDX-License-Identifier: AGPL-3.0-only

package websocket

import "github.com/sudosylabs/proctor/server/localization"

const localizationOrigin = "websocket"

const maximumCloseReasonBytes = 123

type localizedMessage struct {
	id       string
	fallback string
}

type websocketErrorPresentation string

const (
	websocketErrorActionUnknown                websocketErrorPresentation = "action_unknown"
	websocketErrorAttemptAlreadyConnected      websocketErrorPresentation = "attempt_already_connected"
	websocketErrorAttemptConnectionDenied      websocketErrorPresentation = "attempt_connection_denied"
	websocketErrorAttemptConnectionFailed      websocketErrorPresentation = "attempt_connection_failed"
	websocketErrorAttemptConnectionInactive    websocketErrorPresentation = "attempt_connection_inactive"
	websocketErrorAttemptConnectionLost        websocketErrorPresentation = "attempt_connection_lost"
	websocketErrorAttemptConnectRequestInvalid websocketErrorPresentation = "attempt_connect_request_invalid"
	websocketErrorAttemptRenewalDenied         websocketErrorPresentation = "attempt_renewal_denied"
	websocketErrorAttemptRenewalFailed         websocketErrorPresentation = "attempt_renewal_failed"
	websocketErrorAttemptRenewalRequestInvalid websocketErrorPresentation = "attempt_renewal_request_invalid"
	websocketErrorFocusLossConflict            websocketErrorPresentation = "focus_loss_conflict"
	websocketErrorFocusLossDenied              websocketErrorPresentation = "focus_loss_denied"
	websocketErrorFocusLossFailed              websocketErrorPresentation = "focus_loss_failed"
	websocketErrorFocusLossSignalInvalid       websocketErrorPresentation = "focus_loss_signal_invalid"
	websocketErrorBrowserActivityInvalid       websocketErrorPresentation = "browser_activity_invalid"
	websocketErrorBrowserActivityFailed        websocketErrorPresentation = "browser_activity_failed"
	websocketErrorRequestInvalid               websocketErrorPresentation = "request_invalid"
	websocketErrorSubscriptionDenied           websocketErrorPresentation = "subscription_denied"
	websocketErrorSubscriptionFailed           websocketErrorPresentation = "subscription_failed"
	websocketErrorSubscriptionInvalid          websocketErrorPresentation = "subscription_invalid"
	websocketErrorSubscriptionLimit            websocketErrorPresentation = "subscription_limit"
	websocketErrorTerminalAlreadyOpen          websocketErrorPresentation = "terminal_already_open"
	websocketErrorTerminalCloseRequestInvalid  websocketErrorPresentation = "terminal_close_request_invalid"
	websocketErrorTerminalInputFailed          websocketErrorPresentation = "terminal_input_failed"
	websocketErrorTerminalInputInvalid         websocketErrorPresentation = "terminal_input_invalid"
	websocketErrorTerminalNotOpen              websocketErrorPresentation = "terminal_not_open"
	websocketErrorTerminalOpenFailed           websocketErrorPresentation = "terminal_open_failed"
	websocketErrorTerminalOpenRequestInvalid   websocketErrorPresentation = "terminal_open_request_invalid"
	websocketErrorTerminalResizeFailed         websocketErrorPresentation = "terminal_resize_failed"
	websocketErrorTerminalSizeInvalid          websocketErrorPresentation = "terminal_size_invalid"
	websocketErrorTerminalUnavailable          websocketErrorPresentation = "terminal_unavailable"
)

var websocketErrorMessages = map[websocketErrorPresentation]localizedMessage{
	websocketErrorActionUnknown:                {id: "websocket.error.action.unknown", fallback: "Unknown WebSocket action."},
	websocketErrorAttemptAlreadyConnected:      {id: "websocket.error.exam_attempt.connection.already_established", fallback: "Exam Attempt connection is already established."},
	websocketErrorAttemptConnectionDenied:      {id: "websocket.error.exam_attempt.connection.denied", fallback: "Exam Attempt connection denied."},
	websocketErrorAttemptConnectionFailed:      {id: "websocket.error.exam_attempt.connection.failed", fallback: "Exam Attempt connection failed."},
	websocketErrorAttemptConnectionInactive:    {id: "websocket.error.exam_attempt.connection.inactive", fallback: "Exam Attempt connection is not active."},
	websocketErrorAttemptConnectionLost:        {id: "websocket.error.exam_attempt.connection.lost", fallback: "Secure connectivity could not be renewed. Ask a manager to re-allow access."},
	websocketErrorAttemptConnectRequestInvalid: {id: "websocket.error.exam_attempt.connection.request_invalid", fallback: "Invalid Exam Attempt connection request."},
	websocketErrorAttemptRenewalDenied:         {id: "websocket.error.exam_attempt.renewal.denied", fallback: "Exam Attempt renewal denied."},
	websocketErrorAttemptRenewalFailed:         {id: "websocket.error.exam_attempt.renewal.failed", fallback: "Exam Attempt renewal failed."},
	websocketErrorAttemptRenewalRequestInvalid: {id: "websocket.error.exam_attempt.renewal.request_invalid", fallback: "Invalid Exam Attempt renewal request."},
	websocketErrorFocusLossConflict:            {id: "websocket.error.exam_attempt.focus_loss.conflict", fallback: "Focus Loss signal conflicts with an accepted sequence."},
	websocketErrorFocusLossDenied:              {id: "websocket.error.exam_attempt.focus_loss.denied", fallback: "Focus Loss signal was denied."},
	websocketErrorFocusLossFailed:              {id: "websocket.error.exam_attempt.focus_loss.failed", fallback: "Focus Loss signal could not be accepted."},
	websocketErrorFocusLossSignalInvalid:       {id: "websocket.error.exam_attempt.focus_loss.signal_invalid", fallback: "Invalid Focus Loss signal."},
	websocketErrorBrowserActivityInvalid:       {id: "websocket.error.exam_attempt.browser_activity.invalid", fallback: "Invalid browser activity record."},
	websocketErrorBrowserActivityFailed:        {id: "websocket.error.exam_attempt.browser_activity.failed", fallback: "Browser activity could not be accepted."},
	websocketErrorRequestInvalid:               {id: "websocket.error.request.invalid", fallback: "Invalid WebSocket request."},
	websocketErrorSubscriptionDenied:           {id: "websocket.error.subscription.denied", fallback: "WebSocket subscription denied."},
	websocketErrorSubscriptionFailed:           {id: "websocket.error.subscription.failed", fallback: "WebSocket subscription failed."},
	websocketErrorSubscriptionInvalid:          {id: "websocket.error.subscription.invalid", fallback: "Invalid subscription."},
	websocketErrorSubscriptionLimit:            {id: "websocket.error.subscription.limit", fallback: "WebSocket subscription limit reached."},
	websocketErrorTerminalAlreadyOpen:          {id: "websocket.error.exam_attempt.terminal.already_open", fallback: "A terminal is already open."},
	websocketErrorTerminalCloseRequestInvalid:  {id: "websocket.error.exam_attempt.terminal.close_request_invalid", fallback: "Invalid terminal close request."},
	websocketErrorTerminalInputFailed:          {id: "websocket.error.exam_attempt.terminal.input_failed", fallback: "Terminal input failed."},
	websocketErrorTerminalInputInvalid:         {id: "websocket.error.exam_attempt.terminal.input_invalid", fallback: "Invalid terminal input."},
	websocketErrorTerminalNotOpen:              {id: "websocket.error.exam_attempt.terminal.not_open", fallback: "Terminal is not open."},
	websocketErrorTerminalOpenFailed:           {id: "websocket.error.exam_attempt.terminal.open_failed", fallback: "Terminal could not be opened."},
	websocketErrorTerminalOpenRequestInvalid:   {id: "websocket.error.exam_attempt.terminal.open_request_invalid", fallback: "Invalid Exam Attempt terminal request."},
	websocketErrorTerminalResizeFailed:         {id: "websocket.error.exam_attempt.terminal.resize_failed", fallback: "Terminal resize failed."},
	websocketErrorTerminalSizeInvalid:          {id: "websocket.error.exam_attempt.terminal.size_invalid", fallback: "Invalid terminal size."},
	websocketErrorTerminalUnavailable:          {id: "websocket.error.exam_attempt.terminal.unavailable", fallback: "Terminal is unavailable."},
}

var websocketCloseMessages = map[string]localizedMessage{
	"authorization_changed": {id: "websocket.close.authorization_changed", fallback: "authorization changed"},
	"backpressure":          {id: "websocket.close.backpressure", fallback: "client is too slow"},
	"connection_closed":     {id: "websocket.close.connection_closed", fallback: "connection closed"},
	"connection_limit":      {id: "websocket.close.connection_limit", fallback: "connection limit reached"},
	"server_shutdown":       {id: "websocket.close.server_shutdown", fallback: "server shutting down"},
	"session_revoked":       {id: "websocket.close.session_revoked", fallback: "session no longer valid"},
}

// LocalizationDefinitions returns all WebSocket response and close prose. The
// protocol codes remain stable and locale independent.
func LocalizationDefinitions() []localization.Definition {
	definitions := make([]localization.Definition, 0, len(websocketErrorMessages)+len(websocketCloseMessages))
	for _, message := range websocketErrorMessages {
		definitions = append(definitions, localization.Definition{ID: message.id, Origin: localizationOrigin})
	}
	for _, message := range websocketCloseMessages {
		definitions = append(definitions, localization.Definition{ID: message.id, Origin: localizationOrigin})
	}
	return definitions
}

func websocketErrorMessage(presentation websocketErrorPresentation) localizedMessage {
	return websocketErrorMessages[presentation]
}

func localizedText(localizer Localizer, locale string, message localizedMessage) string {
	if localizer != nil && message.id != "" {
		if translated, err := localizer.Translate(locale, message.id, nil); err == nil {
			return translated
		}
	}
	return message.fallback
}

func localizedCloseReason(localizer Localizer, locale string, message localizedMessage) string {
	return boundedCloseReason(localizedText(localizer, locale, message), message.fallback)
}

func boundedCloseReason(translated, fallback string) string {
	if len(translated) <= maximumCloseReasonBytes {
		return translated
	}
	return fallback
}
