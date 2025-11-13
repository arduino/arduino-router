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

package msgpackrouter

import "fmt"

const (
	// Error codes for the router
	ErrCodeInvalidParams        = 1
	ErrCodeMethodNotAvailable   = 2
	ErrCodeFailedToSendRequests = 3
	ErrCodeGenericError         = 4
	ErrCodeRouteAlreadyExists   = 5
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
