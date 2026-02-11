// This file is part of arduino-router
//
// Copyright (C) ARDUINO SRL (www.arduino.cc)
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
	sendMaxWorkers int
}

func New(perConnMaxWorkers int) *Router {
	return &Router{
		routes:         make(map[string]*msgpackrpc.Connection),
		routesInternal: make(map[string]RouterRequestHandler),
		sendMaxWorkers: perConnMaxWorkers,
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
		func(_ msgpackrpc.FunctionLogger, method string, params []any, _res msgpackrpc.ResponseHandler) {
			// This handler is called when a request is received from the client
			slog.Debug("Received request", "method", method, "params", params)
			res := func(result any, err any) {
				slog.Debug("Received response", "method", method, "result", result, "error", err)
				_res(result, err)
			}

			switch method {
			case "$/register":
				// Check if the client is trying to register a new method
				if len(params) != 1 {
					res(nil, routerError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: only one param is expected, got %d", len(params))))
					return
				} else if methodToRegister, ok := params[0].(string); !ok {
					res(nil, routerError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: expected string, got %T", params[0])))
					return
				} else if err := r.registerMethod(methodToRegister, msgpackconn); err != nil {
					if rae, ok := err.(*RouteError); ok {
						res(nil, rae.ToEncodedError())
						return
					}
					res(nil, routerError(ErrCodeGenericError, err.Error()))
					return
				} else {
					res(true, nil)
					return
				}
			case "$/reset":
				// Check if the client is trying to remove its registered methods
				if len(params) != 0 {
					res(nil, routerError(ErrCodeInvalidParams, "invalid params: no params are expected"))
					return
				} else {
					r.removeMethodsFromConnection(msgpackconn)
					res(true, nil)
					return
				}
			}

			// Check if the method is an internal method
			if handler, ok := r.routesInternal[method]; ok {
				// Call the internal method handler
				handler(msgpackconn, params, res)
				return
			}

			// Check if the method is registered
			client, ok := r.getConnectionForMethod(method)
			if !ok {
				res(nil, routerError(ErrCodeMethodNotAvailable, fmt.Sprintf("method %s not available", method)))
				return
			}

			// Forward the call to the registered client
			err := client.SendRequestWithAsyncResult(
				res, // Send the response back to the original caller
				method, params...)
			if err != nil {
				slog.Error("Failed to send request", "method", method, "err", err)
				res(nil, routerError(ErrCodeFailedToSendRequests, fmt.Sprintf("failed to send request: %s", err)))
				return
			}
		},
		func(_ msgpackrpc.FunctionLogger, method string, params []any) {
			// This handler is called when a notification is received from the client
			slog.Debug("Received notification", "method", method, "params", params)

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
