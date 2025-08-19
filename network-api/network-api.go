package networkapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arduino/arduino-router/msgpackrouter"
	"github.com/arduino/arduino-router/msgpackrpc"
)

// Register the Network API methods
func Register(router *msgpackrouter.Router) {
	_ = router.RegisterMethod("tcp/connect", tcpConnect)

	_ = router.RegisterMethod("tcp/listen", tcpListen)
	_ = router.RegisterMethod("tcp/accept", tcpAccept)

	_ = router.RegisterMethod("tcp/read", tcpRead)
	_ = router.RegisterMethod("tcp/write", tcpWrite)
	_ = router.RegisterMethod("tcp/close", tcpClose)

	_ = router.RegisterMethod("tcp/connectSSL", tcpConnectSSL)

}

var lock sync.RWMutex
var liveConnections = make(map[uint]net.Conn)
var liveListeners = make(map[uint]net.Listener)
var nextConnectionID atomic.Uint32

// takeLockAndGenerateNextID generates a new unique ID for a connection or listener.
// It locks the global lock to ensure thread safety and checks for existing IDs.
// It returns the new ID and a function to unlock the global lock.
func takeLockAndGenerateNextID() (newID uint, unlock func()) {
	lock.Lock()
	for {
		id := uint(nextConnectionID.Add(1))
		_, exists1 := liveConnections[id]
		_, exists2 := liveListeners[id]
		if !exists1 && !exists2 {
			return id, func() {
				lock.Unlock()
			}
		}
	}
}

func tcpConnect(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 2 {
		return nil, []any{1, "Invalid number of parameters, expected server address and port"}
	}
	serverAddr, ok := params[0].(string)
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected string for server address"}
	}
	serverPort, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected uint16 for server port"}
	}

	serverAddr = net.JoinHostPort(serverAddr, strconv.FormatUint(uint64(serverPort), 10))

	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		return nil, []any{2, "Failed to connect to server: " + err.Error()}
	}

	// Successfully connected to the server

	id, unlock := takeLockAndGenerateNextID()
	liveConnections[id] = conn
	unlock()
	return id, nil
}

func tcpListen(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 1 {
		return nil, []any{1, "Invalid number of parameters, expected listen address"}
	}
	listenAddr, ok := params[0].(string)
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected string for listen address"}
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, []any{2, "Failed to start listening on address: " + err.Error()}
	}

	id, unlock := takeLockAndGenerateNextID()
	liveListeners[id] = listener
	unlock()
	return id, nil
}

func tcpAccept(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 1 {
		return nil, []any{1, "Invalid number of parameters, expected listener ID"}
	}
	listenerID, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected int for listener ID"}
	}

	lock.RLock()
	listener, exists := liveListeners[listenerID]
	lock.RUnlock()

	if !exists {
		return nil, []any{2, fmt.Sprintf("Listener not found for ID: %d", listenerID)}
	}

	conn, err := listener.Accept()
	if err != nil {
		return nil, []any{3, "Failed to accept connection: " + err.Error()}
	}

	// Successfully accepted a connection

	connID, unlock := takeLockAndGenerateNextID()
	liveConnections[connID] = conn
	unlock()
	return connID, nil
}

func tcpClose(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 1 {
		return nil, []any{1, "Invalid number of parameters, expected connection ID"}
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected int for connection ID"}
	}

	lock.Lock()
	conn, existsConn := liveConnections[id]
	listener, existsListener := liveListeners[id]
	if existsConn {
		delete(liveConnections, id)
	}
	if existsListener {
		delete(liveListeners, id)
	}
	lock.Unlock()

	if !existsConn && !existsListener {
		return nil, []any{2, fmt.Sprintf("Connection not found for ID: %d", id)}
	}

	// Close the connection or listener if it exists
	// We do not return an error if the close operation fails, as it is not critical,
	// but we only log the error for debugging purposes.
	if existsConn {
		if err := conn.Close(); err != nil {
			return err.Error(), nil
		}
	}
	if existsListener {
		if err := listener.Close(); err != nil {
			return err.Error(), nil
		}
	}

	return "", nil
}

func tcpRead(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 2 {
		return nil, []any{1, "Invalid number of parameters, expected (connection ID, max bytes to read)"}
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected int for connection ID"}
	}
	lock.RLock()
	conn, ok := liveConnections[id]
	lock.RUnlock()
	if !ok {
		return nil, []any{2, fmt.Sprintf("Connection not found for ID: %d", id)}
	}
	maxBytes, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected int for max bytes to read"}
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
		return nil, []any{3, "Failed to read from connection: " + err.Error()}
	}

	return buffer[:n], nil
}

func tcpWrite(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 2 {
		return nil, []any{1, "Invalid number of parameters, expected (connection ID, data to write)"}
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected int for connection ID"}
	}
	lock.RLock()
	conn, ok := liveConnections[id]
	lock.RUnlock()
	if !ok {
		return nil, []any{2, fmt.Sprintf("Connection not found for ID: %d", id)}
	}
	data, ok := params[1].([]byte)
	if !ok {
		if dataStr, ok := params[1].(string); ok {
			data = []byte(dataStr)
		} else {
			// If data is not []byte or string, return an error
			return nil, []any{1, "Invalid parameter type, expected []byte or string for data to write"}
		}
	}

	n, err := conn.Write(data)
	if err != nil {
		return nil, []any{3, "Failed to write to connection: " + err.Error()}
	}

	return n, nil
}

func tcpConnectSSL(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	n := len(params)
	if n < 1 || n > 3 {
		return nil, []any{1, "Invalid number of parameters, expected server address, port and optional TLS cert"}
	}
	serverAddr, ok := params[0].(string)
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected string for server address"}
	}
	serverPort, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected uint16 for server port"}
	}

	serverAddr = net.JoinHostPort(serverAddr, strconv.FormatUint(uint64(serverPort), 10))

	var tlsConfig *tls.Config
	if n == 3 {
		cert, ok := params[2].(string)
		if !ok {
			return nil, []any{1, "Invalid parameter type, expected string for TLS cert"}
		}

		if len(cert) > 0 {
			// parse TLS cert in pem format
			certs := x509.NewCertPool()
			if !certs.AppendCertsFromPEM([]byte(cert)) {
				return nil, []any{1, "Failed to parse TLS certificate"}
			}
			tlsConfig = &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    certs,
			}
		}
	}

	conn, err := tls.Dial("tcp", serverAddr, tlsConfig)
	if err != nil {
		return nil, []any{2, "Failed to connect to server: " + err.Error()}
	}

	// Successfully connected to the server

	id, unlock := takeLockAndGenerateNextID()
	liveConnections[id] = conn
	unlock()
	return id, nil
}
