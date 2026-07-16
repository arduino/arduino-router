// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/djherbis/buffer"
	"github.com/djherbis/nio/v3"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func TestRPCConnection(t *testing.T) {
	in, testdataIn := nio.Pipe(buffer.New(1024))
	testdataOut, out := nio.Pipe(buffer.New(1024))
	d := msgpack.NewDecoder(testdataOut)
	d.UseLooseInterfaceDecoding(true)

	expNotifications := []string{
		"NOT method=initialized params=[123]",
	}
	expRequests := []string{
		"REQ method=textDocument/didOpen params=[]",
		"REQ method=textDocument/didClose params=[]",
	}
	expErrors := []string{
		"error=invalid ID in request response '999': double answer or request not sent",
	}

	var recvLock sync.Mutex
	recvNotifications := []string{}
	recvRequests := []string{}
	recvErrors := []string{}
	conn := NewConnection(
		in, out,
		func(logger FunctionLogger, method string, params []any, res ResponseSender) {
			recvLock.Lock()
			defer recvLock.Unlock()
			recvRequests = append(recvRequests, fmt.Sprintf("REQ method=%v params=%v", method, params))
			_ = res([]any{}, nil)
		},
		func(logger FunctionLogger, method string, params []any) {
			recvLock.Lock()
			defer recvLock.Unlock()
			recvNotifications = append(recvNotifications, fmt.Sprintf("NOT method=%v params=%v", method, params))
		},
		func(e error) {
			recvLock.Lock()
			defer recvLock.Unlock()
			if errors.Is(e, io.EOF) || errors.Is(e, io.ErrClosedPipe) {
				return
			}
			recvErrors = append(recvErrors, fmt.Sprintf("error=%v", e))
		},
	)
	t.Cleanup(conn.Close)
	go conn.Run()

	enc := msgpack.NewEncoder(testdataIn)
	enc.UseCompactInts(true)
	send := func(msg ...any) {
		require.NoError(t, enc.Encode(msg))
	}

	{ // Test incoming notification
		send(messageTypeNotification, "initialized", []any{123})
	}

	{ // Test incoming request
		send(messageTypeRequest, MessageID(1), "textDocument/didOpen", []any{})
		msg, err := d.DecodeSlice()
		require.NoError(t, err)
		require.Equal(t, []any{int64(1), int64(1), nil, []any{}}, msg)
	}

	{ // Test another incoming request
		send(messageTypeRequest, MessageID(2), "textDocument/didClose", []any{})
		msg, err := d.DecodeSlice()
		require.NoError(t, err)
		require.Equal(t, []any{int64(1), int64(2), nil, []any{}}, msg)
	}

	{ // Test outgoing request
		var wg sync.WaitGroup
		wg.Go(func() {
			respRes, respErr, err := conn.SendRequest(t.Context(), "helloworld", true)
			require.NoError(t, err)
			require.Nil(t, respErr)
			require.Equal(t, map[string]any{"fakedata": int8(99)}, respRes)
		})
		msg, err := d.DecodeSlice() // Grab the SendRequest
		require.NoError(t, err)
		require.Equal(t, []any{int64(0), int64(1), "helloworld", []any{true}}, msg)
		send(messageTypeResponse, 1, nil, map[string]any{"fakedata": 99})
		wg.Wait()
	}

	{ // Test invalid response
		send(1, 999, 10, nil)
	}

	// Let a bit of time pass to allow the connection to process all messages
	time.Sleep(100 * time.Millisecond)

	// Check that all expected notifications, requests, and errors have been handled
	recvLock.Lock()
	defer recvLock.Unlock()
	require.Equal(t, expNotifications, recvNotifications)
	require.Equal(t, expRequests, recvRequests)
	require.Equal(t, expErrors, recvErrors)
}

func TestRPCMessageMaxSize(t *testing.T) {
	in, testdataIn := nio.Pipe(buffer.New(1024))
	testdataOut, out := nio.Pipe(buffer.New(1024))

	enc := msgpack.NewEncoder(testdataIn)
	enc.UseCompactInts(true)
	send := func(msg ...any) {
		require.NoError(t, enc.Encode(msg))
	}

	d := msgpack.NewDecoder(testdataOut)
	d.UseLooseInterfaceDecoding(true)

	conn := NewConnection(
		in, out,
		func(logger FunctionLogger, method string, params []any, res ResponseSender) {
			// Return a big response
			require.Error(t, res("123456789012345678901234567890", nil))
			require.NoError(t, res("123", nil))
		},
		func(logger FunctionLogger, method string, params []any) {
			// Should receive only small notifications
			require.Equal(t, "hi", params[0].(string))
		},
		func(e error) {
			// ignore "can't read packet: io: read/write on closed pipe"
			if errors.Is(e, io.ErrClosedPipe) {
				return
			}
			if errors.Is(e, io.EOF) {
				return
			}
			require.FailNow(t, "error handler should not be called")
		},
	)

	// Set a very small max message size to trigger the error
	conn.SetMaxOutgoingMessageSize(16)

	// Start the connection loop
	go conn.Run()
	t.Cleanup(conn.Close)

	// Call a method that returns a response exceeding the limit
	send(messageTypeRequest, MessageID(1), "test", []any{})
	var resp any
	require.NoError(t, d.Decode(&resp))
	require.Equal(t, []any{int64(1), int64(1), nil, "123"}, resp) // Check that the custom error is returned

	// Call a method with parameters exceeding the limit
	res, reqErr, err := conn.SendRequest(t.Context(), "test", "123456789") // This message should exceed the limit
	require.Nil(t, res)
	require.Nil(t, reqErr)
	require.ErrorIs(t, err, &ErrBufferLimitExceeded{})

	// Send a notification with parameters exceeding the limit
	err = conn.SendNotification("test", "hi") // This message should pass
	require.NoError(t, err)
	err = conn.SendNotification("test", "123456789") // This message should exceed the limit
	require.ErrorIs(t, err, &ErrBufferLimitExceeded{})
	time.Sleep(500 * time.Millisecond)
}

func expectReadHex(t *testing.T, r io.Reader, hexStr string) {
	expected, err := hex.DecodeString(hexStr)
	require.NoError(t, err)
	resp := make([]byte, len(expected)*2)
	n, err := r.Read(resp)
	require.NoError(t, err)
	require.Equal(t, expected, resp[:n])
}

func TestFixIntHandling(t *testing.T) {
	in, _ := nio.Pipe(buffer.New(1024))
	testdataOut, out := nio.Pipe(buffer.New(1024))

	conn := NewConnection(in, out, nil, nil, nil)
	go conn.Run()
	t.Cleanup(conn.Close)

	{
		// Send a notification and check that the integer is
		// encoded using the smallest integer representation
		require.NoError(t, conn.SendNotification("a", 10))
		expectReadHex(t, testdataOut, "9302a161910a") // [2, "a", [10]]
		require.NoError(t, conn.SendNotification("a", int8(10)))
		expectReadHex(t, testdataOut, "9302a161910a") // [2, "a", [10]]
		require.NoError(t, conn.SendNotification("a", int16(10)))
		expectReadHex(t, testdataOut, "9302a161910a") // [2, "a", [10]]
		require.NoError(t, conn.SendNotification("a", int32(10)))
		expectReadHex(t, testdataOut, "9302a161910a") // [2, "a", [10]]
		require.NoError(t, conn.SendNotification("a", int64(10)))
		expectReadHex(t, testdataOut, "9302a161910a") // [2, "a", [10]]

		require.NoError(t, conn.SendNotification("b", uint8(10)))
		expectReadHex(t, testdataOut, "9302a162910a") // [2, "b", [10]]
		require.NoError(t, conn.SendNotification("b", uint16(10)))
		expectReadHex(t, testdataOut, "9302a162910a") // [2, "b", [10]]
		require.NoError(t, conn.SendNotification("b", uint32(10)))
		expectReadHex(t, testdataOut, "9302a162910a") // [2, "b", [10]]
		require.NoError(t, conn.SendNotification("b", uint64(10)))
		expectReadHex(t, testdataOut, "9302a162910a") // [2, "b", [10]]
	}
}
func TestRPCConnectionRecover(t *testing.T) {
	in, testdataIn := nio.Pipe(buffer.New(1024))
	testdataOut, out := nio.Pipe(buffer.New(1024))

	enc := msgpack.NewEncoder(testdataIn)
	enc.UseCompactInts(true)
	send := func(msg ...any) {
		require.NoError(t, enc.Encode(msg))
	}

	d := msgpack.NewDecoder(testdataOut)
	d.UseLooseInterfaceDecoding(true)

	conn := NewConnection(
		in, out,
		func(logger FunctionLogger, method string, params []any, res ResponseSender) {
			// Return a big response
			require.NoError(t, res("123", nil))
		},
		func(logger FunctionLogger, method string, params []any) {
			// Should receive only small notifications
			require.Equal(t, "hi", params[0].(string))
		},
		func(e error) {
			fmt.Println("ERROR HANDLER CALLED:", e)
		},
	)

	// Start the connection loop
	go conn.Run()
	t.Cleanup(conn.Close)

	// Send a valid request and check the response
	send(messageTypeRequest, MessageID(1), "test", []any{})
	var resp any
	require.NoError(t, d.Decode(&resp))
	require.Equal(t, []any{int64(1), int64(1), nil, "123"}, resp)

	// Send an invalid message to trigger an error
	send(1, 2, 3) // Invalid message format

	// Send another valid request and check the response
	send(messageTypeRequest, MessageID(2), "test", []any{})
	var resp2 any
	require.NoError(t, d.Decode(&resp2))
	require.Equal(t, []any{int64(1), int64(2), nil, "123"}, resp2)

	// Send a truncated message followed by a sequence to drain the incomplete message
	call := []byte{0x94, 0x00, 0x03, 0xA4, 0x74, 0x65, 0x73, 0x74, 0x90} // Encoding of [0, 3, "test", []]
	_, err := testdataIn.Write(call[:5])                                 // Send incomplete RPC message
	require.NoError(t, err)
	_, err = testdataIn.Write([]byte{0xC0, 0xC0, 0xC0, 0xC0, 0xC0, 0xC0}) // Send a sequence of NIL to drain the incomplete message
	require.NoError(t, err)

	// Send another valid request and check the response
	send(messageTypeRequest, MessageID(3), "test", []any{})
	var resp3 any
	require.NoError(t, d.Decode(&resp3))
	require.Equal(t, []any{int64(1), int64(3), nil, "123"}, resp3)
}
