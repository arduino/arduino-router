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

package msgpackrpc

import (
	"context"
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
	type CustomError struct {
		Code    int
		Message string
	}

	var wg sync.WaitGroup
	notification := ""
	request := ""
	requestError := ""
	conn := NewConnection(
		in, out,
		func(ctx context.Context, logger FunctionLogger, method string, params []any) (_result any, _err any) {
			defer wg.Done()
			request = fmt.Sprintf("REQ method=%v params=%v", method, params)
			if method == "tocancel" {
				select {
				case <-ctx.Done():
					request += " canceled"
				case <-time.After(time.Second):
					request += " not canceled"
					t.Fail()
				}
				return nil, CustomError{Code: 1, Message: "error message"}
			}
			return []any{}, nil
		},
		func(logger FunctionLogger, method string, params []any) {
			defer wg.Done()
			notification = fmt.Sprintf("NOT method=%v params=%v", method, params)
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
	sendCancel := func(id MessageID) {
		send(messageTypeNotification, "$/cancelRequest", []any{id})
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

	{ // Test incoming request cancelation
		wg.Add(1)
		send(messageTypeRequest, MessageID(3), "tocancel", []any{})
		time.Sleep(time.Millisecond * 100)
		sendCancel(3)
		wg.Wait()
		require.Equal(t, "REQ method=tocancel params=[] canceled", request)
		msg, err := d.DecodeSlice()
		require.NoError(t, err)
		require.Equal(t, []any{int64(1), int64(3), map[string]any{"Code": int64(1), "Message": "error message"}, nil}, msg)
	}

	{ // Test outgoing request
		wg.Add(1)
		go func() {
			defer wg.Done()
			respRes, respErr, err := conn.SendRequest(t.Context(), "helloworld", []any{true})
			require.NoError(t, err)
			require.Nil(t, respErr)
			require.Equal(t, map[string]any{"fakedata": int8(99)}, respRes)
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

func TestRPCRougeDoubleCallWithSameID(t *testing.T) {
	in, testdataIn := nio.Pipe(buffer.New(1024))
	testdataOut, out := nio.Pipe(buffer.New(1024))

	enc := msgpack.NewEncoder(testdataIn)
	enc.UseCompactInts(true)
	send := func(msg ...any) {
		require.NoError(t, enc.Encode(msg))
	}

	d := msgpack.NewDecoder(testdataOut)
	d.UseLooseInterfaceDecoding(true)
	var reqLock sync.Mutex
	request := ""
	requestError := ""
	var wg sync.WaitGroup
	conn := NewConnection(
		in, out,
		func(ctx context.Context, logger FunctionLogger, method string, params []any) (_result any, _err any) {
			defer wg.Done()
			reqLock.Lock()
			request += fmt.Sprintf("REQ method=%v params=%v\n", method, params)
			reqLock.Unlock()
			time.Sleep(500 * time.Millisecond) // Simulate a long request
			return params[0], nil
		},
		nil,
		func(e error) {
			if e == io.EOF {
				return
			}
			reqLock.Lock()
			requestError = fmt.Sprintf("error=%s", e)
			reqLock.Unlock()
		},
	)
	go conn.Run()
	t.Cleanup(conn.Close)

	wg.Add(2)
	send(messageTypeRequest, MessageID(1), "test", []any{1})
	time.Sleep(100 * time.Millisecond)
	send(messageTypeRequest, MessageID(1), "test", []any{2})
	wg.Wait()
	require.Equal(t, "REQ method=test params=[1]\nREQ method=test params=[2]\n", request)
	require.Equal(t, "error=RPC protocol violation: request with ID 1 already active, canceling it", requestError)

	time.Sleep(100 * time.Millisecond)
	res, err := d.DecodeInterface()
	require.NoError(t, err)
	// Expect answer from the second request only
	require.Equal(t, []any{int64(1), int64(1), nil, int64(2)}, res)
}
