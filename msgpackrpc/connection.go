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

package msgpackrpc

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

type MessageID uint

const (
	messageTypeRequest      = 0
	messageTypeResponse     = 1
	messageTypeNotification = 2
)

// Connection is a MessagePack-RPC connection
type Connection struct {
	in                  io.ReadCloser
	out                 io.WriteCloser
	outEncoder          *msgpack.Encoder
	outMutex            sync.Mutex
	errorHandler        ErrorHandler
	requestHandler      RequestHandler
	notificationHandler NotificationHandler
	logger              Logger

	activeOutRequests      map[MessageID]*outRequest
	activeOutRequestsMutex sync.Mutex
	lastOutRequestsIndex   atomic.Uint32
}

type outRequest struct {
	res    ResponseHandler
	method string
}

// RequestHandler handles requests from a MessagePack-RPC Connection.
type RequestHandler func(logger FunctionLogger, method string, params []any, res ResponseHandler)

// ResponseHandler is a callback function used to send the result of a request back
// to the caller. It must be thread-safe (request handlers may decide to handle the request
// asynchronously, calling the response handler from another goroutine when the result is ready).
type ResponseHandler func(result any, err any)

// NotificationHandler handles notifications from a MessagePack-RPC Connection.
type NotificationHandler func(logger FunctionLogger, method string, params []any)

// ErrorHandler handles errors from a MessagePack-RPC Connection.
// It is called when an error occurs while reading from the connection or when
// sending a request or notification.
type ErrorHandler func(error)

// NewConnection creates a new MessagePack-RPC Connection handler.
func NewConnection(in io.ReadCloser, out io.WriteCloser, requestHandler RequestHandler, notificationHandler NotificationHandler, errorHandler ErrorHandler) *Connection {
	outEncoder := msgpack.NewEncoder(out)
	outEncoder.UseCompactInts(true)
	if requestHandler == nil {
		requestHandler = func(logger FunctionLogger, method string, params []any, res ResponseHandler) {
			res(nil, fmt.Errorf("method not implemented: %s", method))
		}
	}
	if notificationHandler == nil {
		notificationHandler = func(logger FunctionLogger, method string, params []any) {
			// ignore notifications
		}
	}
	if errorHandler == nil {
		errorHandler = func(err error) {
			// ignore errors
		}
	}
	return &Connection{
		in:                  in,
		out:                 out,
		outEncoder:          outEncoder,
		requestHandler:      requestHandler,
		notificationHandler: notificationHandler,
		errorHandler:        errorHandler,
		activeOutRequests:   map[MessageID]*outRequest{},
		logger:              NullLogger{},
	}
}

// SetLogger sets the logger for the connection.
// It is NOT safe to call this method while the connection is running,
// it should be called before starting the connection with Run method.
func (c *Connection) SetLogger(l Logger) {
	c.logger = l
}

func (c *Connection) Run() {
	in := msgpack.NewDecoder(c.in)
	for {
		var data []any
		start := time.Now()
		if v, err := in.DecodeInterface(); err != nil {
			c.errorHandler(fmt.Errorf("can't read packet: %w", err))
			return // unrecoverable
		} else if s, ok := v.([]any); !ok {
			c.errorHandler(fmt.Errorf("invalid packet, expected array, got: %T", v))
			continue // ignore invalid packets
		} else {
			data = s
		}
		elapsed := time.Since(start)
		c.logger.LogIncomingDataDelay(elapsed)

		if err := c.processIncomingMessage(data); err != nil {
			c.errorHandler(err)
		}
	}
}

func (c *Connection) processIncomingMessage(data []any) error {
	if len(data) < 3 {
		return fmt.Errorf("invalid packet, expected array with at least 3 elements")
	}

	msgType, ok := ToInt(data[0])
	if !ok {
		return fmt.Errorf("invalid packet, expected int as first element, got %T", data[0])
	}

	switch msgType {
	case messageTypeRequest:
		if len(data) != 4 {
			return fmt.Errorf("invalid request, expected array with 4 elements")
		}
		if id, ok := ToUint(data[1]); !ok {
			return fmt.Errorf("invalid request, expected msgid (uint) as second element")
		} else if method, ok := data[2].(string); !ok {
			return fmt.Errorf("invalid request, expected method (string) as third element")
		} else if params, ok := data[3].([]any); !ok {
			return fmt.Errorf("invalid request, expected params (array) as fourth element")
		} else {
			c.handleIncomingRequest(MessageID(id), method, params)
		}
		return nil
	case messageTypeResponse:
		if len(data) != 4 {
			return fmt.Errorf("invalid response, expected array with 4 elements")
		}
		if id, ok := ToUint(data[1]); !ok {
			return fmt.Errorf("invalid response, expected msgid (uint) as second element")
		} else {
			reqError := data[2]
			reqResult := data[3]
			c.handleIncomingResponse(MessageID(id), reqError, reqResult)
		}
		return nil
	case messageTypeNotification:
		if len(data) != 3 {
			return fmt.Errorf("invalid notification, expected array with 3 elements")
		}
		if method, ok := data[1].(string); !ok {
			return fmt.Errorf("invalid notification, expected method (string) as second element")
		} else if params, ok := data[2].([]any); !ok {
			return fmt.Errorf("invalid notification, expected params (array) as third element")
		} else {
			c.handleIncomingNotification(method, params)
		}
		return nil
	default:
		return fmt.Errorf("invalid packet, expected request, response or notification")
	}
}

func (c *Connection) handleIncomingRequest(id MessageID, method string, params []any) {
	logger := c.logger.LogIncomingRequest(id, method, params)

	// This callback may be called by another goroutine, because the request handler
	// may want to process the request asynchronously.
	cb := func(reqResult, reqError any) {
		c.logger.LogOutgoingResponse(id, method, reqResult, reqError)

		if err := c.send(messageTypeResponse, id, reqError, reqResult); err != nil {
			c.errorHandler(fmt.Errorf("error sending response: %w", err))
			c.Close()
		}
	}

	c.requestHandler(logger, method, params, cb)
}

func (c *Connection) handleIncomingNotification(method string, params []any) {
	logger := c.logger.LogIncomingNotification(method, params)
	c.notificationHandler(logger, method, params)
}

func (c *Connection) handleIncomingResponse(id MessageID, reqError any, reqResult any) {
	c.activeOutRequestsMutex.Lock()
	req, ok := c.activeOutRequests[id]
	if ok {
		delete(c.activeOutRequests, id)
	}
	c.activeOutRequestsMutex.Unlock()

	if !ok {
		c.errorHandler(fmt.Errorf("invalid ID in request response '%v': double answer or request not sent", id))
		return
	}

	c.logger.LogIncomingResponse(id, req.method, reqResult, reqError)

	req.res(reqResult, reqError)
}

func (c *Connection) Close() {
	_ = c.in.Close()
	_ = c.out.Close()
}

func (c *Connection) sendRequest(method string, params []any, res ResponseHandler) (MessageID, error) {
	if params == nil {
		params = []any{}
	}
	id := MessageID(c.lastOutRequestsIndex.Add(1))

	c.activeOutRequestsMutex.Lock()
	c.activeOutRequests[id] = &outRequest{
		method: method,
		res:    res,
	}
	c.activeOutRequestsMutex.Unlock()

	c.logger.LogOutgoingRequest(id, method, params)

	if err := c.send(messageTypeRequest, id, method, params); err != nil {
		c.activeOutRequestsMutex.Lock()
		delete(c.activeOutRequests, id)
		c.activeOutRequestsMutex.Unlock()
		return 0, fmt.Errorf("sending request: %w", err)
	}

	return id, nil
}

func (c *Connection) SendRequestWithAsyncResult(res ResponseHandler, method string, params ...any) error {
	_, err := c.sendRequest(method, params, res)
	return err
}

func (c *Connection) SendRequest(ctx context.Context, method string, params ...any) (any, any, error) {
	var reqResult, reqError any
	done := make(chan struct{})
	id, err := c.sendRequest(method, params, func(result any, err any) {
		reqResult = result
		reqError = err
		close(done)
	})
	if err != nil {
		return nil, nil, err
	}

	select {
	case <-done:
		// OK
	case <-ctx.Done():
		c.logger.LogOutgoingCancelRequest(id)
		return nil, nil, ctx.Err()
	}

	return reqResult, reqError, nil
}

func (c *Connection) SendNotification(method string, params ...any) error {
	if params == nil {
		params = []any{}
	}

	c.logger.LogOutgoingNotification(method, params)

	if err := c.send(messageTypeNotification, method, params); err != nil {
		return fmt.Errorf("sending notification: %w", err)
	}
	return nil
}

func (c *Connection) send(data ...any) error {
	start := time.Now()

	c.outMutex.Lock()
	err := c.outEncoder.Encode(data)
	c.outMutex.Unlock()
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	c.logger.LogOutgoingDataDelay(elapsed)
	return nil
}
