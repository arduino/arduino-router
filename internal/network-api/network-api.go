// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package networkapi

import (
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

	"github.com/arduino/arduino-router/internal/msgpackrouter"

	"github.com/arduino/arduino-router/msgpackrpc"

	"go.bug.st/f"
)

// Register the Network API methods
func Register(router *msgpackrouter.Router) {
	f.NoError(router.RegisterMethod("tcp/connect", tcpConnect))

	f.NoError(router.RegisterMethod("tcp/listen", tcpListen))
	f.NoError(router.RegisterMethod("tcp/closeListener", tcpCloseListener))

	f.NoError(router.RegisterMethod("tcp/accept", tcpAccept))
	f.NoError(router.RegisterMethod("tcp/read", tcpRead))
	f.NoError(router.RegisterMethod("tcp/write", tcpWrite))
	f.NoError(router.RegisterMethod("tcp/close", tcpClose))

	f.NoError(router.RegisterMethod("tcp/connectSSL", tcpConnectSSL))

	f.NoError(router.RegisterMethod("udp/connect", udpConnect))
	f.NoError(router.RegisterMethod("udp/beginPacket", udpBeginPacket))
	f.NoError(router.RegisterMethod("udp/write", udpWrite))
	f.NoError(router.RegisterMethod("udp/endPacket", udpEndPacket))
	f.NoError(router.RegisterMethod("udp/awaitPacket", udpAwaitPacket))
	f.NoError(router.RegisterMethod("udp/read", udpRead))
	f.NoError(router.RegisterMethod("udp/dropPacket", udpDropPacket))
	f.NoError(router.RegisterMethod("udp/close", udpClose))
}

// socketID uniquely identifies a network socket by its ID and the associated rpc connection.
type socketID struct {
	id   uint
	conn *msgpackrpc.Connection
}

var lock sync.RWMutex
var liveConnections = make(map[socketID]net.Conn)
var liveListeners = make(map[socketID]net.Listener)
var liveUdpConnections = make(map[socketID]net.PacketConn)
var udpReadBuffers = make(map[socketID][]byte)
var udpWriteTargets = make(map[socketID]*net.UDPAddr)
var udpWriteBuffers = make(map[socketID][]byte)
var nextConnectionID atomic.Uint32

// Common errors
var errInvalidServerAddressType = []any{1, "Invalid parameter type, expected string for server address"}
var errInvalidServerPortType = []any{1, "Invalid parameter type, expected uint16 for server port"}
var errInvalidUDPConnectionIDType = []any{1, "Invalid parameter type, expected int for UDP connection ID"}
var errInvalidConnectionIDType = []any{1, "Invalid parameter type, expected int for connection ID"}
var errInvalidUDPUIntIDType = []any{1, "Invalid parameter type, expected uint for UDP connection ID"}

// takeLockAndGenerateNextID generates a new socketID for a connection or listener.
// It locks the global lock to ensure thread safety and checks for existing IDs.
// It returns the new ID and a function to unlock the global lock, when the ID has
// been used by the caller.
func takeLockAndGenerateNextID(rpc *msgpackrpc.Connection) (newID socketID, unlock func()) {
	lock.Lock()
	for {
		id := uint(nextConnectionID.Add(1))
		newID = socketID{id: id, conn: rpc}
		_, exists1 := liveConnections[newID]
		_, exists2 := liveListeners[newID]
		if !exists1 && !exists2 {
			return newID, func() {
				lock.Unlock()
			}
		}
	}
}

func tcpConnect(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 2 {
		res(nil, []any{1, "Invalid number of parameters, expected server address and port"})
		return
	}
	serverAddr, ok := params[0].(string)
	if !ok {
		res(nil, errInvalidServerAddressType)
		return
	}
	serverPort, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		res(nil, errInvalidServerPortType)
		return
	}

	serverAddr = net.JoinHostPort(serverAddr, strconv.FormatUint(uint64(serverPort), 10))

	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		res(nil, []any{2, "Failed to connect to server: " + err.Error()})
		return
	}

	// Successfully connected to the server

	sockId, unlock := takeLockAndGenerateNextID(rpc)
	liveConnections[sockId] = conn
	unlock()
	res(sockId.id, nil)
}

func tcpListen(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 2 {
		res(nil, []any{1, "Invalid number of parameters, expected listen address and port"})
		return
	}
	listenAddr, ok := params[0].(string)
	if !ok {
		res(nil, []any{1, "Invalid parameter type, expected string for listen address"})
		return
	}
	listenPort, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		res(nil, []any{1, "Invalid parameter type, expected uint16 for listen port"})
		return
	}

	listenAddr = net.JoinHostPort(listenAddr, strconv.FormatUint(uint64(listenPort), 10))

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		res(nil, []any{2, "Failed to start listening on address: " + err.Error()})
		return
	}

	sockId, unlock := takeLockAndGenerateNextID(rpc)
	liveListeners[sockId] = listener
	unlock()
	res(sockId.id, nil)
}

func tcpAccept(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 {
		res(nil, []any{1, "Invalid number of parameters, expected listener ID"})
		return
	}
	listenerID, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, []any{1, "Invalid parameter type, expected int for listener ID"})
		return
	}

	lock.RLock()
	listener, exists := liveListeners[socketID{conn: rpc, id: listenerID}]
	lock.RUnlock()

	if !exists {
		res(nil, []any{2, fmt.Sprintf("Listener not found for ID: %d", listenerID)})
		return
	}

	sock, err := listener.Accept()
	if err != nil {
		res(nil, []any{3, "Failed to accept connection: " + err.Error()})
		return
	}

	// Successfully accepted a connection

	sockID, unlock := takeLockAndGenerateNextID(rpc)
	liveConnections[sockID] = sock
	unlock()
	res(sockID.id, nil)
}

func tcpClose(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 {
		res(nil, []any{1, "Invalid number of parameters, expected connection ID"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidConnectionIDType)
		return
	}

	sockId := socketID{conn: rpc, id: id}
	lock.Lock()
	conn, existsConn := liveConnections[sockId]
	if existsConn {
		delete(liveConnections, sockId)
	}
	lock.Unlock()

	if !existsConn {
		res(nil, []any{2, fmt.Sprintf("Connection not found for ID: %d", id)})
		return
	}

	// Close the connection if it exists
	// We do not return an error to the caller if the close operation fails, as it is not critical,
	// but we only log the error for debugging purposes.
	if err := conn.Close(); err != nil {
		res(err.Error(), nil)
		return
	}
	res("", nil)
}

func tcpCloseListener(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 {
		res(nil, []any{1, "Invalid number of parameters, expected listener ID"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, []any{1, "Invalid parameter type, expected int for listener ID"})
		return
	}

	sockId := socketID{conn: rpc, id: id}
	lock.Lock()
	listener, existsListener := liveListeners[sockId]
	if existsListener {
		delete(liveListeners, sockId)
	}
	lock.Unlock()

	if !existsListener {
		res(nil, []any{2, fmt.Sprintf("Listener not found for ID: %d", id)})
		return
	}

	// Close the listener if it exists
	// We do not return an error to the caller if the close operation fails, as it is not critical,
	// but we only log the error for debugging purposes.
	if err := listener.Close(); err != nil {
		res(err.Error(), nil)
		return
	}
	res("", nil)
}

func tcpRead(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 2 && len(params) != 3 {
		res(nil, []any{1, "Invalid number of parameters, expected (connection ID, max bytes to read[, optional timeout in ms])"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidConnectionIDType)
		return
	}
	lock.RLock()
	conn, ok := liveConnections[socketID{conn: rpc, id: id}]
	lock.RUnlock()
	if !ok {
		res(nil, []any{2, fmt.Sprintf("Connection not found for ID: %d", id)})
		return
	}
	maxBytes, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		res(nil, []any{1, "Invalid parameter type, expected int for max bytes to read"})
		return
	}
	var deadline time.Time // default value == no timeout
	if len(params) == 2 {
		// It seems that there is no way to set a 0 ms timeout (immediate return) on a TCP connection.
		// Setting the read deadline to time.Now() will always returns an empty (zero bytes)
		// read, so we set it by default to a very short duration in the future (1 ms).
		deadline = time.Now().Add(time.Millisecond)
	} else if ms, ok := msgpackrpc.ToInt(params[2]); !ok {
		res(nil, []any{1, "Invalid parameter type, expected int for timeout in ms"})
		return
	} else if ms > 0 {
		deadline = time.Now().Add(time.Duration(ms) * time.Millisecond)
	}

	buffer := make([]byte, maxBytes)
	if err := conn.SetReadDeadline(deadline); err != nil {
		res(nil, []any{3, "Failed to set read timeout: " + err.Error()})
		return
	}
	n, err := conn.Read(buffer)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		// timeout
	} else if err != nil {
		res(nil, []any{3, "Failed to read from connection: " + err.Error()})
		return
	}

	res(buffer[:n], nil)
}

func tcpWrite(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 2 {
		res(nil, []any{1, "Invalid number of parameters, expected (connection ID, data to write)"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidConnectionIDType)
		return
	}
	lock.RLock()
	conn, ok := liveConnections[socketID{conn: rpc, id: id}]
	lock.RUnlock()
	if !ok {
		res(nil, []any{2, fmt.Sprintf("Connection not found for ID: %d", id)})
		return
	}
	data, ok := params[1].([]byte)
	if !ok {
		if dataStr, ok := params[1].(string); ok {
			data = []byte(dataStr)
		} else {
			// If data is not []byte or string, return an error
			res(nil, []any{1, "Invalid parameter type, expected []byte or string for data to write"})
			return
		}
	}

	n, err := conn.Write(data)
	if err != nil {
		res(nil, []any{3, "Failed to write to connection: " + err.Error()})
		return
	}

	res(n, nil)
}

func tcpConnectSSL(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	n := len(params)
	if n < 1 || n > 3 {
		res(nil, []any{1, "Invalid number of parameters, expected server address, port and optional TLS cert"})
		return
	}
	serverAddr, ok := params[0].(string)
	if !ok {
		res(nil, errInvalidServerAddressType)
		return
	}
	serverPort, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		res(nil, errInvalidServerPortType)
		return
	}

	serverAddr = net.JoinHostPort(serverAddr, strconv.FormatUint(uint64(serverPort), 10))

	var tlsConfig *tls.Config
	if n == 3 {
		cert, ok := params[2].(string)
		if !ok {
			res(nil, []any{1, "Invalid parameter type, expected string for TLS cert"})
			return
		}

		if len(cert) > 0 {
			// parse TLS cert in pem format
			certs := x509.NewCertPool()
			if !certs.AppendCertsFromPEM([]byte(cert)) {
				res(nil, []any{1, "Failed to parse TLS certificate"})
				return
			}
			tlsConfig = &tls.Config{
				MinVersion: tls.VersionTLS12,
				RootCAs:    certs,
			}
		}
	}

	conn, err := tls.Dial("tcp", serverAddr, tlsConfig)
	if err != nil {
		res(nil, []any{2, "Failed to connect to server: " + err.Error()})
		return
	}

	// Successfully connected to the server

	sockId, unlock := takeLockAndGenerateNextID(rpc)
	liveConnections[sockId] = conn
	unlock()
	res(sockId.id, nil)
}

func udpConnect(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 2 {
		res(nil, []any{1, "Invalid number of parameters, expected server address and port"})
		return
	}
	serverAddr, ok := params[0].(string)
	if !ok {
		res(nil, errInvalidServerAddressType)
		return
	}
	serverPort, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		res(nil, errInvalidServerPortType)
		return
	}

	serverAddr = net.JoinHostPort(serverAddr, fmt.Sprintf("%d", serverPort))
	udpAddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		res(nil, []any{2, "Failed to resolve UDP address: " + err.Error()})
		return
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		res(nil, []any{2, "Failed to connect to server: " + err.Error()})
		return
	}

	// Successfully opened UDP channel

	sockId, unlock := takeLockAndGenerateNextID(rpc)
	liveUdpConnections[sockId] = udpConn
	unlock()
	res(sockId.id, nil)
}

func udpBeginPacket(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 3 {
		res(nil, []any{1, "Invalid number of parameters, expected udpConnId, dest address, dest port"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidUDPConnectionIDType)
		return
	}
	targetIP, ok := params[1].(string)
	if !ok {
		res(nil, errInvalidServerAddressType)
		return
	}
	targetPort, ok := msgpackrpc.ToUint(params[2])
	if !ok {
		res(nil, errInvalidServerPortType)
		return
	}

	sockId := socketID{conn: rpc, id: id}
	lock.RLock()
	defer lock.RUnlock()
	if _, ok := liveUdpConnections[sockId]; !ok {
		res(nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)})
		return
	}
	targetAddr := net.JoinHostPort(targetIP, fmt.Sprintf("%d", targetPort))
	addr, err := net.ResolveUDPAddr("udp", targetAddr) // TODO: This is inefficient, implement some caching
	if err != nil {
		res(nil, []any{3, "Failed to resolve target address: " + err.Error()})
		return
	}
	udpWriteTargets[sockId] = addr
	udpWriteBuffers[sockId] = nil
	res(true, nil)
}

func udpWrite(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 2 {
		res(nil, []any{1, "Invalid number of parameters, expected udpConnId, payload"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidUDPConnectionIDType)
		return
	}
	data, ok := params[1].([]byte)
	if !ok {
		if dataStr, ok := params[1].(string); ok {
			data = []byte(dataStr)
		} else {
			// If data is not []byte or string, return an error
			res(nil, []any{1, "Invalid parameter type, expected []byte or string for data to write"})
			return
		}
	}

	sockId := socketID{conn: rpc, id: id}
	lock.RLock()
	udpBuffer, ok := udpWriteBuffers[sockId]
	if ok {
		udpWriteBuffers[sockId] = append(udpBuffer, data...)
	}
	lock.RUnlock()
	if !ok {
		res(nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)})
		return
	}
	res(len(data), nil)
}

func udpEndPacket(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 {
		res(nil, []any{1, "Invalid number of parameters, expected expected udpConnId"})
		return
	}
	id, buffExists := msgpackrpc.ToUint(params[0])
	if !buffExists {
		res(nil, errInvalidUDPConnectionIDType)
		return
	}

	var udpBuffer []byte
	var udpAddr *net.UDPAddr
	lock.RLock()
	sockId := socketID{conn: rpc, id: id}
	udpConn, connExists := liveUdpConnections[sockId]
	if connExists {
		udpBuffer, buffExists = udpWriteBuffers[sockId]
		udpAddr = udpWriteTargets[sockId]
		delete(udpWriteBuffers, sockId)
		delete(udpWriteTargets, sockId)
	}
	lock.RUnlock()
	if !connExists {
		res(nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)})
		return
	}
	if !buffExists {
		res(nil, []any{3, fmt.Sprintf("No UDP packet begun for ID: %d", id)})
		return
	}

	if n, err := udpConn.WriteTo(udpBuffer, udpAddr); err != nil {
		res(nil, []any{4, "Failed to write to UDP connection: " + err.Error()})
	} else {
		res(n, nil)
	}
}

func udpAwaitPacket(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 && len(params) != 2 {
		res(nil, []any{1, "Invalid number of parameters, expected (UDP connection ID[, optional timeout in ms])"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidUDPUIntIDType)
		return
	}
	var deadline time.Time // default value == no timeout
	if len(params) == 2 {
		if ms, ok := msgpackrpc.ToInt(params[1]); !ok {
			res(nil, []any{1, "Invalid parameter type, expected int for timeout in ms"})
			return
		} else if ms > 0 {
			deadline = time.Now().Add(time.Duration(ms) * time.Millisecond)
		}
	}

	sockId := socketID{conn: rpc, id: id}
	lock.RLock()
	udpConn, ok := liveUdpConnections[sockId]
	lock.RUnlock()
	if !ok {
		res(nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)})
		return
	}
	if err := udpConn.SetReadDeadline(deadline); err != nil {
		res(nil, []any{3, "Failed to set read deadline: " + err.Error()})
		return
	}
	buffer := make([]byte, 64*1024) // 64 KB buffer
	n, addr, err := udpConn.ReadFrom(buffer)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		// timeout
		res(nil, []any{5, "Timeout"})
		return
	}
	if err != nil {
		res(nil, []any{3, "Failed to read from UDP connection: " + err.Error()})
		return
	}
	host, portStr, err := net.SplitHostPort(addr.String())
	if err != nil {
		// Should never fail, but...
		res(nil, []any{4, "Failed to parse source address: " + err.Error()})
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		// Should never fail, but...
		res(nil, []any{4, "Failed to parse source address: " + err.Error()})
		return
	}

	lock.Lock()
	udpReadBuffers[sockId] = buffer[:n]
	lock.Unlock()
	res([]any{n, host, port}, nil)
}

func udpDropPacket(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 && len(params) != 2 {
		res(nil, []any{1, "Invalid number of parameters, expected (UDP connection ID[, optional timeout in ms])"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidUDPUIntIDType)
		return
	}

	lock.RLock()
	delete(udpReadBuffers, socketID{conn: rpc, id: id})
	lock.RUnlock()
	if !ok {
		res(nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)})
		return
	}
	res(true, nil)
}

func udpRead(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 2 && len(params) != 3 {
		res(nil, []any{1, "Invalid number of parameters, expected (UDP connection ID, max bytes to read)"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidUDPUIntIDType)
		return
	}
	maxBytes, ok := msgpackrpc.ToUint(params[1])
	if !ok {
		res(nil, []any{1, "Invalid parameter type, expected uint for max bytes to read"})
		return
	}

	sockId := socketID{conn: rpc, id: id}
	lock.Lock()
	buffer, exists := udpReadBuffers[sockId]
	n := uint(len(buffer))
	if exists {
		// keep the remainder of the buffer for the next read
		if n > maxBytes {
			udpReadBuffers[sockId] = buffer[maxBytes:]
			n = maxBytes
		} else {
			delete(udpReadBuffers, sockId)
		}
	}
	lock.Unlock()

	res(buffer[:n], nil)
}

func udpClose(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 {
		res(nil, []any{1, "Invalid number of parameters, expected UDP connection ID"})
		return
	}
	id, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, errInvalidUDPUIntIDType)
		return
	}

	sockId := socketID{conn: rpc, id: id}
	lock.Lock()
	udpConn, existsConn := liveUdpConnections[sockId]
	delete(liveUdpConnections, sockId)
	delete(udpReadBuffers, sockId)
	lock.Unlock()

	if !existsConn {
		res(nil, []any{2, fmt.Sprintf("UDP connection not found for ID: %d", id)})
		return
	}

	// Close the connection if it exists
	// We do not return an error to the caller if the close operation fails, as it is not critical,
	// but we only log the error for debugging purposes.
	if err := udpConn.Close(); err != nil {
		res(err.Error(), nil)
		return
	}
	res("", nil)
}
