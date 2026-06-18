// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc

import (
	"context"
	"errors"
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
	in                      io.ReadCloser
	out                     io.WriteCloser
	msgpEncoder             *msgpack.Encoder
	msgpBuffer              *LimitedBuffer
	outMutex                sync.Mutex
	errorHandler            ErrorHandler
	requestHandler          RequestHandler
	notificationHandler     NotificationHandler
	logger                  Logger
	paramsAndResponsesAsRaw bool

	activeOutRequests      map[MessageID]*outRequest
	activeOutRequestsMutex sync.Mutex
	lastOutRequestsIndex   atomic.Uint32
}

type outRequest struct {
	res    ResponseHandler
	method string
}

// RequestHandler handles requests from a MessagePack-RPC Connection.
type RequestHandler func(logger FunctionLogger, method string, params []any, res ResponseSender)

// ResponseHandler is a callback function used to process the result of a request.
// It must be thread-safe (request handlers may decide to handle the request
// asynchronously, calling the response handler from another goroutine when the result is ready).
type ResponseHandler func(result any, err any)

// ResponseSender is a callback function used to send the result of a request back
// to the caller. It must be thread-safe (request handlers may decide to handle the request
// asynchronously, calling the response handler from another goroutine when the result is ready).
// If the response could not be delivered, an error should be returned.
type ResponseSender func(result any, err any) error

// NotificationHandler handles notifications from a MessagePack-RPC Connection.
type NotificationHandler func(logger FunctionLogger, method string, params []any)

// ErrorHandler handles errors from a MessagePack-RPC Connection.
// It is called when an error occurs while reading from the connection or when
// sending a request or notification.
type ErrorHandler func(error)

// NewConnection creates a new MessagePack-RPC Connection handler.
func NewConnection(in io.ReadCloser, out io.WriteCloser, requestHandler RequestHandler, notificationHandler NotificationHandler, errorHandler ErrorHandler) *Connection {
	msgpEncoder := msgpack.NewEncoder(nil)
	msgpEncoder.UseCompactInts(true)
	if requestHandler == nil {
		requestHandler = func(logger FunctionLogger, method string, params []any, res ResponseSender) {
			_ = res(nil, fmt.Errorf("method not implemented: %s", method))
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
		msgpEncoder:         msgpEncoder,
		msgpBuffer:          nil, // no max buffer size by default, can be set with SetMaxBufferSize
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

// SetMaxOutgoingMessageSize sets the maximum buffer size for outgoing messages (default is no limit).
// A value <= 0 means no limit.
func (c *Connection) SetMaxOutgoingMessageSize(size int) {
	c.outMutex.Lock()
	defer c.outMutex.Unlock()

	if size <= 0 {
		// No limits
		c.msgpBuffer = nil
	}

	// Set the buffer and error to be used when a message exceeds the limit
	c.msgpBuffer = NewLimitedBuffer(size)
}

// SetKeepParamsAndResponsesAsRaw sets whether to keep the parameters and responses as raw bytes when decoding them.
// When set to true, the parameters and responses will be kept as msgpack.RawMessage, allowing for lazy decoding.
func (c *Connection) SetKeepParamsAndResponsesAsRaw(keepAsRaw bool) {
	c.paramsAndResponsesAsRaw = keepAsRaw
}

type rpcPacket struct {
	msgType   int
	msgID     MessageID
	method    string
	params    []any
	reqError  any
	reqResult any
}

// decodeRpcPacket decodes a MessagePack-RPC packet from the given decoder.
// It returns an rpcPacket struct containing the decoded data, or an error
// if the packet is invalid, in such case the invalid packet is skipped.
func (c *Connection) decodeRpcPacket(in *msgpack.Decoder) (rpcPacket, error) {
	// This function is used to skip the invalid packet and return an error.
	// n elements will be skipped from the decoder, and the error will be returned.
	// If also skipping fails, the skipping error will be returned, with the original error incorporated.
	skipAndError := func(n int, err error) (rpcPacket, error) {
		for range n {
			if skipErr := in.Skip(); skipErr != nil {
				return rpcPacket{}, fmt.Errorf("failed to skip invalid packet: %w; invalid packet: %v", skipErr, err)
			}
		}
		return rpcPacket{}, err
	}

	l, err := in.DecodeArrayLen()
	if err != nil {
		return skipAndError(1, fmt.Errorf("invalid packet, expected array: %w", err))
	}

	if l < 3 || l > 4 {
		return skipAndError(l, errors.New("invalid packet, expected array with 3 or 4 elements"))
	}

	var packet rpcPacket
	if msgType, err := in.DecodeInt(); err != nil {
		return skipAndError(l, fmt.Errorf("invalid packet, expected int for message type: %w", err))
	} else {
		packet.msgType = msgType
		l--
		switch msgType {
		case messageTypeRequest, messageTypeResponse:
			if l != 3 {
				return skipAndError(l, errors.New("invalid packet, expected array with 4 elements for request or response"))
			}
		case messageTypeNotification:
			if l != 2 {
				return skipAndError(l, errors.New("invalid packet, expected array with 3 elements for notification"))
			}
		default:
			return skipAndError(l, errors.New("invalid packet, expected request, response or notification"))
		}
	}

	// Decode the message ID for requests and responses
	switch packet.msgType {
	case messageTypeRequest, messageTypeResponse:
		if id, err := in.DecodeUint(); err != nil {
			return skipAndError(l, fmt.Errorf("invalid packet, expected uint for message ID: %w", err))
		} else {
			packet.msgID = MessageID(id)
			l--
		}
	}

	switch packet.msgType {
	case messageTypeRequest, messageTypeNotification:
		if method, err := in.DecodeString(); err != nil {
			return skipAndError(l, fmt.Errorf("invalid packet, expected string for method name: %w", err))
		} else {
			packet.method = method
			l--
		}

		paramLen, err := in.DecodeArrayLen()
		if err != nil {
			return skipAndError(l, fmt.Errorf("invalid packet, expected array for params: %w", err))
		}
		l--
		if paramLen < 0 {
			return skipAndError(l, fmt.Errorf("invalid packet, error decoding params: expected array, got Nil"))
		}

		// Now remains only the params to decode, which is an array of length paramLen

		packet.params = make([]any, paramLen)
		toSkip := paramLen
		for i := range paramLen {
			var p any
			if c.paramsAndResponsesAsRaw {
				p, err = in.DecodeRaw()
			} else {
				p, err = in.DecodeInterface()
			}
			if err != nil {
				return skipAndError(toSkip, fmt.Errorf("invalid packet, error decoding param %d: %w", i, err))
			}
			packet.params[i] = p
			toSkip--
		}

	case messageTypeResponse:
		if c.paramsAndResponsesAsRaw {
			if e, err := in.DecodeRaw(); err != nil {
				return skipAndError(l, fmt.Errorf("invalid packet, error decoding response: %w", err))
			} else {
				packet.reqError = e
				l--
			}
			if r, err := in.DecodeRaw(); err != nil {
				return skipAndError(l, fmt.Errorf("invalid packet, error decoding response: %w", err))
			} else {
				packet.reqResult = r
			}
		} else {
			if e, err := in.DecodeInterface(); err != nil {
				return skipAndError(l, fmt.Errorf("invalid packet, error decoding response: %w", err))
			} else {
				packet.reqError = e
				l--
			}
			if r, err := in.DecodeInterface(); err != nil {
				return skipAndError(l, fmt.Errorf("invalid packet, error decoding response: %w", err))
			} else {
				packet.reqResult = r
			}
		}
	}

	return packet, nil
}

func (c *Connection) Run() {
	in := msgpack.NewDecoder(c.in)
	for {
		start := time.Now()
		packet, err := c.decodeRpcPacket(in)
		if err != nil {
			c.errorHandler(fmt.Errorf("decoding packet: %w", err))
			continue
		}
		elapsed := time.Since(start)
		c.logger.LogIncomingDataDelay(elapsed)

		switch packet.msgType {
		case messageTypeRequest:
			c.handleIncomingRequest(packet.msgID, packet.method, packet.params)
		case messageTypeResponse:
			c.handleIncomingResponse(packet.msgID, packet.reqError, packet.reqResult)
		case messageTypeNotification:
			c.handleIncomingNotification(packet.method, packet.params)
		}
	}
}

func (c *Connection) handleIncomingRequest(id MessageID, method string, params []any) {
	logger := c.logger.LogIncomingRequest(id, method, params)

	// This callback may be called by another goroutine, because the request handler
	// may want to process the request asynchronously.
	cb := func(reqResult, reqError any) error {
		c.logger.LogOutgoingResponse(id, method, reqResult, reqError)
		return c.send(messageTypeResponse, id, reqError, reqResult)
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
	doSend := func() error {
		c.outMutex.Lock()
		defer c.outMutex.Unlock()

		if c.msgpBuffer == nil {
			// Unlimited buffer size, write directly to the out stream without buffering
			c.msgpEncoder.Reset(c.out)
			c.msgpEncoder.UseCompactInts(true)
			return c.msgpEncoder.Encode(data)
		}

		// Limited buffer size, write to the buffer first and then flush it to the out stream
		c.msgpBuffer.Reset()
		c.msgpEncoder.Reset(c.msgpBuffer)
		c.msgpEncoder.UseCompactInts(true)
		if err := c.msgpEncoder.Encode(data); err != nil {
			return err
		}
		_, err := c.out.Write(c.msgpBuffer.Bytes())
		return err
	}

	start := time.Now()
	if err := doSend(); err != nil {
		return err
	}
	elapsed := time.Since(start)

	c.logger.LogOutgoingDataDelay(elapsed)
	return nil
}
