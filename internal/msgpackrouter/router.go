// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrouter

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"sync"

	"github.com/arduino/arduino-router/msgpackrpc"
)

type RouterRequestHandler func(rpc *msgpackrpc.Connection, params []any, res RouterResponseHandler)

type RouterResponseHandler func(result any, err any)

type Router struct {
	routesLock     sync.Mutex
	routes         map[string]*msgpackrpc.Connection
	routesInternal map[string]RouterRequestHandler
}

func New() *Router {
	return &Router{
		routes:         make(map[string]*msgpackrpc.Connection),
		routesInternal: make(map[string]RouterRequestHandler),
	}
}

func (r *Router) Accept(conn io.ReadWriteCloser) <-chan struct{} {
	res := make(chan struct{})
	go func() {
		r.connectionLoop(conn)
		close(res)
	}()
	return res
}

func (r *Router) RegisterMethod(method string, handler RouterRequestHandler) error {
	r.routesLock.Lock()
	defer r.routesLock.Unlock()

	if _, ok := r.routesInternal[method]; ok {
		slog.Error("Route already exists", "method", method)
		return newRouteAlreadyExistsError(method)
	}

	// Register the method with the handler
	r.routesInternal[method] = handler
	slog.Info("Registered internal method", "method", method)
	return nil
}

func (r *Router) connectionLoop(conn io.ReadWriteCloser) {
	defer conn.Close()

	var msgpackconn *msgpackrpc.Connection
	msgpackconn = msgpackrpc.NewConnection(conn, conn,
		func(_ msgpackrpc.FunctionLogger, method string, params []any, res msgpackrpc.ResponseSender) {
			// This handler is called when a request is received from the client
			slog.Debug("Received request", "method", method, "params", params)

			fwdRes := func(reqResult any, reqErr any) {
				// This handler is used to send the response back to the original caller.
				slog.Debug("Received response", "method", method, "result", reqResult, "error", reqErr)

				err := res(reqResult, reqErr)
				if errors.Is(err, &msgpackrpc.ErrBufferLimitExceeded{}) {
					slog.Error("Response exceeded buffer limit", "method", method)
					err = res(nil, routerError(ErrCodeBufferLimitExceeded, "message size exceeds the limit"))
				}
				if err != nil {
					slog.Error("Error sending response", "err", err)
				}
			}

			switch method {
			case "$/register":
				// Check if the client is trying to register a new method
				if len(params) != 1 {
					fwdRes(nil, routerError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: only one param is expected, got %d", len(params))))
					return
				} else if methodToRegister, ok := params[0].(string); !ok {
					fwdRes(nil, routerError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: expected string, got %T", params[0])))
					return
				} else if err := r.registerMethod(methodToRegister, msgpackconn); err != nil {
					if rae, ok := err.(*RouteError); ok {
						fwdRes(nil, rae.ToEncodedError())
						return
					}
					fwdRes(nil, routerError(ErrCodeGenericError, err.Error()))
					return
				} else {
					fwdRes(true, nil)
					return
				}
			case "$/reset":
				// Check if the client is trying to remove its registered methods
				if len(params) != 0 {
					fwdRes(nil, routerError(ErrCodeInvalidParams, "invalid params: no params are expected"))
					return
				} else {
					r.removeMethodsFromConnection(msgpackconn)
					fwdRes(true, nil)
					return
				}
			case "$/setMaxMsgSize":
				// Fix the buffer size for the connection, if a bigger message is received, it will be rejected
				if len(params) != 1 {
					fwdRes(nil, routerError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: only one param is expected, got %d", len(params))))
					return
				} else if maxBuffSize, ok := msgpackrpc.ToInt(params[0]); !ok {
					fwdRes(nil, routerError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: expected int, got %T", params[0])))
					return
				} else if maxBuffSize <= 127 {
					fwdRes(nil, routerError(ErrCodeInvalidParams, "invalid params: max buffer size must be greater than 127"))
					return
				} else {
					msgpackconn.SetMaxOutgoingMessageSize(maxBuffSize)
					fwdRes(true, nil)
					return
				}
			}

			// Check if the method is an internal method
			if handler, ok := r.routesInternal[method]; ok {
				// Call the internal method handler
				handler(msgpackconn, params, fwdRes)
				return
			}

			// Check if the method is registered
			client, ok := r.getConnectionForMethod(method)
			if !ok {
				fwdRes(nil, routerError(ErrCodeMethodNotAvailable, fmt.Sprintf("method %s not available", method)))
				return
			}

			// Forward the call to the registered client
			err := client.SendRequestWithAsyncResult(
				fwdRes, // Send the response back to the original caller
				method, params...)
			if errors.Is(err, &msgpackrpc.ErrBufferLimitExceeded{}) {
				slog.Error("Request exceeded buffer limit", "method", method)
				fwdRes(nil, routerError(ErrCodeBufferLimitExceeded, "message size exceeds the limit"))
				return
			}
			if err != nil {
				slog.Error("Failed to send request", "method", method, "err", err)
				fwdRes(nil, routerError(ErrCodeFailedToSendRequests, fmt.Sprintf("failed to send request: %s", err)))
				return
			}
		},
		func(_ msgpackrpc.FunctionLogger, method string, params []any) {
			// This handler is called when a notification is received from the client
			slog.Debug("Received notification", "method", method, "params", params)

			if method == "$/setMaxMsgSize" {
				// Fix the buffer size for the connection, if a bigger message is received, it will be rejected
				if len(params) != 1 {
					slog.Error(fmt.Sprintf("invalid params: only one param is expected, got %d", len(params)))
					return
				} else if maxBuffSize, ok := msgpackrpc.ToInt(params[0]); !ok {
					slog.Error(fmt.Sprintf("invalid params: expected int, got %T", params[0]))
					return
				} else if maxBuffSize <= 127 {
					slog.Error("invalid params: max buffer size must be greater than 127")
					return
				} else {
					msgpackconn.SetMaxOutgoingMessageSize(maxBuffSize)
					return
				}
			}

			// Check if the method is an internal method
			if handler, ok := r.routesInternal[method]; ok {
				// call the internal method handler (since it's a notification, discard the result)
				handler(msgpackconn, params, func(_, _ any) {})
				return
			}

			// Check if the method is registered
			client, ok := r.getConnectionForMethod(method)
			if !ok {
				// if the method is not registered, the notifitication is lost
				return
			}

			// Forward the notification to the registered client
			if err := client.SendNotification(method, params...); err != nil {
				slog.Error("Failed to send notification", "method", method, "err", err)
				return
			}
		},
		func(err error) {
			if errors.Is(err, io.EOF) {
				slog.Info("Connection closed by peer")
				return
			}
			slog.Error("Error in connection", "err", err)
		},
	)

	msgpackconn.SetEncodeParamsAsRaw(true)
	msgpackconn.Run()

	// Unregister the methods when the connection is terminated
	r.removeMethodsFromConnection(msgpackconn)
	msgpackconn.Close()

}

func (r *Router) registerMethod(method string, conn *msgpackrpc.Connection) error {
	r.routesLock.Lock()
	defer r.routesLock.Unlock()

	if _, ok := r.routes[method]; ok {
		return newRouteAlreadyExistsError(method)
	}
	r.routes[method] = conn
	return nil
}

func (r *Router) removeMethodsFromConnection(conn *msgpackrpc.Connection) {
	r.routesLock.Lock()
	defer r.routesLock.Unlock()

	maps.DeleteFunc(r.routes, func(k string, v *msgpackrpc.Connection) bool {
		return v == conn
	})
}

func (r *Router) getConnectionForMethod(method string) (*msgpackrpc.Connection, bool) {
	r.routesLock.Lock()
	defer r.routesLock.Unlock()
	conn, ok := r.routes[method]
	return conn, ok
}
