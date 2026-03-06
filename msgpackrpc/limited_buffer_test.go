// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc_test

import (
	"testing"

	"github.com/arduino/arduino-router/msgpackrpc"

	"github.com/stretchr/testify/require"
)

func TestLimitedBuffer(t *testing.T) {
	buf := msgpackrpc.NewLimitedBuffer(10)
	require.Equal(t, 10, buf.Cap())

	n, err := buf.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, "hello", buf.String())
	require.Equal(t, 5, buf.Len())

	n, err = buf.Write([]byte("world"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, "helloworld", buf.String())
	require.Equal(t, 10, buf.Len())

	n, err = buf.Write([]byte("!"))
	require.Error(t, err)
	require.Equal(t, 0, n)
	require.Equal(t, "helloworld", buf.String())

	buf.Reset()
	require.Equal(t, "", buf.String())
	require.Equal(t, 0, buf.Len())

	n, err = buf.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, "hello", buf.String())
	require.Equal(t, 5, buf.Len())

	n, err = buf.Write([]byte("world"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, "helloworld", buf.String())
	require.Equal(t, 10, buf.Len())

	n, err = buf.Write([]byte("!"))
	require.Error(t, err)
	require.Equal(t, 0, n)
	require.Equal(t, "helloworld", buf.String())
}
