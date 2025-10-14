// This file is part of arduino-router
//
// Copyright 2025 ARDUINO SA (http://www.arduino.cc/)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-router
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
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

type FunctionLogger interface {
	Logf(format string, a ...interface{})
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

func (NullFunctionLogger) Logf(format string, a ...interface{}) {}

func (NullLogger) LogIncomingDataDelay(time.Duration) {}

func (NullLogger) LogOutgoingDataDelay(time.Duration) {}
