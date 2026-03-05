// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc

import (
	"time"
)

// Logger is an interface for logging outgoing and incoming requests, responses, and notifications.
// All methods of this interface must be thread-safe.
type Logger interface {
	LogOutgoingRequest(id MessageID, method string, params []any)
	LogIncomingRequest(id MessageID, method string, params []any) FunctionLogger
	LogOutgoingResponse(id MessageID, method string, resp any, respErr any)
	LogIncomingResponse(id MessageID, method string, resp any, respErr any)
	LogOutgoingNotification(method string, params []any)
	LogIncomingNotification(method string, params []any) FunctionLogger
	LogIncomingCancelRequest(id MessageID)
	LogOutgoingCancelRequest(id MessageID)
	LogIncomingDataDelay(time.Duration)
	LogOutgoingDataDelay(time.Duration)
}

// FunctionLogger is an interface for logging additional information about the
// processing of a request or notification. It must be thread-safe.
type FunctionLogger interface {
	Logf(format string, a ...any)
}

type NullLogger struct{}

func (NullLogger) LogOutgoingRequest(id MessageID, method string, params []any) {
}

func (NullLogger) LogIncomingRequest(id MessageID, method string, params []any) FunctionLogger {
	return &NullFunctionLogger{}
}

func (NullLogger) LogOutgoingResponse(id MessageID, method string, resp any, respErr any) {
}

func (NullLogger) LogIncomingResponse(id MessageID, method string, resp any, respErr any) {
}

func (NullLogger) LogOutgoingNotification(method string, params []any) {
}

func (NullLogger) LogIncomingNotification(method string, params []any) FunctionLogger {
	return &NullFunctionLogger{}
}

func (NullLogger) LogIncomingCancelRequest(id MessageID) {}

func (NullLogger) LogOutgoingCancelRequest(id MessageID) {}

type NullFunctionLogger struct{}

func (NullFunctionLogger) Logf(format string, a ...any) {}

func (NullLogger) LogIncomingDataDelay(time.Duration) {}

func (NullLogger) LogOutgoingDataDelay(time.Duration) {}
