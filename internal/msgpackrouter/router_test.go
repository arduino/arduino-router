// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrouter_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/arduino/arduino-router/internal/msgpackrouter"
	"github.com/arduino/arduino-router/msgpackrpc"

	"github.com/djherbis/buffer"
	"github.com/djherbis/nio/v3"
	"github.com/stretchr/testify/require"
)

type FullPipe struct {
	in  *nio.PipeReader
	out *nio.PipeWriter
}

func (p *FullPipe) Read(b []byte) (int, error) {
	return p.in.Read(b)
}

func (p *FullPipe) Write(b []byte) (int, error) {
	return p.out.Write(b)
}

func (p *FullPipe) Close() error {
	err1 := p.out.Close()
	err2 := p.in.Close()
	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}
	return nil
}

func newFullPipe() (io.ReadWriteCloser, io.ReadWriteCloser) {
	in1, out1 := nio.Pipe(buffer.New(1024))
	in2, out2 := nio.Pipe(buffer.New(1024))
	return &FullPipe{in1, out2}, &FullPipe{in2, out1}
}

func TestBasicRouterFunctionality(t *testing.T) {
	ch1a, ch1b := newFullPipe()
	ch2a, ch2b := newFullPipe()

	cl1NotificationsMux := sync.Mutex{}
	cl1Notifications := bytes.NewBuffer(nil)

	cl1 := msgpackrpc.NewConnection(ch1a, ch1a, func(logger msgpackrpc.FunctionLogger, method string, params []any, res msgpackrpc.ResponseSender) {
		switch method {
		case "ping":
			_ = res(params, nil)
		default:
			_ = res(nil, "unknown method: "+method)
		}
	}, func(logger msgpackrpc.FunctionLogger, method string, params []any) {
		cl1NotificationsMux.Lock()
		fmt.Fprintf(cl1Notifications, "notification: %s %+v\n", method, params)
		cl1NotificationsMux.Unlock()
	}, func(err error) {
	})
	go cl1.Run()

	cl2 := msgpackrpc.NewConnection(ch2a, ch2a, func(logger msgpackrpc.FunctionLogger, method string, params []any, res msgpackrpc.ResponseSender) {
		_ = res(nil, nil)
	}, func(logger msgpackrpc.FunctionLogger, method string, params []any) {
	}, func(err error) {
	})
	go cl2.Run()

	router := msgpackrouter.New()
	router.Accept(ch1b)
	router.Accept(ch2b)

	{
		// Register a method on the first client
		result, reqErr, err := cl1.SendRequest(context.Background(), "$/register", "ping")
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		// Try to re-register the same method
		result, reqErr, err := cl1.SendRequest(context.Background(), "$/register", "ping")
		require.Nil(t, result)
		require.Equal(t, []any{int8(msgpackrouter.ErrCodeRouteAlreadyExists), "route already exists: ping"}, reqErr)
		require.NoError(t, err)
	}
	{
		// Register a method on the second client
		result, reqErr, err := cl2.SendRequest(context.Background(), "$/register", "temperature")
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		// Call from client2 the registered method on client1
		result, reqErr, err := cl2.SendRequest(context.Background(), "ping", "1", 2, true)
		require.Equal(t, []any{"1", int8(2), true}, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		// Self-call from client1
		result, reqErr, err := cl1.SendRequest(context.Background(), "ping", "c", 12, false)
		require.Equal(t, []any{"c", int8(12), false}, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		// Call from client2 an un-registered method
		result, reqErr, err := cl2.SendRequest(context.Background(), "not-existent-method", "1", 2, true)
		require.Nil(t, result)
		require.Equal(t, []any{int8(msgpackrouter.ErrCodeMethodNotAvailable), "method not-existent-method not available"}, reqErr)
		require.NoError(t, err)
	}
	{
		// Send notification to client1
		err := cl2.SendNotification("ping", "a", int16(4), false)
		require.NoError(t, err)
	}
	{
		// Send notification to unregistered method
		err := cl2.SendNotification("notexistent", "a", int16(4), false)
		require.NoError(t, err)
	}
	{
		// Self-send notification
		err := cl1.SendNotification("ping", "b", int16(14), true, true)
		require.NoError(t, err)
	}
	time.Sleep(100 * time.Millisecond) // Give some time for the notifications to be processed

	cl1NotificationsMux.Lock()
	require.Contains(t, cl1Notifications.String(), "notification: ping [a 4 false]")
	require.Contains(t, cl1Notifications.String(), "notification: ping [b 14 true true]")
	cl1NotificationsMux.Unlock()
}

func TestMessageForwarderCongestionControl(t *testing.T) {
	// Test parameters
	msgLatency := 100 * time.Millisecond
	// Run a batch of 20 requests, and expect them to take more than 400 ms
	// in total because the router should throttle requests in batch of 5.
	batchSize := 4
	expectedLatency := msgLatency * time.Duration(batchSize)

	// Make a client that simulates a slow response
	ch1a, ch1b := newFullPipe()
	cl1 := msgpackrpc.NewConnection(ch1a, ch1a, func(logger msgpackrpc.FunctionLogger, method string, params []any, res msgpackrpc.ResponseSender) {
		time.Sleep(msgLatency)
		_ = res(true, nil)
	}, nil, nil)
	go cl1.Run()

	// Make a second client to send requests, without any delay
	ch2a, ch2b := newFullPipe()
	cl2 := msgpackrpc.NewConnection(ch2a, ch2a, nil, nil, nil)
	go cl2.Run()

	// Setup router
	router := msgpackrouter.New() // max 5 pending messages per connection
	router.Accept(ch1b)
	router.Accept(ch2b)

	{
		// Register a method on the first client
		result, reqErr, err := cl1.SendRequest(context.Background(), "$/register", "test")
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}

	// Run batch of requests from cl2 to cl1
	start := time.Now()
	var wg sync.WaitGroup
	for range batchSize {
		wg.Go(func() {
			_, _, err := cl2.SendRequest(t.Context(), "test")
			require.NoError(t, err)
		})
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Check that the elapsed time is greater than expectedLatency
	fmt.Println("Elapsed time for requests:", elapsed)
	require.Greater(t, elapsed, expectedLatency, "Expected elapsed time to be greater than %s", expectedLatency)
}

func TestRouterMaxMsgSize(t *testing.T) {
	ch1a, ch1b := newFullPipe()
	cl1 := msgpackrpc.NewConnection(ch1a, ch1a, func(logger msgpackrpc.FunctionLogger, method string, params []any, res msgpackrpc.ResponseSender) {
		if method == "n" {
			_ = res(true, nil)
		} else {
			_ = res(make([]byte, 160), nil) // return a response that exceeds the limit
		}
	}, func(logger msgpackrpc.FunctionLogger, method string, params []any) {
		fmt.Println("Received notification", "method", method, "params", params)
		arg := params[0].(string)
		require.LessOrEqual(t, len(arg), 128)
	}, nil)
	go cl1.Run()

	ch2a, ch2b := newFullPipe()
	cl2 := msgpackrpc.NewConnection(ch2a, ch2a, nil, nil, nil)
	go cl2.Run()

	router := msgpackrouter.New()
	router.Accept(ch1b)
	router.Accept(ch2b)

	// Register a method on the first client
	{
		result, reqErr, err := cl1.SendRequest(t.Context(), "$/register", "n")
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		result, reqErr, err := cl1.SendRequest(t.Context(), "$/register", "y")
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}

	// Set a max message size
	{
		result, reqErr, err := cl1.SendRequest(context.Background(), "$/setMaxMsgSize", 20)
		require.Nil(t, result)
		require.Equal(t, []any{int8(1), "invalid params: max buffer size must be greater than 127"}, reqErr)
		require.NoError(t, err)
	}
	{
		result, reqErr, err := cl1.SendRequest(context.Background(), "$/setMaxMsgSize", 128)
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}

	// Try to send a notification that exceeds the limit
	{
		result, reqErr, err := cl2.SendRequest(context.Background(), "n", string(make([]byte, 160)))
		require.Nil(t, result)
		require.Equal(t, []any{int8(6), "message size exceeds the limit"}, reqErr)
		require.NoError(t, err)
	}
	{
		require.NoError(t, cl2.SendNotification("n", string("ciao!")))
		require.NoError(t, cl2.SendNotification("n", string(make([]byte, 160))))
	}

	{
		result, reqErr, err := cl2.SendRequest(context.Background(), "$/setMaxMsgSize", 128)
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		result, reqErr, err := cl2.SendRequest(context.Background(), "y")
		require.Nil(t, result)
		require.Equal(t, []any{int8(6), "message size exceeds the limit"}, reqErr)
		require.NoError(t, err)
	}
}
