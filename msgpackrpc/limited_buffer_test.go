// This file is part of arduino-router
//
// Copyright (C) ARDUINO SRL (www.arduino.cc)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-router
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

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
