// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package serialdetect

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arduino/arduino-router/internal/noisyline"
)

// TestInteropAutoDetect checks that the C++ implementation (the MCU side) is
// detected as reliable. See internal/seriallink/interop_test.go for how to
// build the peer: the test is skipped if SERIALLINK_PEER is not set.
func TestInteropAutoDetect(t *testing.T) {
	bin := os.Getenv("SERIALLINK_PEER")
	if bin == "" {
		t.Skip("SERIALLINK_PEER not set, see internal/seriallink/interop_test.go")
	}
	line := noisyline.New(1)
	port := line.OpenB()
	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	go func() { _, _ = io.Copy(stdin, port); _ = stdin.Close() }()
	go func() { _, _ = io.Copy(port, stdout) }()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = port.Close()
	}()

	detected := make(chan Protocol, 1)
	s := NewAutoStream(line.OpenA(), newReliable, func(p Protocol) { detected <- p })
	defer s.Close()

	// The peer echoes everything
	readDone := make(chan []byte, 1)
	go func() {
		got := make([]byte, 5000)
		_, err := io.ReadFull(s, got)
		require.NoError(t, err)
		readDone <- got
	}()
	data := bytes.Repeat([]byte("0123456789"), 500)
	_, err = s.Write(data)
	require.NoError(t, err)
	require.Equal(t, data, <-readDone)
	require.Equal(t, Reliable, <-detected)
}
