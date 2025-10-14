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

package monitorapi

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/djherbis/buffer"
	"github.com/djherbis/nio/v3"

	"github.com/arduino/arduino-router/msgpackrouter"
	"github.com/arduino/arduino-router/msgpackrpc"
)

var socketsLock sync.RWMutex
var sockets map[net.Conn]struct{}
var monSendPipeRd *nio.PipeReader
var monSendPipeWr *nio.PipeWriter
var bytesInSendPipe atomic.Int64

// Register the Monitor API methods
func Register(router *msgpackrouter.Router, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start listener: %w", err)
	}
	sockets = make(map[net.Conn]struct{})
	monSendPipeRd, monSendPipeWr = nio.Pipe(buffer.New(1024))

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
		socketsLock.Lock()
		sockets[conn] = struct{}{}
		socketsLock.Unlock()

		go func() {
			defer close(conn)

			// Read from the connection and write to the monitor send pipe
			buff := make([]byte, 1024)
			for {
				if n, err := conn.Read(buff); err != nil {
					// Connection closed from client
					return
				} else if written, err := monSendPipeWr.Write(buff[:n]); err != nil {
					return
				} else {
					bytesInSendPipe.Add(int64(written))
				}
			}
		}()
	}
}

func connected(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 0 {
		return nil, []any{1, "Invalid number of parameters, expected no parameters"}
	}

	socketsLock.RLock()
	connected := len(sockets) > 0
	socketsLock.RUnlock()

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

	if bytesInSendPipe.Load() == 0 {
		return []byte{}, nil
	}

	buffer := make([]byte, maxBytes)
	if readed, err := monSendPipeRd.Read(buffer); err != nil {
		slog.Error("Error reading monitor", "error", err)
		return nil, []any{3, "Failed to read from connection: " + err.Error()}
	} else {
		bytesInSendPipe.Add(int64(-readed))
		return buffer[:readed], nil
	}
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

	socketsLock.RLock()
	clients := make([]net.Conn, 0, len(sockets))
	for c := range sockets {
		clients = append(clients, c)
	}
	socketsLock.RUnlock()

	for _, conn := range clients {
		if len(clients) > 1 {
			// If there are multiple clients, allow 500 ms for the data to
			// get through each one.
			_ = conn.SetWriteDeadline(time.Now().Add(time.Millisecond * 500))
		} else {
			_ = conn.SetWriteDeadline(time.Time{})
		}
		if _, err := conn.Write(data); err != nil {
			// If we get an error, we assume the connection is lost.
			slog.Error("Monitor connection lost, closing connection", "error", err)
			close(conn)
		}
	}

	return len(data), nil
}

func close(conn net.Conn) {
	socketsLock.Lock()
	delete(sockets, conn)
	socketsLock.Unlock()
	_ = conn.Close()
}

func reset(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 0 {
		return nil, []any{1, "Invalid number of parameters, expected no parameters"}
	}

	socketsLock.Lock()
	socketsToClose := sockets
	sockets = make(map[net.Conn]struct{})
	socketsLock.Unlock()

	for c := range socketsToClose {
		_ = c.Close()
	}

	slog.Info("Monitor connection reset")
	return true, nil
}
