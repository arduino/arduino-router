package msgpackrouter_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/arduino/router/msgpackrouter"
	"github.com/arduino/router/msgpackrpc"

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

	router := msgpackrouter.New()
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
