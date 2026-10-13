// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package seriallink

// Interoperability tests with the C++ implementation of the protocol, that
// runs on the MCU (Arduino_RouterBridge library). They use a test peer that
// runs the C++ link over stdin/stdout and echoes everything it receives:
//
//	make -C <Arduino_RouterBridge>/extras/seriallink-test peer
//	SERIALLINK_PEER=<Arduino_RouterBridge>/extras/seriallink-test/seriallink_peer \
//	  go test ./internal/seriallink -run Interop -v
//
// The tests are skipped if SERIALLINK_PEER is not set.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arduino/arduino-router/internal/noisyline"
)

// cppPeerTiming makes the C++ peer use the same timing as testConfig.
var cppPeerTiming = []string{
	"--rto", strconv.Itoa(int(testConfig.RetransmitTimeout.Milliseconds())),
	"--ack-delay", strconv.Itoa(int(testConfig.AckDelay.Milliseconds())),
	"--keepalive", strconv.Itoa(int(testConfig.KeepAliveInterval.Milliseconds())),
	"--link-timeout", strconv.Itoa(int(testConfig.LinkTimeout.Milliseconds())),
	"--max-retries", strconv.Itoa(testConfig.MaxRetries),
}

// cppPeer is a running C++ test peer, connected to side B of a line.
type cppPeer struct {
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	port       *noisyline.Port
	stdinDone  chan struct{}
	stdoutDone chan struct{}
	stopOnce   sync.Once
}

// logWriter forwards the peer stderr to the test log.
type logWriter struct{ t *testing.T }

func (w logWriter) Write(p []byte) (int, error) {
	w.t.Log(string(bytes.TrimRight(p, "\n")))
	return len(p), nil
}

func startCppPeer(t *testing.T, line *noisyline.Line, args ...string) *cppPeer {
	bin := os.Getenv("SERIALLINK_PEER")
	if bin == "" {
		t.Skip("SERIALLINK_PEER not set, see interop_test.go")
	}
	cmd := exec.Command(bin, append(slices.Clone(cppPeerTiming), args...)...)
	cmd.Stderr = logWriter{t}
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	p := &cppPeer{
		cmd:        cmd,
		stdin:      stdin,
		port:       line.OpenB(),
		stdinDone:  make(chan struct{}),
		stdoutDone: make(chan struct{}),
	}
	go func() {
		defer close(p.stdinDone)
		_, _ = io.Copy(stdin, p.port)
		_ = stdin.Close()
	}()
	go func() {
		defer close(p.stdoutDone)
		_, _ = io.Copy(p.port, stdout)
	}()
	t.Cleanup(p.kill)
	return p
}

// kill terminates the peer abruptly, like an MCU reset.
func (p *cppPeer) kill() {
	p.stopOnce.Do(func() {
		_ = p.cmd.Process.Kill()
		<-p.stdoutDone
		_ = p.cmd.Wait()
		_ = p.port.Close()
		<-p.stdinDone
	})
}

// close terminates the peer gracefully: it closes the session (sending a
// RST) and exits.
func (p *cppPeer) close(t *testing.T) {
	p.stopOnce.Do(func() {
		_ = p.stdin.Close()
		<-p.stdoutDone
		require.NoError(t, p.cmd.Wait())
		_ = p.port.Close()
		<-p.stdinDone
	})
}

// echo sends size random bytes through the link and checks that the peer
// echoes them back intact.
func echo(t *testing.T, l *Link, size int) {
	data := randomData(uint64(size), size)
	writeErr := make(chan error, 1)
	go func() {
		for p := data; len(p) > 0; {
			n := min(len(p), 1+len(p)%997)
			if _, err := l.Write(p[:n]); err != nil {
				writeErr <- err
				return
			}
			p = p[n:]
		}
		writeErr <- nil
	}()
	got := make([]byte, size)
	_, err := io.ReadFull(l, got)
	require.NoError(t, err)
	require.NoError(t, <-writeErr)
	require.True(t, bytes.Equal(data, got), "echoed data mismatch")
}

func TestInteropEcho(t *testing.T) {
	line := noisyline.New(1)
	startCppPeer(t, line)
	a := newLink(t, line.OpenA())
	echo(t, a, 256*1024)
	require.Zero(t, a.Stats().RxErrors)
	require.Zero(t, a.Stats().Retransmits)
}

func TestInteropNoisyLine(t *testing.T) {
	line := noisyline.New(1)
	line.SetFaults(testFaults)
	startCppPeer(t, line)
	a := newLink(t, line.OpenA())
	echo(t, a, 64*1024)
	t.Logf("Go side: %+v", a.Stats())
	require.NotZero(t, line.AtoB().Stats().Flipped+line.BtoA().Stats().Flipped, "faults were not injected")
	require.NotZero(t, a.Stats().RxErrors, "no corrupted frame received")
	require.NotZero(t, a.Stats().Retransmits, "nothing was retransmitted")
}

func TestInteropFlowControl(t *testing.T) {
	// The peer does not read for 3 link timeouts: the Go side must wait
	// without overflowing it, and the link must stay alive
	stall := 3 * testConfig.LinkTimeout
	line := noisyline.New(1)
	startCppPeer(t, line, "--stall", strconv.Itoa(int(stall.Milliseconds())))
	a := newLink(t, line.OpenA())

	data := randomData(1, 40*1024)
	writeDone := make(chan error, 1)
	go func() {
		_, err := a.Write(data)
		writeDone <- err
	}()
	time.Sleep(stall / 2)
	select {
	case err := <-writeDone:
		require.Fail(t, "Write must block while the peer is not reading", "err: %v", err)
	default:
	}

	got := make([]byte, len(data))
	_, err := io.ReadFull(a, got)
	require.NoError(t, err)
	require.NoError(t, <-writeDone)
	require.True(t, bytes.Equal(data, got), "echoed data mismatch")
	require.Zero(t, a.Stats().Retransmits)
}

func testInteropPeerReboot(t *testing.T, args ...string) {
	line := noisyline.New(1)
	p := startCppPeer(t, line, args...)
	a := newLink(t, line.OpenA())
	echo(t, a, 1000)

	// The MCU resets, the router must notice the new session...
	p.kill()
	startCppPeer(t, line, args...)
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrSessionReset)

	// ...and open a new link
	require.NoError(t, a.Close())
	a = newLink(t, line.OpenA())
	echo(t, a, 10000)
}

func TestInteropPeerReboot(t *testing.T) {
	testInteropPeerReboot(t)
}

func TestInteropPeerRebootSameNonce(t *testing.T) {
	testInteropPeerReboot(t, "--nonce", "42")
}

func TestInteropPeerClose(t *testing.T) {
	line := noisyline.New(1)
	p := startCppPeer(t, line)
	a := newLink(t, line.OpenA())
	echo(t, a, 1000)

	p.close(t)
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrSessionReset)
}

func TestInteropRouterRestart(t *testing.T) {
	line := noisyline.New(1)
	startCppPeer(t, line)
	a := newLink(t, line.OpenA())
	echo(t, a, 1000)

	// The router closes the link (sending a RST): the MCU must start a new
	// session that the next router link can join
	require.NoError(t, a.Close())
	a = newLink(t, line.OpenA())
	echo(t, a, 10000)
}

func TestInteropRouterRestartSilent(t *testing.T) {
	line := noisyline.New(1)
	startCppPeer(t, line)
	a := newLink(t, line.OpenA())
	echo(t, a, 1000)

	// The router restarts without being able to notify the MCU: the MCU must
	// detect the new session from the SYN nonce
	line.AtoB().SetBlackhole(true)
	require.NoError(t, a.Close())
	line.AtoB().SetBlackhole(false)
	a = newLink(t, line.OpenA())
	echo(t, a, 10000)
}

func TestInteropLinkTimeout(t *testing.T) {
	line := noisyline.New(1)
	startCppPeer(t, line)
	a := newLink(t, line.OpenA())
	echo(t, a, 1000)

	// The line goes down: both sides time out...
	line.SetBlackhole(true)
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrLinkTimeout)

	// ...and when it comes back a new session is established
	time.Sleep(testConfig.LinkTimeout)
	line.SetBlackhole(false)
	require.NoError(t, a.Close())
	a = newLink(t, line.OpenA())
	echo(t, a, 10000)
}
