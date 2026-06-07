// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

func TestOrderedMapEncode(t *testing.T) {
	m1 := OrderedMap{
		{Key: "a", Value: 1},
		{Key: "b", Value: 2},
		{Key: "c", Value: 3},
		{Key: "d", Value: 4},
	}

	b, err := msgpack.Marshal(m1)
	require.NoError(t, err)

	var m2 OrderedMap
	err = msgpack.Unmarshal(b, &m2)
	require.NoError(t, err)

	assert.Equal(t, len(m1), len(m2))

	for i, kv := range m1 {
		assert.Equal(t, kv.Key, m2[i].Key)
		v1, ok := ToInt(kv.Value)
		require.True(t, ok)
		v2, ok := ToInt(m2[i].Value)
		require.True(t, ok)
		assert.Equal(t, v1, v2)
	}
}
