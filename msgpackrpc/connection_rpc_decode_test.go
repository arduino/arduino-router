// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func encodeStream(t *testing.T, values ...any) []byte {
	t.Helper()

	var b bytes.Buffer
	enc := msgpack.NewEncoder(&b)
	enc.UseCompactInts(true)
	for _, v := range values {
		require.NoError(t, enc.Encode(v))
	}
	return b.Bytes()
}

func decodePacketFromBytes(c *Connection, data []byte) (rpcPacket, error) {
	d := msgpack.NewDecoder(bytes.NewReader(data))
	return c.decodeRpcPacket(d)
}

func TestDecodeRpcPacketValid(t *testing.T) {
	t.Run("request non-raw", func(t *testing.T) {
		c := &Connection{}
		packet, err := decodePacketFromBytes(c, encodeStream(t, []any{messageTypeRequest, MessageID(7), "sum", []any{1, "x"}}))
		require.NoError(t, err)
		require.Equal(t, messageTypeRequest, packet.msgType)
		require.Equal(t, MessageID(7), packet.msgID)
		require.Equal(t, "sum", packet.method)
		require.Len(t, packet.params, 2)
		require.EqualValues(t, 1, packet.params[0])
		require.Equal(t, "x", packet.params[1])
	})

	t.Run("notification non-raw", func(t *testing.T) {
		c := &Connection{}
		packet, err := decodePacketFromBytes(c, encodeStream(t, []any{messageTypeNotification, "initialized", []any{true}}))
		require.NoError(t, err)
		require.Equal(t, messageTypeNotification, packet.msgType)
		require.Equal(t, "initialized", packet.method)
		require.Len(t, packet.params, 1)
		require.Equal(t, true, packet.params[0])
	})

	t.Run("response non-raw", func(t *testing.T) {
		c := &Connection{}
		packet, err := decodePacketFromBytes(c, encodeStream(t, []any{messageTypeResponse, MessageID(3), nil, map[string]any{"ok": true}}))
		require.NoError(t, err)
		require.Equal(t, messageTypeResponse, packet.msgType)
		require.Equal(t, MessageID(3), packet.msgID)
		require.Nil(t, packet.reqError)
		require.Equal(t, map[string]any{"ok": true}, packet.reqResult)
	})

	t.Run("request and response raw", func(t *testing.T) {
		c := &Connection{}
		c.SetKeepParamsAndResponsesAsRaw(true)

		requestPacket, err := decodePacketFromBytes(c, encodeStream(t, []any{messageTypeRequest, MessageID(1), "raw", []any{42}}))
		require.NoError(t, err)
		require.Len(t, requestPacket.params, 1)
		require.IsType(t, msgpack.RawMessage{}, requestPacket.params[0])

		responsePacket, err := decodePacketFromBytes(c, encodeStream(t, []any{messageTypeResponse, MessageID(9), "e", []any{"r"}}))
		require.NoError(t, err)
		require.IsType(t, msgpack.RawMessage{}, responsePacket.reqError)
		require.IsType(t, msgpack.RawMessage{}, responsePacket.reqResult)
	})
}

func TestDecodeRpcPacketInvalid(t *testing.T) {
	tests := []struct {
		name        string
		data        []byte
		keepAsRaw   bool
		errContains string
	}{
		{
			name:        "top-level is not array",
			data:        encodeStream(t, 123, []any{messageTypeNotification, "ok", []any{}}),
			errContains: "invalid packet, expected array",
		},
		{
			name:        "array length too small",
			data:        encodeStream(t, []any{messageTypeRequest, 1}),
			errContains: "expected array with 3 or 4 elements",
		},
		{
			name:        "array length too large",
			data:        encodeStream(t, []any{messageTypeRequest, 1, "m", []any{}, "extra"}),
			errContains: "expected array with 3 or 4 elements",
		},
		{
			name:        "message type is not int",
			data:        encodeStream(t, []any{"x", 1, "m", []any{}}),
			errContains: "expected int for message type",
		},
		{
			name:        "request with wrong number of fields",
			data:        encodeStream(t, []any{messageTypeRequest, 1, "m"}),
			errContains: "expected array with 4 elements for request or response",
		},
		{
			name:        "notification with wrong number of fields",
			data:        encodeStream(t, []any{messageTypeNotification, "m", []any{}, "x"}),
			errContains: "expected array with 3 elements for notification",
		},
		{
			name:        "invalid message type",
			data:        encodeStream(t, []any{77, "m", []any{}}),
			errContains: "expected request, response or notification",
		},
		{
			name:        "request id is not uint",
			data:        encodeStream(t, []any{messageTypeRequest, "id", "m", []any{}}),
			errContains: "expected uint for message ID",
		},
		{
			name:        "method is not string",
			data:        encodeStream(t, []any{messageTypeRequest, 1, 2, []any{}}),
			errContains: "expected string for method name",
		},
		{
			name:        "params is not array",
			data:        encodeStream(t, []any{messageTypeRequest, 1, "m", "bad"}),
			errContains: "expected array for params",
		},
		{
			name:        "params is nil",
			data:        encodeStream(t, []any{messageTypeRequest, 1, "m", nil}),
			errContains: "expected array, got Nil",
		},
		{
			name:        "response decode error on first field non-raw",
			data:        []byte{0x94, 0x01, 0x01, 0xc1, 0xc0},
			errContains: "error decoding response",
		},
		{
			name:        "response decode error on second field non-raw",
			data:        []byte{0x94, 0x01, 0x01, 0xc0, 0xc1},
			errContains: "error decoding response",
		},
		{
			name:        "response decode error on first field raw",
			data:        []byte{0x94, 0x01, 0x01, 0xc1, 0xc0},
			keepAsRaw:   true,
			errContains: "error decoding response",
		},
		{
			name:        "response decode error on second field raw",
			data:        []byte{0x94, 0x01, 0x01, 0xc0, 0xc1},
			keepAsRaw:   true,
			errContains: "error decoding response",
		},
		{
			name:        "param decode error",
			data:        []byte{0x94, 0x00, 0x01, 0xa1, 0x6d, 0x91, 0xc1},
			errContains: "error decoding param 0",
		},
		{
			name:        "param decode error raw",
			data:        []byte{0x94, 0x00, 0x01, 0xa1, 0x6d, 0x91, 0xc1},
			keepAsRaw:   true,
			errContains: "error decoding param 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Connection{}
			c.SetKeepParamsAndResponsesAsRaw(tc.keepAsRaw)
			_, err := decodePacketFromBytes(c, tc.data)
			require.Error(t, err)
			require.ErrorContains(t, err, tc.errContains)
		})
	}
}

func TestDecodeRpcPacketSkipAndContinue(t *testing.T) {
	stream := encodeStream(
		t,
		[]any{messageTypeRequest, 1, "m", []any{}, "extra"},
		[]any{messageTypeNotification, "ready", []any{123}},
	)

	c := &Connection{}
	d := msgpack.NewDecoder(bytes.NewReader(stream))

	_, err := c.decodeRpcPacket(d)
	require.Error(t, err)
	require.ErrorContains(t, err, "expected array with 3 or 4 elements")

	packet, err := c.decodeRpcPacket(d)
	require.NoError(t, err)
	require.Equal(t, messageTypeNotification, packet.msgType)
	require.Equal(t, "ready", packet.method)
	require.Len(t, packet.params, 1)
	require.EqualValues(t, 123, packet.params[0])
}

func TestDecodeRpcPacketSkipFailure(t *testing.T) {
	// fixarray(3), int(0), then EOF. The parser errors and cannot skip remaining elements.
	data := []byte{0x93, 0x00}

	c := &Connection{}
	_, err := decodePacketFromBytes(c, data)
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to skip invalid packet")

	require.True(t, errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF))
}
