// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package cobs

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// seq returns the bytes from..to (inclusive).
func seq(from, to int) []byte {
	var b []byte
	for i := from; i <= to; i++ {
		b = append(b, byte(i))
	}
	return b
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func TestVectors(t *testing.T) {
	// Examples from https://en.wikipedia.org/wiki/Consistent_Overhead_Byte_Stuffing
	vectors := []struct{ decoded, encoded []byte }{
		{[]byte{}, []byte{0x01}},
		{[]byte{0x00}, []byte{0x01, 0x01}},
		{[]byte{0x00, 0x00}, []byte{0x01, 0x01, 0x01}},
		{[]byte{0x00, 0x11, 0x00}, []byte{0x01, 0x02, 0x11, 0x01}},
		{[]byte{0x11, 0x22, 0x00, 0x33}, []byte{0x03, 0x11, 0x22, 0x02, 0x33}},
		{[]byte{0x11, 0x22, 0x33, 0x44}, []byte{0x05, 0x11, 0x22, 0x33, 0x44}},
		{[]byte{0x11, 0x00, 0x00, 0x00}, []byte{0x02, 0x11, 0x01, 0x01, 0x01}},
		{seq(0x01, 0xFE), concat([]byte{0xFF}, seq(0x01, 0xFE))},
		{seq(0x00, 0xFE), concat([]byte{0x01, 0xFF}, seq(0x01, 0xFE))},
		{seq(0x01, 0xFF), concat([]byte{0xFF}, seq(0x01, 0xFE), []byte{0x02, 0xFF})},
		{concat(seq(0x02, 0xFF), []byte{0x00}), concat([]byte{0xFF}, seq(0x02, 0xFF), []byte{0x01, 0x01})},
		{concat(seq(0x03, 0xFF), []byte{0x00, 0x01}), concat([]byte{0xFE}, seq(0x03, 0xFF), []byte{0x02, 0x01})},
	}
	for _, v := range vectors {
		require.Equal(t, v.encoded, Encode(nil, v.decoded))
		dec, err := Decode(nil, v.encoded)
		require.NoError(t, err)
		require.Equal(t, v.decoded, append([]byte{}, dec...))
	}
}

func TestRoundTrip(t *testing.T) {
	cases := [][]byte{
		bytes.Repeat([]byte{1}, 253),
		bytes.Repeat([]byte{1}, 254),
		bytes.Repeat([]byte{1}, 255),
		bytes.Repeat([]byte{1}, 508),
		append(bytes.Repeat([]byte{1}, 254), 0),
		bytes.Repeat([]byte{0}, 600),
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 1000 {
		b := make([]byte, rng.IntN(1200))
		for i := range b {
			if rng.IntN(4) > 0 { // plenty of zeros, as in msgpack
				b[i] = byte(rng.Uint32())
			}
		}
		cases = append(cases, b)
	}

	for _, src := range cases {
		enc := Encode(nil, src)
		require.NotContains(t, enc, byte(0))
		require.LessOrEqual(t, len(enc), MaxEncodedLen(len(src)))
		dec, err := Decode(nil, enc)
		require.NoError(t, err)
		require.Equal(t, src, append([]byte{}, dec...))
	}
}

func TestAppend(t *testing.T) {
	enc := Encode([]byte{0xAA}, []byte{0x11, 0x00})
	require.Equal(t, []byte{0xAA, 0x02, 0x11, 0x01}, enc)
	dec, err := Decode([]byte{0xBB}, enc[1:])
	require.NoError(t, err)
	require.Equal(t, []byte{0xBB, 0x11, 0x00}, dec)
}

func TestInvalid(t *testing.T) {
	for _, enc := range [][]byte{
		{0x05, 0x01, 0x02},       // code points past the end
		{0x02, 0x00},             // contains a zero
		{0x00},                   // zero code
		{0xFF, 0x01},             // truncated full block
		{0x02, 0x11, 0x03, 0x22}, // truncated last block
	} {
		_, err := Decode(nil, enc)
		require.ErrorIs(t, err, ErrInvalid, "% X", enc)
	}
}
