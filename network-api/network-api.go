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
	_ = router.RegisterMethod("tcp/closeListener", tcpCloseListener)

	_ = router.RegisterMethod("tcp/accept", tcpAccept)
	_ = router.RegisterMethod("tcp/read", tcpRead)
	_ = router.RegisterMethod("tcp/write", tcpWrite)
	_ = router.RegisterMethod("tcp/close", tcpClose)

	_ = router.RegisterMethod("tcp/connectSSL", tcpConnectSSL)

	_ = router.RegisterMethod("udp/connect", udpConnect)
	_ = router.RegisterMethod("udp/write", udpWrite)
	_ = router.RegisterMethod("udp/awaitRead", udpAwaitRead)
	_ = router.RegisterMethod("udp/read", udpRead)
	_ = router.RegisterMethod("udp/close", udpClose)
}

var lock sync.RWMutex
var liveConnections = make(map[uint]net.Conn)
var liveListeners = make(map[uint]net.Listener)
var liveUdpConnections = make(map[uint]net.PacketConn)
var udpReadBuffers = make(map[uint][]byte)
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
	if len(params) != 2 {
		return nil, []any{1, "Invalid number of parameters, expected listen address and port"}
	}
	listenAddr, ok := params[0].(string)
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected string for listen address"}
	}
	listenPort, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected uint16 for listen port"}
	}

	listenAddr = net.JoinHostPort(listenAddr, strconv.FormatUint(uint64(listenPort), 10))

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
	if existsConn {
		delete(liveConnections, id)
	}
	lock.Unlock()

	if !existsConn {
		return nil, []any{2, fmt.Sprintf("Connection not found for ID: %d", id)}
	}

	// Close the connection if it exists
	// We do not return an error to the caller if the close operation fails, as it is not critical,
	// but we only log the error for debugging purposes.
	if err := conn.Close(); err != nil {
		return err.Error(), nil
	}
	return "", nil
}

func tcpCloseListener(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 1 {
		return nil, []any{1, "Invalid number of parameters, expected listener ID"}
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected int for listener ID"}
	}

	lock.Lock()
	listener, existsListener := liveListeners[id]
	if existsListener {
		delete(liveListeners, id)
	}
	lock.Unlock()

	if !existsListener {
		return nil, []any{2, fmt.Sprintf("Listener not found for ID: %d", id)}
	}

	// Close the listener if it exists
	// We do not return an error to the caller if the close operation fails, as it is not critical,
	// but we only log the error for debugging purposes.
	if err := listener.Close(); err != nil {
		return err.Error(), nil
	}
	return "", nil
}

func tcpRead(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 2 && len(params) != 3 {
		return nil, []any{1, "Invalid number of parameters, expected (connection ID, max bytes to read[, optional timeout in ms])"}
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
	var deadline time.Time // default value == no timeout
	if len(params) == 2 {
		// It seems that there is no way to set a 0 ms timeout (immediate return) on a TCP connection.
		// Setting the read deadline to time.Now() will always returns an empty (zero bytes)
		// read, so we set it by default to a very short duration in the future (1 ms).
		deadline = time.Now().Add(time.Millisecond)
	} else if ms, ok := msgpackrpc.ToInt(params[2]); !ok {
		return nil, []any{1, "Invalid parameter type, expected int for timeout in ms"}
	} else if ms > 0 {
		deadline = time.Now().Add(time.Duration(ms) * time.Millisecond)
	} else if ms == 0 {
		// No timeout
	}

	buffer := make([]byte, maxBytes)
	if err := conn.SetReadDeadline(deadline); err != nil {
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

func udpConnect(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
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

	serverAddr = net.JoinHostPort(serverAddr, fmt.Sprintf("%d", serverPort))
	udpAddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return nil, []any{2, "Failed to resolve UDP address: " + err.Error()}
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, []any{2, "Failed to connect to server: " + err.Error()}
	}

	// Successfully opened UDP channel

	id, unlock := takeLockAndGenerateNextID()
	liveUdpConnections[id] = udpConn
	unlock()
	return id, nil
}

func udpWrite(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 4 {
		return nil, []any{1, "Invalid number of parameters, expected udpConnId, dest address, dest port, payload"}
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected int for UDP connection ID"}
	}
	targetIP, ok := params[1].(string)
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected string for server address"}
	}
	targetPort, ok := msgpackrpc.ToUint(params[2])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected uint16 for server port"}
	}
	data, ok := params[3].([]byte)
	if !ok {
		if dataStr, ok := params[3].(string); ok {
			data = []byte(dataStr)
		} else {
			// If data is not []byte or string, return an error
			return nil, []any{1, "Invalid parameter type, expected []byte or string for data to write"}
		}
	}

	lock.RLock()
	udpConn, ok := liveUdpConnections[id]
	lock.RUnlock()
	if !ok {
		return nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)}
	}

	targetAddr := net.JoinHostPort(targetIP, fmt.Sprintf("%d", targetPort))
	addr, err := net.ResolveUDPAddr("udp", targetAddr) // TODO: This is inefficient, implement some caching
	if err != nil {
		return nil, []any{3, "Failed to resolve target address: " + err.Error()}
	}
	if n, err := udpConn.WriteTo(data, addr); err != nil {
		return nil, []any{4, "Failed to write to UDP connection: " + err.Error()}
	} else {
		return n, nil
	}
}

func udpAwaitRead(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 1 && len(params) != 2 {
		return nil, []any{1, "Invalid number of parameters, expected (UDP connection ID[, optional timeout in ms])"}
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected uint for UDP connection ID"}
	}
	var deadline time.Time // default value == no timeout
	if len(params) == 1 {
		// No timeout
	} else if ms, ok := msgpackrpc.ToInt(params[1]); !ok {
		return nil, []any{1, "Invalid parameter type, expected int for timeout in ms"}
	} else if ms > 0 {
		deadline = time.Now().Add(time.Duration(ms) * time.Millisecond)
	} else if ms == 0 {
		// No timeout
	}

	lock.RLock()
	udpConn, ok := liveUdpConnections[id]
	lock.RUnlock()
	if !ok {
		return nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)}
	}
	if err := udpConn.SetReadDeadline(deadline); err != nil {
		return nil, []any{3, "Failed to set read deadline: " + err.Error()}
	}
	buffer := make([]byte, 64*1024) // 64 KB buffer
	n, addr, err := udpConn.ReadFrom(buffer)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		// timeout
		return nil, []any{5, "Timeout"}
	}
	if err != nil {
		return nil, []any{3, "Failed to read from UDP connection: " + err.Error()}
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		// Should never fail, but...
		return nil, []any{4, "Failed to parse source address: " + err.Error()}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		// Should never fail, but...
		return nil, []any{4, "Failed to parse source address: " + err.Error()}
	}

	lock.Lock()
	udpReadBuffers[id] = buffer[:n]
	lock.Unlock()
	return []any{n, host, port}, nil
}

func udpRead(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 2 && len(params) != 3 {
		return nil, []any{1, "Invalid number of parameters, expected (UDP connection ID, max bytes to read)"}
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected uint for UDP connection ID"}
	}
	maxBytes, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected uint for max bytes to read"}
	}

	lock.Lock()
	buffer, exists := udpReadBuffers[id]
	n := uint(len(buffer))
	if exists {
		// keep the remainder of the buffer for the next read
		if n > maxBytes {
			udpReadBuffers[id] = buffer[maxBytes:]
			n = maxBytes
		} else {
			udpReadBuffers[id] = nil
		}
	}
	lock.Unlock()

	return buffer[:n], nil
}

func udpClose(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_result any, _err any) {
	if len(params) != 1 {
		return nil, []any{1, "Invalid number of parameters, expected UDP connection ID"}
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected int for UDP connection ID"}
	}

	lock.Lock()
	udpConn, existsConn := liveUdpConnections[id]
	delete(liveUdpConnections, id)
	delete(udpReadBuffers, id)
	lock.Unlock()

	if !existsConn {
		return nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)}
	}

	// Close the connection if it exists
	// We do not return an error to the caller if the close operation fails, as it is not critical,
	// but we only log the error for debugging purposes.
	if err := udpConn.Close(); err != nil {
		return err.Error(), nil
	}
	return "", nil
}
