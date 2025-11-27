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

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"sync"

	"github.com/arduino/arduino-router/msgpackrpc"
)

type RouterRequestHandler func(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (result any, err any)

type Router struct {
	routesLock     sync.Mutex
	routes         map[string]*msgpackrpc.Connection
	routesInternal map[string]RouterRequestHandler
	sendQueueSize  int
}

func New(perConnSendQueueSize int) *Router {
	return &Router{
		routes:         make(map[string]*msgpackrpc.Connection),
		routesInternal: make(map[string]RouterRequestHandler),
		sendQueueSize:  perConnSendQueueSize,
	}
}

// SetSendQueueSize sets the size of the send queue for each connection.
// Only new connections will be affected by this change, existing connections
// will keep their current send queue size.
func (r *Router) SetSendQueueSize(size int) {
	r.routesLock.Lock()
	defer r.routesLock.Unlock()
	r.sendQueueSize = size
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
		func(ctx context.Context, _ msgpackrpc.FunctionLogger, method string, params []any) (_result any, _err any) {
			// This handler is called when a request is received from the client
			slog.Debug("Received request", "method", method, "params", params)
			defer func() {
				slog.Debug("Received response", "method", method, "result", _result, "error", _err)
			}()

			switch method {
			case "$/register":
				// Check if the client is trying to register a new method
				if len(params) != 1 {
					return nil, routerError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: only one param is expected, got %d", len(params)))
				} else if methodToRegister, ok := params[0].(string); !ok {
					return nil, routerError(ErrCodeInvalidParams, fmt.Sprintf("invalid params: expected string, got %T", params[0]))
				} else if err := r.registerMethod(methodToRegister, msgpackconn); err != nil {
					if rae, ok := err.(*RouteError); ok {
						return nil, rae.ToEncodedError()
					}
					return nil, routerError(ErrCodeGenericError, err.Error())
				} else {
					return true, nil
				}
			case "$/reset":
				// Check if the client is trying to remove its registered methods
				if len(params) != 0 {
					return nil, routerError(ErrCodeInvalidParams, "invalid params: no params are expected")
				} else {
					r.removeMethodsFromConnection(msgpackconn)
					return true, nil
				}
			}

			// Check if the method is an internal method
			if handler, ok := r.routesInternal[method]; ok {
				// Call the internal method handler
				return handler(ctx, msgpackconn, params)
			}

			// Check if the method is registered
			client, ok := r.getConnectionForMethod(method)
			if !ok {
				return nil, routerError(ErrCodeMethodNotAvailable, fmt.Sprintf("method %s not available", method))
			}

			// Forward the call to the registered client
			reqResult, reqError, err := client.SendRequest(ctx, method, params)
			if err != nil {
				slog.Error("Failed to send request", "method", method, "err", err)
				return nil, routerError(ErrCodeFailedToSendRequests, fmt.Sprintf("failed to send request: %s", err))
			}

			// Send the response back to the original caller
			return reqResult, reqError
		},
		func(_ msgpackrpc.FunctionLogger, method string, params []any) {
			// This handler is called when a notification is received from the client
			slog.Debug("Received notification", "method", method, "params", params)

			// Check if the method is an internal method
			if handler, ok := r.routesInternal[method]; ok {
				// call the internal method handler (since it's a notification, discard the result)
				_, _ = handler(context.Background(), msgpackconn, params)
				return
			}

			// Check if the method is registered
			client, ok := r.getConnectionForMethod(method)
			if !ok {
				// if the method is not registered, the notifitication is lost
				return
			}

			// Forward the notification to the registered client
			if err := client.SendNotification(method, params); err != nil {
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
