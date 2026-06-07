// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc

import (
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

	var wg sync.WaitGroup
	notification := ""
	request := ""
	requestError := ""
	conn := NewConnection(
		in, out,
		func(logger FunctionLogger, method string, params []any, res ResponseSender) {
			go func() {
				defer wg.Done()
				request = fmt.Sprintf("REQ method=%v params=%v", method, params)
				_ = res([]any{}, nil)
			}()
		},
		func(logger FunctionLogger, method string, params []any) {
			go func() {
				defer wg.Done()
				notification = fmt.Sprintf("NOT method=%v params=%v", method, params)
			}()
		},
		func(e error) {
			defer wg.Done()
			if e == io.EOF {
				return
			}
			requestError = fmt.Sprintf("error=%s", e)
		},
	)
	t.Cleanup(func() {
		wg.Add(1) // this will produce an error in the callback handler
		conn.Close()
	})
	go conn.Run()

	enc := msgpack.NewEncoder(testdataIn)
	enc.UseCompactInts(true)
	send := func(msg ...any) {
		require.NoError(t, enc.Encode(msg))
	}

	{ // Test incoming notification
		wg.Add(1)
		send(messageTypeNotification, "initialized", []any{123})
		wg.Wait()
		require.Equal(t, "NOT method=initialized params=[123]", notification)
	}

	{ // Test incoming request
		wg.Add(1)
		send(messageTypeRequest, MessageID(1), "textDocument/didOpen", []any{})
		wg.Wait()
		require.Equal(t, "REQ method=textDocument/didOpen params=[]", request)
		msg, err := d.DecodeSlice()
		require.NoError(t, err)
		require.Equal(t, []any{int64(1), int64(1), nil, []any{}}, msg)
	}

	{ // Test another incoming request
		wg.Add(1)
		send(messageTypeRequest, MessageID(2), "textDocument/didClose", []any{})
		wg.Wait()
		require.Equal(t, "REQ method=textDocument/didClose params=[]", request)
		msg, err := d.DecodeSlice()
		require.NoError(t, err)
		require.Equal(t, []any{int64(1), int64(2), nil, []any{}}, msg)
	}

	{ // Test outgoing request
		wg.Add(1)
		go func() {
			defer wg.Done()
			respRes, respErr, err := conn.SendRequest(t.Context(), "helloworld", true)
			require.NoError(t, err)
			require.Nil(t, respErr)
			require.Equal(t, OrderedMap{{Key: "fakedata", Value: int8(99)}}, respRes)
		}()
		msg, err := d.DecodeSlice() // Grab the SendRequest
		require.NoError(t, err)
		require.Equal(t, []any{int64(0), int64(1), "helloworld", []any{true}}, msg)
		send(messageTypeResponse, 1, nil, map[string]any{"fakedata": 99})
		wg.Wait()
	}

	{ // Test invalid response
		wg.Add(1)
		send(1, 999, 10, nil)
		wg.Wait()
		require.Equal(t, "error=invalid ID in request response '999': double answer or request not sent", requestError)
	}
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

func TestRPCMapParameterOrdering(t *testing.T) {
	in, testdataIn := nio.Pipe(buffer.New(4096))
	_, out := nio.Pipe(buffer.New(4096))

	var (
		wg             sync.WaitGroup
		receivedParams []any
	)

	conn := NewConnection(
		in, out,
		func(logger FunctionLogger, method string, params []any, res ResponseSender) {
			defer wg.Done()
			receivedParams = params
			_ = res(nil, nil)
		},
		func(logger FunctionLogger, method string, params []any) {
			defer wg.Done()
			receivedParams = params
		},
		nil,
	)
	t.Cleanup(conn.Close)
	go conn.Run()

	enc := msgpack.NewEncoder(testdataIn)
	enc.UseCompactInts(true)
	send := func(msg ...any) {
		require.NoError(t, enc.Encode(msg))
	}

	orderedKeys := OrderedMap{
		{Key: "zebra", Value: 0},
		{Key: "apple", Value: 1},
		{Key: "mango", Value: 2},
		{Key: "banana", Value: 3},
		{Key: "cherry", Value: 4},
		{Key: "kiwi", Value: 5},
		{Key: "fig", Value: 6},
		{Key: "date", Value: 7},
	}
	expectedKeys := make([]string, 0, len(orderedKeys))
	for _, kv := range orderedKeys {
		expectedKeys = append(expectedKeys, kv.Key)
	}

	assertReceivedParamsOrdered := func(t *testing.T) {
		t.Helper()
		require.Len(t, receivedParams, 1)
		var actualKeys []string
		switch m := receivedParams[0].(type) {
		case OrderedMap:
			for _, kv := range m {
				actualKeys = append(actualKeys, kv.Key)
			}
		case map[string]any:
			for k := range m {
				actualKeys = append(actualKeys, k)
			}
		default:
			t.Fatalf("unexpected params type %T", receivedParams[0])
		}
		require.Equal(t, expectedKeys, actualKeys)
	}

	t.Run("incoming notification preserves key order", func(t *testing.T) {
		wg.Add(1)
		send(messageTypeNotification, "notify/test", []any{&orderedKeys})
		wg.Wait()
		assertReceivedParamsOrdered(t)
	})

	t.Run("incoming request preserves key order", func(t *testing.T) {
		wg.Add(1)
		send(messageTypeRequest, MessageID(42), "request/test", []any{&orderedKeys})
		wg.Wait()
		assertReceivedParamsOrdered(t)
	})
}
