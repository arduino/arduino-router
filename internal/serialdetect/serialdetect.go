// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package serialdetect detects which protocol the peer on a serial port is
// speaking: plain msgpack-rpc (raw) or msgpack-rpc over the seriallink
// protocol (reliable). This lets the router work with both old and new MCU
// firmwares.
//
// The detection looks at the data received from the peer:
//   - a valid seriallink frame (COBS encoding and CRC-32 check, so garbage is
//     accepted with a probability of about 2^-32) means reliable. A reliable
//     peer sends a SYN frame every few hundred milliseconds while waiting for
//     a session, and ACK or DATA frames if it is already in one;
//   - a complete and well-formed msgpack-rpc message means raw.
//
// Garbage (for example the boot messages of the MCU, or a reliable MCU at the
// negotiated rate received at the base rate) is skipped. Nothing is sent to the
// peer until the protocol is detected, and the data received during the
// detection is not lost: it is passed to the detected protocol, without the
// garbage preceding the first msgpack-rpc message (seriallink resynchronizes
// by itself, while the msgpack-rpc decoder may wait forever for a garbage
// message claiming a huge size, swallowing the valid messages that follow).
package serialdetect

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"unicode"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"

	"github.com/arduino/arduino-router/internal/seriallink"
)

// Protocol is a serial port protocol.
type Protocol int

const (
	// Raw is plain msgpack-rpc.
	Raw Protocol = iota
	// Reliable is msgpack-rpc over the seriallink protocol.
	Reliable
)

func (p Protocol) String() string {
	switch p {
	case Raw:
		return "raw"
	case Reliable:
		return "reliable"
	default:
		return "unknown"
	}
}

const (
	// maxIncompleteMessage is the maximum size of an incomplete msgpack-rpc
	// message we wait for: longer candidates are considered garbage.
	maxIncompleteMessage = 1024
	// maxBuffered is the maximum amount of undecided data kept in memory.
	maxBuffered = 64 * 1024
	// maxFrame is larger than any valid seriallink frame.
	maxFrame = 8 * 1024
)

// Detect reads from r until it can tell which protocol the peer is speaking.
// It returns the detected protocol and the data to be processed by the protocol
// handler before any other data read from r.
func Detect(r io.Reader) (Protocol, []byte, error) {
	var buf []byte
	chunk := make([]byte, 512)
	scanned := 0    // bytes already scanned for frame delimiters
	frameStart := 0 // start of the current frame candidate
	rpcStart := 0   // first offset not yet ruled out as start of a msgpack-rpc message
	for {
		n, err := r.Read(chunk)
		buf = append(buf, chunk[:n]...)

		// Look for a valid seriallink frame
		for ; scanned < len(buf); scanned++ {
			if buf[scanned] != 0 {
				continue
			}
			if scanned > frameStart && seriallink.IsValidFrame(buf[frameStart:scanned]) {
				return Reliable, buf, nil
			}
			frameStart = scanned + 1
		}

		// Look for a valid msgpack-rpc message
	rpcScan:
		for ; rpcStart < len(buf); rpcStart++ {
			switch checkRPCMessage(buf[rpcStart:]) {
			case valid:
				return Raw, buf[rpcStart:], nil
			case incomplete:
				if len(buf)-rpcStart <= maxIncompleteMessage {
					break rpcScan // wait for more data
				}
			}
		}

		if err != nil {
			return Raw, buf, err
		}

		// Drop the data that cannot be part of a message or a frame anymore
		if len(buf) > maxBuffered {
			drop := min(max(frameStart, len(buf)-maxFrame), rpcStart)
			buf = buf[drop:]
			scanned -= drop
			frameStart = max(frameStart-drop, 0) // a too long frame candidate is cut
			rpcStart -= drop
		}
	}
}

type checkResult int

const (
	invalid checkResult = iota
	incomplete
	valid
)

// checkRPCMessage checks if b starts with a well-formed msgpack-rpc message.
func checkRPCMessage(b []byte) checkResult {
	// msgpack-rpc messages are arrays of 3 or 4 elements
	if b[0] != msgpcode.FixedArrayLow|3 && b[0] != msgpcode.FixedArrayLow|4 {
		return invalid
	}
	dec := msgpack.NewDecoder(bytes.NewReader(b))
	result := func(err error) checkResult {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return incomplete
		}
		return invalid
	}
	size, _ := dec.DecodeArrayLen()
	msgType, err := dec.DecodeInt()
	if err != nil {
		return result(err)
	}
	switch {
	case msgType == 0 && size == 4: // request: [0, msgid, method, params]
		if _, err := dec.DecodeUint32(); err != nil {
			return result(err)
		}
		if res := checkMethodAndParams(dec); res != valid {
			return res
		}
	case msgType == 1 && size == 4: // response: [1, msgid, error, result]
		if _, err := dec.DecodeUint32(); err != nil {
			return result(err)
		}
		if err := dec.Skip(); err != nil {
			return result(err)
		}
		if err := dec.Skip(); err != nil {
			return result(err)
		}
	case msgType == 2 && size == 3: // notification: [2, method, params]
		if res := checkMethodAndParams(dec); res != valid {
			return res
		}
	default:
		return invalid
	}
	return valid
}

func checkMethodAndParams(dec *msgpack.Decoder) checkResult {
	result := func(err error) checkResult {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return incomplete
		}
		return invalid
	}
	method, err := dec.DecodeString()
	if err != nil {
		return result(err)
	}
	if method == "" || len(method) > 256 {
		return invalid
	}
	for _, c := range method {
		if c > unicode.MaxASCII || !unicode.IsPrint(c) {
			return invalid
		}
	}
	code, err := dec.PeekCode()
	if err != nil {
		return result(err)
	}
	if !msgpcode.IsFixedArray(code) && code != msgpcode.Array16 && code != msgpcode.Array32 {
		return invalid
	}
	if err := dec.Skip(); err != nil {
		return result(err)
	}
	return valid
}

// NewAutoStream returns a stream that detects the protocol spoken by the peer
// on port (see Detect) on the first Read, and then behaves as the raw port or
// as the stream created by newReliable. Writes block until the protocol is
// detected. onDetect, if not nil, is called with the detected protocol.
func NewAutoStream(port io.ReadWriteCloser, newReliable func(io.ReadWriteCloser) (io.ReadWriteCloser, error), onDetect func(Protocol)) io.ReadWriteCloser {
	return &autoStream{
		port:        port,
		newReliable: newReliable,
		onDetect:    onDetect,
		ready:       make(chan struct{}),
	}
}

type autoStream struct {
	port        io.ReadWriteCloser
	newReliable func(io.ReadWriteCloser) (io.ReadWriteCloser, error)
	onDetect    func(Protocol)

	detectOnce sync.Once
	ready      chan struct{} // closed when the detection is complete
	stream     io.ReadWriteCloser
	err        error
}

func (a *autoStream) detect() {
	defer close(a.ready)
	proto, buffered, err := Detect(a.port)
	if err != nil {
		a.err = err
		return
	}
	if a.onDetect != nil {
		a.onDetect(proto)
	}
	replay := &replayPort{ReadWriteCloser: a.port, pending: buffered}
	if proto == Raw {
		a.stream = &rawStream{ReadWriteCloser: replay}
		return
	}
	a.stream, a.err = a.newReliable(replay)
}

func (a *autoStream) Read(p []byte) (int, error) {
	a.detectOnce.Do(a.detect)
	if a.err != nil {
		return 0, a.err
	}
	return a.stream.Read(p)
}

func (a *autoStream) Write(p []byte) (int, error) {
	<-a.ready
	if a.err != nil {
		return 0, a.err
	}
	return a.stream.Write(p)
}

func (a *autoStream) Close() error {
	// Closing the port interrupts a running detection
	err := a.port.Close()
	select {
	case <-a.ready:
		if a.stream != nil {
			return a.stream.Close()
		}
	default:
		// Detection not started (no Read yet) or still running: make sure
		// pending Writes are released
		go a.detectOnce.Do(a.detect)
	}
	return err
}

// ErrProtocolChanged is returned by the raw stream of NewAutoStream when the
// peer starts speaking the reliable protocol (e.g. a new MCU firmware has
// been loaded): the connection must be dropped and the detection restarted.
var ErrProtocolChanged = errors.New("serialdetect: the peer switched to the reliable protocol")

// rawStream watches the raw data for seriallink frames.
type rawStream struct {
	io.ReadWriteCloser
	candidate  []byte // current frame candidate
	discarding bool   // the current candidate is too long to be a frame
}

func (r *rawStream) Read(p []byte) (int, error) {
	n, err := r.ReadWriteCloser.Read(p)
	for _, b := range p[:n] {
		if b != 0 {
			if !r.discarding {
				r.candidate = append(r.candidate, b)
				if len(r.candidate) > maxFrame {
					r.candidate, r.discarding = r.candidate[:0], true
				}
			}
			continue
		}
		if !r.discarding && len(r.candidate) > 0 && seriallink.IsValidFrame(r.candidate) {
			return 0, ErrProtocolChanged
		}
		r.candidate, r.discarding = r.candidate[:0], false
	}
	return n, err
}

// replayPort returns the pending data before reading again from the port.
type replayPort struct {
	io.ReadWriteCloser
	mu      sync.Mutex
	pending []byte
}

func (r *replayPort) Read(p []byte) (int, error) {
	r.mu.Lock()
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		r.mu.Unlock()
		return n, nil
	}
	r.mu.Unlock()
	return r.ReadWriteCloser.Read(p)
}
