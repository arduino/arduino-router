// This file is part of arduino-router.
//
// Copyright (C) Arduino s.r.l. and/or its affiliated companies
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

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
