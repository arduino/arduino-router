package monitorapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/arduino/arduino-router/msgpackrouter"
	"github.com/arduino/arduino-router/msgpackrpc"
)

var lock sync.RWMutex
var socket net.Conn
var monitorConnectionLost sync.Cond = *sync.NewCond(&lock)

// Register the Monitor API methods
func Register(router *msgpackrouter.Router, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}
	go connectionHandler(listener)
	_ = router.RegisterMethod("mon/connected", connected)
	_ = router.RegisterMethod("mon/read", read)
	_ = router.RegisterMethod("mon/write", write)
	_ = router.RegisterMethod("mon/reset", reset)
	return nil
}

func connectionHandler(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			slog.Error("Failed to accept monitor connection", "error", err)
			return
		}

		slog.Info("Accepted monitor connection", "from", conn.RemoteAddr())
		lock.Lock()
		socket = conn
		lock.Unlock()

		lock.Lock()
		for socket != nil {
			monitorConnectionLost.Wait()
		}
		lock.Unlock()
	}
}

func connected(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 0 {
		return nil, []any{1, "Invalid number of parameters, expected no parameters"}
	}

	lock.RLock()
	connected := socket != nil
	lock.RUnlock()

	return connected, nil
}

func read(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 1 {
		return nil, []any{1, "Invalid number of parameters, expected max bytes to read"}
	}
	maxBytes, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected positive int for max bytes to read"}
	}

	lock.RLock()
	conn := socket
	lock.RUnlock()

	// No active connection, return empty slice
	if conn == nil {
		return []byte{}, nil
	}

	buffer := make([]byte, maxBytes)
	// It seems that the only way to make a non-blocking read is to set a read deadline.
	// BTW setting the read deadline to time.Now() will always returns an empty (zero bytes)
	// read, so we set it to a very short duration in the future.
	if err := conn.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
		return nil, []any{3, "Failed to set read timeout: " + err.Error()}
	}
	n, err := conn.Read(buffer)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		// timeout
	} else if err != nil {
		// If we get an error other than timeout, we assume the connection is lost.
		slog.Error("Monitor connection lost, closing connection", "error", err)
		close()
		return nil, []any{3, "Failed to read from connection: " + err.Error()}
	}

	return buffer[:n], nil
}

func write(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 1 {
		return nil, []any{1, "Invalid number of parameters, expected data to write"}
	}
	data, ok := params[0].([]byte)
	if !ok {
		if dataStr, ok := params[0].(string); ok {
			data = []byte(dataStr)
		} else {
			// If data is not []byte or string, return an error
			return nil, []any{1, "Invalid parameter type, expected []byte or string for data to write"}
		}
	}

	lock.RLock()
	conn := socket
	lock.RUnlock()

	if conn == nil { // No active connection, drop the data
		return len(data), nil
	}

	n, err := conn.Write(data)
	if err != nil {
		// If we get an error, we assume the connection is lost.
		slog.Error("Monitor connection lost, closing connection", "error", err)
		close()

		return nil, []any{3, "Failed to write to connection: " + err.Error()}
	}

	return n, nil
}

func reset(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 0 {
		return nil, []any{1, "Invalid number of parameters, expected no parameters"}
	}
	close()
	slog.Info("Monitor connection reset")
	return true, nil
}

func close() {
	lock.Lock()
	if socket != nil {
		_ = socket.Close()
	}
	socket = nil
	monitorConnectionLost.Broadcast()
	lock.Unlock()
}
