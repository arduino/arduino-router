// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

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
