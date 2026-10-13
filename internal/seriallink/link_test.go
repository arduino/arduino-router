// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package seriallink

import (
	"bytes"
	"context"
	"io"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arduino/arduino-router/internal/noisyline"
	"github.com/arduino/arduino-router/msgpackrpc"
)

// testFaults gives a byte error rate of about 1e-3, so about 25% of the full
// size frames are lost.
var testFaults = noisyline.Faults{BitFlip: 5e-4, Drop: 2e-4, Duplicate: 2e-4, Garbage: 2e-4}

var testConfig = Config{
	RetransmitTimeout: 20 * time.Millisecond,
	AckDelay:          2 * time.Millisecond,
	KeepAliveInterval: 50 * time.Millisecond,
	LinkTimeout:       500 * time.Millisecond,
	MaxRetries:        20,
}

func newLink(t *testing.T, p io.ReadWriteCloser) *Link {
	l, err := New(p, testConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func randomData(seed uint64, size int) []byte {
	rng := rand.New(rand.NewPCG(seed, 0))
	b := make([]byte, size)
	for i := range b {
		if rng.IntN(3) > 0 {
			b[i] = byte(rng.Uint32())
		}
	}
	return b
}

// exchange sends data in both directions concurrently and checks it arrives intact.
func exchange(t *testing.T, a, b *Link, size int) {
	dataA, dataB := randomData(10, size), randomData(20, size)
	var wg sync.WaitGroup
	check := func(dst io.Reader, exp []byte) {
		defer wg.Done()
		got := make([]byte, len(exp))
		_, err := io.ReadFull(dst, got)
		require.NoError(t, err)
		require.True(t, bytes.Equal(exp, got), "data mismatch")
	}
	send := func(src io.Writer, data []byte) {
		defer wg.Done()
		// Write in random sized chunks, like an upper layer would do
		rng := rand.New(rand.NewPCG(uint64(len(data)), 3))
		for len(data) > 0 {
			n := min(len(data), 1+rng.IntN(1000))
			_, err := src.Write(data[:n])
			require.NoError(t, err)
			data = data[n:]
		}
	}
	wg.Add(4)
	go check(b, dataA)
	go check(a, dataB)
	go send(a, dataA)
	go send(b, dataB)
	wg.Wait()
}

func TestCleanLine(t *testing.T) {
	l := noisyline.New(1)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())
	exchange(t, a, b, 512*1024)
	require.Zero(t, a.Stats().RxErrors)
	require.Zero(t, b.Stats().RxErrors)
}

func TestNoisyLine(t *testing.T) {
	l := noisyline.New(1)
	l.SetFaults(testFaults)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())
	exchange(t, a, b, 128*1024)
	sa, sb := a.Stats(), b.Stats()
	t.Logf("A: %+v", sa)
	t.Logf("B: %+v", sb)
	require.NotZero(t, sa.RxErrors+sb.RxErrors, "faults were not injected")
	require.NotZero(t, sa.Retransmits+sb.Retransmits, "nothing was retransmitted")
}

func TestPeerStartsLater(t *testing.T) {
	l := noisyline.New(1)
	a := newLink(t, l.OpenA())
	data := randomData(1, 10000)
	writeDone := make(chan error, 1)
	go func() {
		_, err := a.Write(data)
		writeDone <- err
	}()

	time.Sleep(200 * time.Millisecond)
	select {
	case <-writeDone:
		require.Fail(t, "Write must block until the session is established")
	default:
	}

	b := newLink(t, l.OpenB())
	got := make([]byte, len(data))
	_, err := io.ReadFull(b, got)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.NoError(t, <-writeDone)
}

func TestFlowControl(t *testing.T) {
	l := noisyline.New(1)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())
	data := randomData(1, 40*1024) // 20x the default receive buffer
	writeDone := make(chan error, 1)
	go func() {
		_, err := a.Write(data)
		writeDone <- err
	}()

	// B does not read for longer than the link timeout: A must wait without
	// overflowing B, and the link must stay alive thanks to keep-alives.
	time.Sleep(3 * testConfig.LinkTimeout)
	select {
	case err := <-writeDone:
		require.Fail(t, "Write must block while the peer is not reading", "err: %v", err)
	default:
	}
	require.Zero(t, a.Stats().Retransmits)

	got := make([]byte, len(data))
	_, err := io.ReadFull(b, got)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.NoError(t, <-writeDone)
}

func TestPeerClose(t *testing.T) {
	l := noisyline.New(1)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())
	exchange(t, a, b, 1000)

	require.NoError(t, b.Close())
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrSessionReset)
	_, err = a.Write([]byte{1})
	require.ErrorIs(t, err, ErrSessionReset)
}

func TestPeerReboot(t *testing.T) {
	l := noisyline.New(1)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())
	exchange(t, a, b, 1000)

	// B "reboots" without being able to notify A
	l.BtoA().SetBlackhole(true)
	require.NoError(t, b.Close())
	l.BtoA().SetBlackhole(false)
	b = newLink(t, l.OpenB())

	// A detects the new session...
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrSessionReset)

	// ...and a new link can be established
	require.NoError(t, a.Close())
	a = newLink(t, l.OpenA())
	exchange(t, a, b, 10000)
}

func TestStaleSession(t *testing.T) {
	l := noisyline.New(1)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())
	exchange(t, a, b, 1000)

	// A restarts while B is silent, so B misses the new SYN and keeps talking
	// in the old session: A must reset it with a RST.
	l.AtoB().SetBlackhole(true)
	require.NoError(t, a.Close())
	a = newLink(t, l.OpenA())
	time.Sleep(50 * time.Millisecond)
	l.AtoB().SetBlackhole(false)

	go func() { _, _ = b.Write([]byte("old session")) }()
	_, err := b.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrSessionReset)

	require.NoError(t, b.Close())
	b = newLink(t, l.OpenB())
	exchange(t, a, b, 10000)
}

func TestLinkTimeout(t *testing.T) {
	l := noisyline.New(1)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())
	exchange(t, a, b, 1000)

	l.SetBlackhole(true)
	start := time.Now()
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrLinkTimeout)
	require.Less(t, time.Since(start), 2*testConfig.LinkTimeout)
}

func TestClose(t *testing.T) {
	l := noisyline.New(1)
	a := newLink(t, l.OpenA())
	readDone := make(chan error, 1)
	go func() {
		_, err := a.Read(make([]byte, 10))
		readDone <- err
	}()
	require.NoError(t, a.Close())
	require.ErrorIs(t, <-readDone, ErrClosed)
	<-a.Done()
}

func TestMsgpackRPCOverNoisyLine(t *testing.T) {
	l := noisyline.New(1)
	l.SetFaults(testFaults)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())

	server := msgpackrpc.NewConnection(b, b,
		func(_ msgpackrpc.FunctionLogger, method string, params []any, res msgpackrpc.ResponseSender) {
			_ = res(params, nil) // echo
		}, nil, nil)
	client := msgpackrpc.NewConnection(a, a, nil, nil, nil)
	go server.Run()
	go client.Run()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 25 {
				payload := randomData(uint64(i*100+j), 1+(i*j*97)%3000)
				res, reqErr, err := client.SendRequest(ctx, "echo", payload)
				require.NoError(t, err)
				require.Nil(t, reqErr)
				require.Equal(t, []any{payload}, res)
			}
		})
	}
	wg.Wait()
	t.Logf("A: %+v", a.Stats())
	t.Logf("B: %+v", b.Stats())
}

func TestInvalidConfig(t *testing.T) {
	_, err := New(noisyline.New(1).OpenA(), Config{Window: 128})
	require.Error(t, err)
	_, err = New(noisyline.New(1).OpenA(), Config{MaxPayload: MaxPayloadLimit + 1})
	require.Error(t, err)
	_, err = New(noisyline.New(1).OpenA(), Config{KeepAliveInterval: time.Second, LinkTimeout: time.Second})
	require.Error(t, err)
}

func TestPeerRebootSameNonce(t *testing.T) {
	l := noisyline.New(1)
	a, b := newLink(t, l.OpenA()), newLink(t, l.OpenB())
	exchange(t, a, b, 1000)

	// B reboots silently and picks the same nonce as before (e.g. because it
	// has no good source of randomness)
	l.BtoA().SetBlackhole(true)
	require.NoError(t, b.Close())
	l.BtoA().SetBlackhole(false)
	oldNonce := b.nonce
	newNonce = func() uint32 { return oldNonce }
	defer func() { newNonce = rand.Uint32 }()
	b = newLink(t, l.OpenB())

	// A must not mistake the new SYN for a retransmission of the old one
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrSessionReset)

	require.NoError(t, a.Close())
	a = newLink(t, l.OpenA())
	exchange(t, a, b, 10000)
}
