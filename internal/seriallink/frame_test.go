// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package seriallink

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFrameEncoding(t *testing.T) {
	f := frame{typ: frameData, seq: 7, ack: 200, wnd: 3, payload: []byte{0x94, 0x00, 0x01, 0x00, 0xA4, 'p', 'i', 'n', 'g', 0x90}}
	enc := f.appendEncoded(nil)
	require.Equal(t, byte(0), enc[len(enc)-1])
	require.NotContains(t, enc[:len(enc)-1], byte(0))

	got, err := decodeFrame(enc[:len(enc)-1])
	require.NoError(t, err)
	require.Equal(t, f, got)

	// Every single bit flip must be detected
	for i := range len(enc) - 1 {
		for bit := range 8 {
			corrupted := bytes.Clone(enc[:len(enc)-1])
			corrupted[i] ^= 1 << bit
			_, err := decodeFrame(corrupted)
			require.Error(t, err, "byte %d bit %d", i, bit)
		}
	}

	// Truncated frames must be detected
	for i := range len(enc) - 1 {
		_, err := decodeFrame(enc[:i])
		require.Error(t, err)
	}
}

func TestSynParams(t *testing.T) {
	p := synParams{maxPayload: 256, window: 8, nonce: 0xDEADBEEF, echo: 0x12345678}
	got, ok := parseSynParams(p.marshal(true), true)
	require.True(t, ok)
	require.Equal(t, p, got)

	p.echo = 0
	got, ok = parseSynParams(p.marshal(false), false)
	require.True(t, ok)
	require.Equal(t, p, got)

	_, ok = parseSynParams(p.marshal(false), true)
	require.False(t, ok)
	_, ok = parseSynParams(synParams{maxPayload: 256, window: 200}.marshal(false), false)
	require.False(t, ok)
}
