// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package noisyline

import (
	"bytes"
	"io"
	"math/bits"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var testData = bytes.Repeat([]byte("Hello, noisy world! \x00\xFF"), 100)

// readAvailable reads what has been delivered so far (Write never blocks, so
// after Write returns everything is already in the wire buffer).
func readAvailable(t *testing.T, p *Port) []byte {
	p.rx.mu.Lock()
	n := len(p.rx.buf)
	p.rx.mu.Unlock()
	b := make([]byte, n)
	_, err := io.ReadFull(p, b)
	require.NoError(t, err)
	return b
}

func TestCleanLine(t *testing.T) {
	l := New(1)
	a, b := l.OpenA(), l.OpenB()

	_, err := a.Write(testData)
	require.NoError(t, err)
	require.Equal(t, testData, readAvailable(t, b))

	_, err = b.Write([]byte("reply"))
	require.NoError(t, err)
	require.Equal(t, []byte("reply"), readAvailable(t, a))

	require.Equal(t, Stats{Bytes: len(testData)}, l.AtoB().Stats())
	require.Equal(t, Stats{Bytes: 5}, l.BtoA().Stats())
}

func TestFaults(t *testing.T) {
	t.Run("BitFlip", func(t *testing.T) {
		l := New(1)
		l.AtoB().SetFaults(Faults{BitFlip: 1})
		a, b := l.OpenA(), l.OpenB()
		_, _ = a.Write(testData)
		got := readAvailable(t, b)
		require.Len(t, got, len(testData))
		for i := range got {
			require.Equal(t, 1, bits.OnesCount8(got[i]^testData[i]), "exactly one bit must be flipped")
		}
		require.Equal(t, len(testData), l.AtoB().Stats().Flipped)
	})
	t.Run("Drop", func(t *testing.T) {
		l := New(1)
		l.AtoB().SetFaults(Faults{Drop: 1})
		a, b := l.OpenA(), l.OpenB()
		_, _ = a.Write(testData)
		require.Empty(t, readAvailable(t, b))
		require.Equal(t, len(testData), l.AtoB().Stats().Dropped)
	})
	t.Run("Duplicate", func(t *testing.T) {
		l := New(1)
		l.AtoB().SetFaults(Faults{Duplicate: 1})
		a, b := l.OpenA(), l.OpenB()
		_, _ = a.Write([]byte{1, 2, 3})
		require.Equal(t, []byte{1, 1, 2, 2, 3, 3}, readAvailable(t, b))
	})
	t.Run("Garbage", func(t *testing.T) {
		l := New(1)
		l.AtoB().SetFaults(Faults{Garbage: 1})
		a, b := l.OpenA(), l.OpenB()
		_, _ = a.Write([]byte{1, 2, 3})
		got := readAvailable(t, b)
		require.Len(t, got, 6)
		require.Equal(t, []byte{1, 2, 3}, []byte{got[1], got[3], got[5]})
	})
	t.Run("Probabilistic", func(t *testing.T) {
		l := New(1)
		l.AtoB().SetFaults(Faults{BitFlip: 0.1})
		a, b := l.OpenA(), l.OpenB()
		data := bytes.Repeat([]byte{0}, 10000)
		_, _ = a.Write(data)
		require.Len(t, readAvailable(t, b), len(data))
		require.InDelta(t, 1000, l.AtoB().Stats().Flipped, 150)
	})
	t.Run("OnlyOneDirection", func(t *testing.T) {
		l := New(1)
		l.AtoB().SetFaults(Faults{Drop: 1})
		a, b := l.OpenA(), l.OpenB()
		_, _ = b.Write(testData)
		require.Equal(t, testData, readAvailable(t, a))
	})
}

func TestReproducible(t *testing.T) {
	run := func(seed uint64) []byte {
		l := New(seed)
		l.SetFaults(Faults{BitFlip: 0.05, Drop: 0.05, Duplicate: 0.05, Garbage: 0.05})
		a, b := l.OpenA(), l.OpenB()
		_, _ = a.Write(testData)
		return readAvailable(t, b)
	}
	require.Equal(t, run(42), run(42))
	require.NotEqual(t, run(42), run(43))
}

func TestBlackhole(t *testing.T) {
	l := New(1)
	a, b := l.OpenA(), l.OpenB()
	l.SetBlackhole(true)
	_, err := a.Write(testData)
	require.NoError(t, err)
	require.Empty(t, readAvailable(t, b))
	require.Equal(t, len(testData), l.AtoB().Stats().Blackholed)

	l.SetBlackhole(false)
	_, _ = a.Write([]byte("back"))
	require.Equal(t, []byte("back"), readAvailable(t, b))
}

func TestCloseAndReopen(t *testing.T) {
	l := New(1)
	a, b := l.OpenA(), l.OpenB()

	// Close unblocks a pending Read
	readDone := make(chan error, 1)
	go func() {
		_, err := b.Read(make([]byte, 10))
		readDone <- err
	}()
	time.Sleep(10 * time.Millisecond)
	require.NoError(t, b.Close())
	require.ErrorIs(t, <-readDone, io.EOF)

	// A closed port can't be used anymore
	_, err := b.Write([]byte{1})
	require.ErrorIs(t, err, io.ErrClosedPipe)
	_, err = b.Read(make([]byte, 10))
	require.ErrorIs(t, err, io.EOF)

	// Data sent while B is closed is received by the reopened port
	_, _ = a.Write([]byte("in flight"))
	b = l.OpenB()
	require.Equal(t, []byte("in flight"), readAvailable(t, b))
}

func TestBaudRate(t *testing.T) {
	l := New(1)
	a, b := l.OpenA(), l.OpenB()
	require.Equal(t, DefaultBaudRate, a.BaudRate())

	// Different rates: garbage
	require.NoError(t, a.SetBaudRate(921600))
	_, err := a.Write(testData)
	require.NoError(t, err)
	got := readAvailable(t, b)
	require.Len(t, got, len(testData))
	require.NotEqual(t, testData, got)
	require.Equal(t, len(testData), l.AtoB().Stats().Mismatched)

	// Same rate: clean
	require.NoError(t, b.SetBaudRate(921600))
	_, err = b.Write(testData)
	require.NoError(t, err)
	require.Equal(t, testData, readAvailable(t, a))

	// Reopening a port resets its rate
	require.NoError(t, b.Close())
	require.Error(t, b.SetBaudRate(115200))
	b = l.OpenB()
	require.Equal(t, DefaultBaudRate, b.BaudRate())
	require.Equal(t, 921600, a.BaudRate())
}
