// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package seriallink

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"github.com/arduino/arduino-router/internal/cobs"
)

const (
	protocolVersion = 1
	headerSize      = 4
	crcSize         = 4

	// MaxPayloadLimit is the maximum payload size supported by the protocol.
	MaxPayloadLimit = 4096
	// MaxWindowLimit is the maximum window supported by the protocol (Go-Back-N
	// requires the window to be smaller than half the sequence number space).
	MaxWindowLimit = 127
)

type frameType uint8

const (
	frameData   frameType = 1
	frameAck    frameType = 2
	frameNak    frameType = 3
	frameSyn    frameType = 4
	frameSynAck frameType = 5
	frameRst    frameType = 6
)

func (t frameType) String() string {
	switch t {
	case frameData:
		return "DATA"
	case frameAck:
		return "ACK"
	case frameNak:
		return "NAK"
	case frameSyn:
		return "SYN"
	case frameSynAck:
		return "SYN_ACK"
	case frameRst:
		return "RST"
	default:
		return "UNKNOWN"
	}
}

type frame struct {
	typ     frameType
	seq     uint8
	ack     uint8
	wnd     uint8
	payload []byte
}

var (
	errFrameTooShort = errors.New("frame too short")
	errFrameCRC      = errors.New("frame CRC mismatch")
	errFrameVersion  = errors.New("unsupported protocol version")
	errFrameType     = errors.New("unknown frame type")
)

// maxEncodedFrameSize returns the maximum size of a COBS-encoded frame (without
// the delimiter) carrying up to maxPayload bytes.
func maxEncodedFrameSize(maxPayload int) int {
	return cobs.MaxEncodedLen(headerSize + maxPayload + crcSize)
}

// appendEncoded appends the COBS-encoded frame, followed by the 0x00
// delimiter, to dst.
func (f *frame) appendEncoded(dst []byte) []byte {
	raw := make([]byte, 0, headerSize+len(f.payload)+crcSize)
	raw = append(raw, protocolVersion<<4|byte(f.typ), f.seq, f.ack, f.wnd)
	raw = append(raw, f.payload...)
	raw = binary.LittleEndian.AppendUint32(raw, crc32.ChecksumIEEE(raw))
	dst = cobs.Encode(dst, raw)
	return append(dst, 0)
}

// decodeFrame decodes a COBS-encoded frame (without the delimiter).
func decodeFrame(encoded []byte) (frame, error) {
	raw, err := cobs.Decode(make([]byte, 0, len(encoded)), encoded)
	if err != nil {
		return frame{}, err
	}
	if len(raw) < headerSize+crcSize {
		return frame{}, errFrameTooShort
	}
	body := raw[:len(raw)-crcSize]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(raw[len(body):]) {
		return frame{}, errFrameCRC
	}
	if body[0]>>4 != protocolVersion {
		return frame{}, errFrameVersion
	}
	f := frame{
		typ:     frameType(body[0] & 0x0F),
		seq:     body[1],
		ack:     body[2],
		wnd:     body[3],
		payload: body[headerSize:],
	}
	if f.typ < frameData || f.typ > frameRst {
		return frame{}, errFrameType
	}
	return f, nil
}

// synParams are the session parameters exchanged in SYN and SYN_ACK frames.
type synParams struct {
	maxPayload int
	window     int
	nonce      uint32
	echo       uint32 // only in SYN_ACK
}

func (p synParams) marshal(withEcho bool) []byte {
	b := binary.LittleEndian.AppendUint16(nil, uint16(p.maxPayload))
	b = append(b, uint8(p.window))
	b = binary.LittleEndian.AppendUint32(b, p.nonce)
	if withEcho {
		b = binary.LittleEndian.AppendUint32(b, p.echo)
	}
	return b
}

func parseSynParams(b []byte, withEcho bool) (synParams, bool) {
	size := 7
	if withEcho {
		size = 11
	}
	if len(b) != size {
		return synParams{}, false
	}
	p := synParams{
		maxPayload: int(binary.LittleEndian.Uint16(b[0:2])),
		window:     int(b[2]),
		nonce:      binary.LittleEndian.Uint32(b[3:7]),
	}
	if withEcho {
		p.echo = binary.LittleEndian.Uint32(b[7:11])
	}
	if p.maxPayload < 1 || p.maxPayload > MaxPayloadLimit || p.window < 1 || p.window > MaxWindowLimit {
		return synParams{}, false
	}
	return p, true
}
