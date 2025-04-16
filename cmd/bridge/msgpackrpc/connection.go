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
	errorHandler        func(error)
	requestHandler      RequestHandler
	notificationHandler NotificationHandler
	logger              Logger
	loggerMutex         sync.Mutex

	activeInRequests      map[MessageID]*inRequest
	activeInRequestsMutex sync.Mutex

	activeOutRequests      map[MessageID]*outRequest
	activeOutRequestsMutex sync.Mutex
	lastOutRequestsIndex   atomic.Uint32
}

type inRequest struct {
	cancel func()
}

type outRequest struct {
	resultChan chan<- *outResponse
	method     string
}

type outResponse struct {
	reqError  any
	reqResult any
}

// RequestHandler handles requests from a MessagePack-RPC Connection.
type RequestHandler func(ctx context.Context, logger FunctionLogger, method string, params []any) (result any, err any)

// NotificationHandler handles notifications from a MessagePack-RPC Connection.
type NotificationHandler func(logger FunctionLogger, method string, params []any)

// NewConnection starts a new
func NewConnection(in io.ReadCloser, out io.WriteCloser, requestHandler RequestHandler, notificationHandler NotificationHandler, errorHandler func(error)) *Connection {
	outEncoder := msgpack.NewEncoder(out)
	outEncoder.UseCompactInts(true)
	conn := &Connection{
		in:                  in,
		out:                 out,
		outEncoder:          outEncoder,
		requestHandler:      requestHandler,
		notificationHandler: notificationHandler,
		errorHandler:        errorHandler,
		activeInRequests:    map[MessageID]*inRequest{},
		activeOutRequests:   map[MessageID]*outRequest{},
		logger:              NullLogger{},
	}
	return conn
}

func (c *Connection) SetLogger(l Logger) {
	c.loggerMutex.Lock()
	c.logger = l
	c.loggerMutex.Unlock()
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
		c.loggerMutex.Lock()
		c.logger.LogIncomingDataDelay(elapsed)
		c.loggerMutex.Unlock()

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
	ctx, cancel := context.WithCancel(context.Background())

	c.activeInRequestsMutex.Lock()
	c.activeInRequests[id] = &inRequest{
		cancel: cancel,
	}
	c.activeInRequestsMutex.Unlock()

	c.loggerMutex.Lock()
	logger := c.logger.LogIncomingRequest(id, method, params)
	c.loggerMutex.Unlock()

	go func() {
		reqResult, reqError := c.requestHandler(ctx, logger, method, params)

		c.activeInRequestsMutex.Lock()
		c.activeInRequests[id].cancel()
		delete(c.activeInRequests, id)
		c.activeInRequestsMutex.Unlock()

		c.loggerMutex.Lock()
		c.logger.LogOutgoingResponse(id, method, reqResult, reqError)
		c.loggerMutex.Unlock()

		if err := c.send(messageTypeResponse, id, reqError, reqResult); err != nil {
			c.errorHandler(fmt.Errorf("error sending response: %w", err))
			c.Close()
		}
	}()
}

func (c *Connection) handleIncomingNotification(method string, params []any) {
	if method == "$/cancelRequest" {
		// Send cancelation signal and exit
		if len(params) != 1 {
			c.errorHandler(fmt.Errorf("invalid cancelRequest, expected array with 1 element"))
			return
		}
		id, ok := ToUint(params[0])
		if !ok {
			c.errorHandler(fmt.Errorf("invalid cancelRequest, expected msgid (uint) as first element"))
			return
		}
		c.cancelIncomingRequest(MessageID(id))
		return
	}

	c.loggerMutex.Lock()
	logger := c.logger.LogIncomingNotification(method, params)
	c.loggerMutex.Unlock()

	go c.notificationHandler(logger, method, params)
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

	req.resultChan <- &outResponse{
		reqError:  reqError,
		reqResult: reqResult,
	}
}

func (c *Connection) cancelIncomingRequest(id MessageID) {
	c.activeInRequestsMutex.Lock()
	if req, ok := c.activeInRequests[id]; ok {
		c.loggerMutex.Lock()
		c.logger.LogIncomingCancelRequest(id)
		c.loggerMutex.Unlock()

		req.cancel()
	}
	c.activeInRequestsMutex.Unlock()
}

func (c *Connection) Close() {
	_ = c.in.Close()
	_ = c.out.Close()
}

func (c *Connection) SendRequest(ctx context.Context, method string, params []any) (reqResult any, reqError any, err error) {
	id := MessageID(c.lastOutRequestsIndex.Add(1))

	c.loggerMutex.Lock()
	c.logger.LogOutgoingRequest(id, method, params)
	c.loggerMutex.Unlock()

	resultChan := make(chan *outResponse, 1)
	c.activeOutRequestsMutex.Lock()
	c.activeOutRequests[id] = &outRequest{
		resultChan: resultChan,
		method:     method,
	}
	c.activeOutRequestsMutex.Unlock()

	if err := c.send(messageTypeRequest, id, method, params); err != nil {
		c.activeOutRequestsMutex.Lock()
		delete(c.activeOutRequests, id)
		c.activeOutRequestsMutex.Unlock()
		return nil, nil, fmt.Errorf("sending request: %w", err)
	}

	// Wait the response or send cancel request if requested from context
	var result *outResponse
	select {
	case result = <-resultChan:
		// got result, do nothing

	case <-ctx.Done():
		c.activeOutRequestsMutex.Lock()
		_, active := c.activeOutRequests[id]
		c.activeOutRequestsMutex.Unlock()
		if active {
			c.loggerMutex.Lock()
			c.logger.LogOutgoingCancelRequest(id)
			c.loggerMutex.Unlock()

			_ = c.SendNotification("$/cancelRequest", []any{id}) // ignore error (it won't matter anyway)
		}

		// After cancelation wait for result...
		result = <-resultChan
	}

	c.loggerMutex.Lock()
	c.logger.LogIncomingResponse(id, method, result.reqResult, result.reqError)
	c.loggerMutex.Unlock()

	return result.reqResult, result.reqError, nil
}

func (c *Connection) SendNotification(method string, params []any) error {
	c.loggerMutex.Lock()
	c.logger.LogOutgoingNotification(method, params)
	c.loggerMutex.Unlock()

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

	c.loggerMutex.Lock()
	c.logger.LogOutgoingDataDelay(elapsed)
	c.loggerMutex.Unlock()
	return nil
}
