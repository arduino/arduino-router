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

package msgpackrouter

import "fmt"

const (
	// Error codes for the router
	ErrCodeInvalidParams        = 1
	ErrCodeMethodNotAvailable   = 2
	ErrCodeFailedToSendRequests = 3
	ErrCodeGenericError         = 4
	ErrCodeRouteAlreadyExists   = 5
	ErrCodeBufferLimitExceeded  = 6
)

type RouteError struct {
	message string
	code    int
}

func (m *RouteError) Error() string {
	return m.message
}

func (m *RouteError) ToEncodedError() []any {
	return []any{m.code, m.message}
}

func newRouteAlreadyExistsError(route string) *RouteError {
	return &RouteError{
		message: fmt.Sprintf("route already exists: %s", route),
		code:    ErrCodeRouteAlreadyExists,
	}
}

func routerError(code int8, message string) []any {
	return []any{code, message}
}
