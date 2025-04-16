package msgpackrouter

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"sync"

	"github.com/arduino/bridge/msgpackrpc"
)

type Router struct {
	routesLock sync.Mutex
	routes     map[string]*msgpackrpc.Connection
}

func New() Router {
	return Router{
		routes: make(map[string]*msgpackrpc.Connection),
	}
}

func (r *Router) Accept(conn io.ReadWriteCloser) {
	go r.connectionLoop(conn)
}

func (r *Router) connectionLoop(conn io.ReadWriteCloser) {
	defer conn.Close()

	var msgpackconn *msgpackrpc.Connection
	msgpackconn = msgpackrpc.NewConnection(conn, conn,
		func(ctx context.Context, _ msgpackrpc.FunctionLogger, method string, params []any) (_result any, _err any) {
			// This handler is called when a request is received from the client

			switch method {
			case "$/register":
				// Check if the client is trying to register a new method
				if len(params) != 1 {
					return nil, fmt.Sprintf("invalid params: only one param is expected, got %d", len(params))
				} else if methodToRegister, ok := params[0].(string); !ok {
					return nil, fmt.Sprintf("invalid params: expected string, got %T", params[0])
				} else if err := r.registerMethod(methodToRegister, msgpackconn); err != nil {
					return nil, err.Error()
				} else {
					return nil, nil
				}
			case "$/reset":
				// Check if the client is trying to remove its registered methods
				if len(params) != 0 {
					return nil, "invalid params: no params are expected"
				} else {
					r.removeMethodsFromConnection(msgpackconn)
					return nil, nil
				}
			}

			// Check if the method is registered
			client, ok := r.getConnectionForMethod(method)
			if !ok {
				return nil, fmt.Sprintf("method %s not available", method)
			}

			// Forward the call to the registered client
			reqResult, reqError, err := client.SendRequest(ctx, method, params)
			if err != nil {
				slog.Error("Failed to send request", "method", method, "err", err)
				return nil, fmt.Sprintf("failed to send request: %s", err)
			}

			// Send the response back to the original caller
			return reqResult, reqError
		},
		func(_ msgpackrpc.FunctionLogger, method string, params []any) {
			// This handler is called when a notification is received from the client

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
		return fmt.Errorf("route already exists: %s", method)
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
