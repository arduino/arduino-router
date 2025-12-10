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

package msgpackrouter_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

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

	cl1 := msgpackrpc.NewConnection(ch1a, ch1a, func(ctx context.Context, logger msgpackrpc.FunctionLogger, method string, params []any) (_result any, _err any) {
		switch method {
		case "ping":
			return params, nil
		default:
			return nil, "unknown method: " + method
		}
	}, func(logger msgpackrpc.FunctionLogger, method string, params []any) {
		cl1NotificationsMux.Lock()
		fmt.Fprintf(cl1Notifications, "notification: %s %+v\n", method, params)
		cl1NotificationsMux.Unlock()
	}, func(err error) {
	})
	go cl1.Run()

	cl2 := msgpackrpc.NewConnection(ch2a, ch2a, func(ctx context.Context, logger msgpackrpc.FunctionLogger, method string, params []any) (result any, err any) {
		return nil, nil
	}, func(logger msgpackrpc.FunctionLogger, method string, params []any) {
	}, func(err error) {
	})
	go cl2.Run()

	router := msgpackrouter.New(0)
	router.Accept(ch1b)
	router.Accept(ch2b)

	{
		// Register a method on the first client
		result, reqErr, err := cl1.SendRequest(context.Background(), "$/register", []any{"ping"})
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		// Try to re-register the same method
		result, reqErr, err := cl1.SendRequest(context.Background(), "$/register", []any{"ping"})
		require.Nil(t, result)
		require.Equal(t, []any{int8(msgpackrouter.ErrCodeRouteAlreadyExists), "route already exists: ping"}, reqErr)
		require.NoError(t, err)
	}
	{
		// Register a method on the second client
		result, reqErr, err := cl2.SendRequest(context.Background(), "$/register", []any{"temperature"})
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		// Call from client2 the registered method on client1
		result, reqErr, err := cl2.SendRequest(context.Background(), "ping", []any{"1", 2, true})
		require.Equal(t, []any{"1", int8(2), true}, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		// Self-call from client1
		result, reqErr, err := cl1.SendRequest(context.Background(), "ping", []any{"c", 12, false})
		require.Equal(t, []any{"c", int8(12), false}, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}
	{
		// Call from client2 an un-registered method
		result, reqErr, err := cl2.SendRequest(context.Background(), "not-existent-method", []any{"1", 2, true})
		require.Nil(t, result)
		require.Equal(t, []any{int8(msgpackrouter.ErrCodeMethodNotAvailable), "method not-existent-method not available"}, reqErr)
		require.NoError(t, err)
	}
	{
		// Send notification to client1
		err := cl2.SendNotification("ping", []any{"a", int16(4), false})
		require.NoError(t, err)
	}
	{
		// Send notification to unregistered method
		err := cl2.SendNotification("notexistent", []any{"a", int16(4), false})
		require.NoError(t, err)
	}
	{
		// Self-send notification
		err := cl1.SendNotification("ping", []any{"b", int16(14), true, true})
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
	queueSize := 5
	msgLatency := 100 * time.Millisecond
	// Run a batch of 20 requests, and expect them to take more than 400 ms
	// in total because the router should throttle requests in batch of 5.
	batchSize := queueSize * 4
	expectedLatency := msgLatency * time.Duration(batchSize/queueSize)

	// Make a client that simulates a slow response
	ch1a, ch1b := newFullPipe()
	cl1 := msgpackrpc.NewConnection(ch1a, ch1a, func(ctx context.Context, logger msgpackrpc.FunctionLogger, method string, params []any) (_result any, _err any) {
		time.Sleep(msgLatency)
		return true, nil
	}, nil, nil)
	go cl1.Run()

	// Make a second client to send requests, without any delay
	ch2a, ch2b := newFullPipe()
	cl2 := msgpackrpc.NewConnection(ch2a, ch2a, nil, nil, nil)
	go cl2.Run()

	// Setup router
	router := msgpackrouter.New(queueSize) // max 5 pending messages per connection
	router.Accept(ch1b)
	router.Accept(ch2b)

	{
		// Register a method on the first client
		result, reqErr, err := cl1.SendRequest(context.Background(), "$/register", []any{"test"})
		require.Equal(t, true, result)
		require.Nil(t, reqErr)
		require.NoError(t, err)
	}

	// Run batch of requests from cl2 to cl1
	start := time.Now()
	var wg sync.WaitGroup
	for range batchSize {
		wg.Go(func() {
			_, _, err := cl2.SendRequest(t.Context(), "test", []any{})
			require.NoError(t, err)
		})
	}
	wg.Wait()
	elapsed := time.Since(start)

	// Check that the elapsed time is greater than expectedLatency
	fmt.Println("Elapsed time for requests:", elapsed)
	require.Greater(t, elapsed, expectedLatency, "Expected elapsed time to be greater than %s", expectedLatency)
}
