// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package serialdetect

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/arduino/arduino-router/internal/noisyline"
	"github.com/arduino/arduino-router/internal/seriallink"
	"github.com/arduino/arduino-router/msgpackrpc"
)

func encode(t *testing.T, v ...any) []byte {
	var b bytes.Buffer
	enc := msgpack.NewEncoder(&b)
	enc.UseCompactInts(true)
	for _, x := range v {
		require.NoError(t, enc.Encode(x))
	}
	return b.Bytes()
}

// What a raw MCU (Arduino_RouterBridge) sends at boot.
func rawBoot(t *testing.T) []byte {
	return append([]byte("MCU starting RPC Bridge communication"), rawReset(t)...)
}

func rawReset(t *testing.T) []byte {
	return encode(t, []any{0, 0, "$/reset", []any{}})
}

// The SYN frame a reliable MCU sends at boot (from the C++ implementation tests).
var synFrame = []byte{0x02, 0x14, 0x01, 0x01, 0x01, 0x0B, 0x01, 0x08, 0xEF, 0xBE, 0xAD, 0xDE, 0xC2, 0x21, 0x9A, 0xB5, 0x00}

func detect(t *testing.T, r io.Reader) (Protocol, []byte) {
	p, buf, err := Detect(r)
	require.NoError(t, err)
	return p, buf
}

func TestDetectRaw(t *testing.T) {
	data := rawBoot(t)
	p, buf := detect(t, bytes.NewReader(data))
	require.Equal(t, Raw, p)
	require.Equal(t, rawReset(t), buf, "the data must be returned from the first message")

	// Byte by byte
	p, buf = detect(t, iotest.OneByteReader(bytes.NewReader(data)))
	require.Equal(t, Raw, p)
	require.Equal(t, rawReset(t), buf)

	// Notifications and responses are recognized too
	p, _ = detect(t, bytes.NewReader(encode(t, []any{2, "mon/write", []any{"hello"}})))
	require.Equal(t, Raw, p)
	p, _ = detect(t, bytes.NewReader(encode(t, []any{1, 5, nil, true})))
	require.Equal(t, Raw, p)
}

func TestDetectReliable(t *testing.T) {
	// The SYN frame, preceded by garbage (boot logs, a truncated frame)
	data := append([]byte("boot log\r\n\x05\x11\x22"), 0)
	data = append(data, synFrame...)
	p, buf := detect(t, bytes.NewReader(data))
	require.Equal(t, Reliable, p)
	require.Equal(t, data, buf)

	p, buf = detect(t, iotest.OneByteReader(bytes.NewReader(data)))
	require.Equal(t, Reliable, p)
	require.Equal(t, data, buf)
}

func TestDetectReliableFromGoLink(t *testing.T) {
	// Whatever a real Link sends first is detected
	line := noisyline.New(1)
	l, err := seriallink.New(line.OpenB(), seriallink.Config{})
	require.NoError(t, err)
	defer l.Close()
	p, _ := detect(t, line.OpenA())
	require.Equal(t, Reliable, p)
}

func TestDetectRejectsCorruptedFrames(t *testing.T) {
	// A frame with a bad CRC is not enough, the next good one is
	bad := bytes.Clone(synFrame)
	bad[9] ^= 0x01
	data := append(bad, synFrame...)
	p, buf := detect(t, iotest.OneByteReader(bytes.NewReader(data)))
	require.Equal(t, Reliable, p)
	require.Equal(t, data, buf)

	// Only garbage: no decision
	_, _, err := Detect(bytes.NewReader(bad))
	require.ErrorIs(t, err, io.EOF)
}

func TestDetectSkipsGarbage(t *testing.T) {
	// Garbage looking like the beginning of a msgpack-rpc message, with a huge
	// string length, must not stall the detection
	garbage := []byte{0x94, 0x00, 0x01, 0xDB, 0xFF, 0xFF, 0xFF, 0xFF}
	garbage = append(garbage, bytes.Repeat([]byte{'x'}, 2*maxIncompleteMessage)...)
	data := append(garbage, rawBoot(t)...)
	p, buf := detect(t, bytes.NewReader(data))
	require.Equal(t, Raw, p)
	require.Equal(t, rawReset(t), buf)

	// Malformed messages are not accepted
	for _, msg := range [][]byte{
		encode(t, []any{0, 1, "", []any{}}),            // empty method
		encode(t, []any{0, 1, "bad\x01name", []any{}}), // non printable method
		encode(t, []any{0, 1, "method", 5}),            // params not an array
		encode(t, []any{3, 1, "method", []any{}}),      // bad type
		encode(t, []any{2, "method", []any{}, 1}),      // bad size
	} {
		_, _, err := Detect(bytes.NewReader(msg))
		require.ErrorIs(t, err, io.EOF, "% X", msg)
	}
}

func TestDetectLimitsMemory(t *testing.T) {
	// Lots of undecided garbage, then a message
	garbage := bytes.Repeat([]byte("garbage "), 3*maxBuffered/8)
	data := append(garbage, rawBoot(t)...)
	p, buf := detect(t, bytes.NewReader(data))
	require.Equal(t, Raw, p)
	require.Equal(t, rawReset(t), buf)
}

func newReliable(port io.ReadWriteCloser) (io.ReadWriteCloser, error) {
	return seriallink.New(port, seriallink.Config{})
}

func TestAutoStreamReliable(t *testing.T) {
	line := noisyline.New(1)
	peer, err := seriallink.New(line.OpenB(), seriallink.Config{})
	require.NoError(t, err)
	defer peer.Close()

	detected := make(chan Protocol, 1)
	s := NewAutoStream(line.OpenA(), newReliable, func(p Protocol) { detected <- p })
	defer s.Close()

	go func() { _, _ = peer.Write([]byte("hello from the MCU")) }()
	got := make([]byte, 18)
	_, err = io.ReadFull(s, got)
	require.NoError(t, err)
	require.Equal(t, "hello from the MCU", string(got))
	require.Equal(t, Reliable, <-detected)

	_, err = s.Write([]byte("hello from the router"))
	require.NoError(t, err)
	got = make([]byte, 21)
	_, err = io.ReadFull(peer, got)
	require.NoError(t, err)
	require.Equal(t, "hello from the router", string(got))
}

func TestAutoStreamRaw(t *testing.T) {
	// A raw MCU: its first request must not be lost
	line := noisyline.New(1)
	mcuPort := line.OpenB()
	_, err := mcuPort.Write([]byte("MCU starting RPC Bridge communication"))
	require.NoError(t, err)
	mcu := msgpackrpc.NewConnection(mcuPort, mcuPort, nil, nil, nil)
	go mcu.Run()
	defer mcu.Close()

	detected := make(chan Protocol, 1)
	s := NewAutoStream(line.OpenA(), newReliable, func(p Protocol) { detected <- p })
	router := msgpackrpc.NewConnection(s, s,
		func(_ msgpackrpc.FunctionLogger, method string, _ []any, res msgpackrpc.ResponseSender) {
			_ = res(method == "$/reset", nil)
		}, nil, nil)
	go router.Run()
	defer router.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, reqErr, err := mcu.SendRequest(ctx, "$/reset")
	require.NoError(t, err)
	require.Nil(t, reqErr)
	require.Equal(t, true, res)
	require.Equal(t, Raw, <-detected)
}

func TestAutoStreamRawAfterGarbage(t *testing.T) {
	// A reliable MCU at the negotiated rate, received at the base rate, is
	// replaced by a raw one. This garbage, captured on a UNO Q, starts with a
	// map and contains an array of 648 million elements: given to the
	// msgpack-rpc decoder, it swallows all the data that follows.
	garbage, err := hex.DecodeString("8ea6e2ae2ba6adae11e2abea9da6af2e5962fb6a9de6afee6902a9aa9926ad2e23af1da6cbaf8e46a6b6b59b27ad6e1122ab2add26af2e5996ab6a9d66ef9ec9a2")
	require.NoError(t, err)
	line := noisyline.New(1)
	mcuPort := line.OpenB()
	_, err = mcuPort.Write(garbage)
	require.NoError(t, err)
	mcu := msgpackrpc.NewConnection(mcuPort, mcuPort, nil, nil, nil)
	go mcu.Run()
	defer mcu.Close()

	s := NewAutoStream(line.OpenA(), newReliable, nil)
	router := msgpackrpc.NewConnection(s, s,
		func(_ msgpackrpc.FunctionLogger, method string, _ []any, res msgpackrpc.ResponseSender) {
			_ = res(method == "$/reset", nil)
		}, nil, nil)
	go router.Run()
	defer router.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, reqErr, err := mcu.SendRequest(ctx, "$/reset")
	require.NoError(t, err)
	require.Nil(t, reqErr)
	require.Equal(t, true, res)
}

func TestAutoStreamClose(t *testing.T) {
	// Closing the stream interrupts the detection and pending reads and writes
	line := noisyline.New(1)
	s := NewAutoStream(line.OpenA(), newReliable, nil)
	readDone := make(chan error, 1)
	go func() {
		_, err := s.Read(make([]byte, 10))
		readDone <- err
	}()
	writeDone := make(chan error, 1)
	go func() {
		_, err := s.Write([]byte{1})
		writeDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, s.Close())
	require.Error(t, <-readDone)
	require.Error(t, <-writeDone)

	// Close before any read
	s = NewAutoStream(line.OpenA(), newReliable, nil)
	require.NoError(t, s.Close())
	_, err := s.Write([]byte{1})
	require.Error(t, err)
}

func TestAutoStreamRawToReliable(t *testing.T) {
	// The router detected a raw MCU, then a reliable firmware is loaded: the
	// raw stream must fail, so that the router restarts the detection
	line := noisyline.New(1)
	mcuPort := line.OpenB()
	_, err := mcuPort.Write(encode(t, []any{2, "mon/write", []any{"raw firmware"}}))
	require.NoError(t, err)

	s := NewAutoStream(line.OpenA(), newReliable, nil)
	defer s.Close()
	dec := msgpack.NewDecoder(s)
	var msg []any
	require.NoError(t, dec.Decode(&msg))
	require.Equal(t, "mon/write", msg[1])

	// Some raw data containing zeros, then the new firmware starts
	_, err = mcuPort.Write(encode(t, []any{2, "mon/write", []any{"bye", 0, 0}}))
	require.NoError(t, err)
	_, err = mcuPort.Write(synFrame)
	require.NoError(t, err)
	_, err = io.ReadAll(s)
	require.ErrorIs(t, err, ErrProtocolChanged)
}
