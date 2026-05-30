// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpack_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/arduino/arduino-router/msgpack"

	"github.com/stretchr/testify/require"
)

func TestBooleanConstants(t *testing.T) {
	require.Equal(t, msgpack.NewBool(true), msgpack.True)
	require.Equal(t, msgpack.NewBool(false), msgpack.False)
}

func TestOneByteElements(t *testing.T) {
	t.Run("Nil", func(t *testing.T) {
		t.Run("Decode", func(t *testing.T) {
			in := bytes.NewBuffer([]byte{0xc0}) // nil

			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindNil)
			_, ok := v.(msgpack.Nil)
			require.True(t, ok)
			require.Equal(t, "nil", v.String())
		})
		t.Run("Encode", func(t *testing.T) {
			e, err := msgpack.Marshal(msgpack.NewNil())
			require.NoError(t, err)
			require.Equal(t, []byte{0xc0}, e)
			e, err = msgpack.Marshal(nil)
			require.NoError(t, err)
			require.Equal(t, []byte{0xc0}, e)
		})
	})

	t.Run("Booleans", func(t *testing.T) {
		t.Run("Decode", func(t *testing.T) {
			in := bytes.NewBuffer([]byte{0xc2, 0xc3}) // false, true

			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindBool)
			bv := v.(msgpack.Bool)
			require.False(t, bool(bv))
			require.Equal(t, "false", v.String())

			v, err = msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindBool)
			bv = v.(msgpack.Bool)
			require.True(t, bool(bv))
			require.Equal(t, "true", v.String())
		})
		t.Run("Encode", func(t *testing.T) {
			e, err := msgpack.Marshal(msgpack.False)
			require.NoError(t, err)
			require.Equal(t, []byte{0xc2}, e)
			e, err = msgpack.Marshal(false)
			require.NoError(t, err)
			require.Equal(t, []byte{0xc2}, e)
			e, err = msgpack.Marshal(msgpack.True)
			require.NoError(t, err)
			require.Equal(t, []byte{0xc3}, e)
			e, err = msgpack.Marshal(true)
			require.NoError(t, err)
			require.Equal(t, []byte{0xc3}, e)
		})
	})

	t.Run("OneByteIntegers", func(t *testing.T) {
		// Test one-byte integers
		in := bytes.NewBuffer(nil)
		for i := 0; i <= 0x7f; i++ {
			in.WriteByte(byte(i))
		}
		for i := 0xe0; i <= 0xff; i++ {
			in.WriteByte(byte(i))
		}

		for i := 0; i <= 127; i++ {
			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindInteger)
			iv := v.(msgpack.Integer)
			require.True(t, iv.IsSigned())
			{
				ivint, ok := iv.Int()
				require.Equal(t, i, ivint)
				require.True(t, ok)
			}
			{
				ivint64, ok := iv.Int64()
				require.Equal(t, int64(i), ivint64)
				require.True(t, ok)
			}
			{
				ivuint64, ok := iv.Uint64()
				require.Equal(t, uint64(i), ivuint64)
				require.True(t, ok)
			}

			e, err := msgpack.Marshal(v)
			require.NoError(t, err)
			require.Equal(t, []byte{byte(i)}, e)
		}
		for i := -32; i < 0; i++ {
			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindInteger)
			iv := v.(msgpack.Integer)
			require.True(t, iv.IsSigned())
			{
				ivint, ok := iv.Int()
				require.Equal(t, i, ivint)
				require.True(t, ok)
			}
			{
				ivint64, ok := iv.Int64()
				require.Equal(t, int64(i), ivint64)
				require.True(t, ok)
			}
			{
				_, ok := iv.Uint64()
				require.False(t, ok)
			}

			e, err := msgpack.Marshal(v)
			require.NoError(t, err)
			require.Equal(t, []byte{byte(0x100 + i)}, e)
		}
	})
}

func TestUnsignedIntegers(t *testing.T) {
	t.Run("UnsignedIntegers", func(t *testing.T) {
		// Test uints
		for _, test := range []struct {
			data     []byte
			expected uint64
			str      string
			reencode []byte
		}{
			{[]byte{0xcc, 0x11}, 0x11, "17", []byte{0x11}},
			{[]byte{0xcd, 0x11, 0x22}, 0x1122, "4386", []byte{0xd1, 0x11, 0x22}},
			{[]byte{0xd1, 0x11, 0x22}, 0x1122, "4386", []byte{0xd1, 0x11, 0x22}},
			{[]byte{0xce, 0x11, 0x22, 0x33, 0x44}, 0x11223344, "287454020", []byte{0xd2, 0x11, 0x22, 0x33, 0x44}},
			{[]byte{0xd2, 0x11, 0x22, 0x33, 0x44}, 0x11223344, "287454020", []byte{0xd2, 0x11, 0x22, 0x33, 0x44}},
			{[]byte{0xcf, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}, 0x1122334455667788, "1234605616436508552", []byte{0xd3, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}},
			{[]byte{0xd3, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}, 0x1122334455667788, "1234605616436508552", []byte{0xd3, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}},
		} {
			in := bytes.NewBuffer(test.data)
			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindInteger)
			iv := v.(msgpack.Integer)
			require.True(t, iv.IsSigned())
			vi, ok := iv.Int()
			require.Equal(t, int(test.expected), vi)
			require.True(t, ok)
			ui, ok := iv.Uint64()
			require.Equal(t, test.expected, ui)
			require.True(t, ok)
			ii, ok := iv.Int64()
			if test.expected <= math.MaxInt64 {
				require.Equal(t, int64(test.expected), ii)
			} else {
				require.FailNow(t, "invalid test: expected value %v does not fit in int64", test.expected)
			}
			require.True(t, ok)
			require.Equal(t, test.str, v.String())

			e, err := msgpack.Marshal(v)
			require.NoError(t, err)
			require.Equal(t, test.reencode, e)
		}
		{
			in := bytes.NewBuffer([]byte{0xcf, 0xFF, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88})
			expected := uint64(0xFF22334455667788)
			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindInteger)
			iv := v.(msgpack.Integer)
			require.False(t, iv.IsSigned())
			_, ok := iv.Int()
			require.False(t, ok)
			ui, ok := iv.Uint64()
			require.Equal(t, expected, ui)
			require.True(t, ok)
			_, ok = iv.Int64()
			require.False(t, ok)
			require.Equal(t, "18384312997463357320", v.String())

			e, err := msgpack.Marshal(v)
			require.NoError(t, err)
			require.Equal(t, []byte{0xcf, 0xFF, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}, e)
		}
	})

	t.Run("IncompleteUnsignedIntegers", func(t *testing.T) {
		// Test incomplete uints

		in := bytes.NewBuffer([]byte{0xcc}) // missing one byte
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xcd, 0x11}) // missing one byte
		_, err = msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xce, 0x11, 0x22, 0x33}) // missing one byte
		_, err = msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xcf, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}) // missing one byte
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})
}

func TestSignedIntegers(t *testing.T) {
	t.Run("SignedIntegers", func(t *testing.T) {
		for _, test := range []struct {
			data     []byte
			expected int64
			str      string
			reencode []byte
		}{
			{[]byte{0xd0, 0x11}, 17, "17", []byte{0x11}},
			{[]byte{0xd0, 0xee}, -18, "-18", []byte{0xee}},
			{[]byte{0xd1, 0x11, 0x22}, 4386, "4386", []byte{0xd1, 0x11, 0x22}},
			{[]byte{0xd1, 0xee, 0xdd}, -4387, "-4387", []byte{0xd1, 0xee, 0xdd}},
			{[]byte{0xd2, 0x11, 0x22, 0x33, 0x44}, 287454020, "287454020", []byte{0xd2, 0x11, 0x22, 0x33, 0x44}},
			{[]byte{0xd2, 0xee, 0xdd, 0xcc, 0xbb}, -287454021, "-287454021", []byte{0xd2, 0xee, 0xdd, 0xcc, 0xbb}},
			{[]byte{0xd3, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}, 1234605616436508552, "1234605616436508552", []byte{0xd3, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}},
			{[]byte{0xd3, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77}, -1234605616436508553, "-1234605616436508553", []byte{0xd3, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77}},
		} {
			in := bytes.NewBuffer(test.data)
			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindInteger)
			iv := v.(msgpack.Integer)
			require.True(t, iv.IsSigned())
			vi, ok := iv.Int64()
			require.Equal(t, test.expected, vi)
			require.True(t, ok)
			ui, ok := iv.Uint64()
			if test.expected < 0 {
				require.False(t, ok)
			} else {
				require.Equal(t, uint64(test.expected), ui)
				require.True(t, ok)
			}
			iint, ok := iv.Int()
			require.Equal(t, int(test.expected), iint)
			require.True(t, ok)
			require.Equal(t, test.str, v.String())

			e, err := msgpack.Marshal(v)
			require.NoError(t, err)
			require.Equal(t, test.reencode, e)
		}
	})

	t.Run("IncompleteSignedIntegers", func(t *testing.T) {
		// Test incomplete ints

		in := bytes.NewBuffer([]byte{0xd0}) // missing one byte
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xd1, 0x11}) // missing one byte
		_, err = msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xd2, 0x11, 0x22, 0x33}) // missing one byte
		_, err = msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xd3, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}) // missing one byte
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})
}

func TestFloats(t *testing.T) {
	t.Run("Floats32", func(t *testing.T) {
		// Test some float32 values
		in := bytes.NewBuffer([]byte{
			0xca, 0x41, 0x20, 0x00, 0x00, // 10.0
			0xca, 0xc1, 0x20, 0x00, 0x00, // -10.0
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindFloat32)
		fv := v.(msgpack.Float32)
		require.Equal(t, float32(10.0), float32(fv))
		require.Equal(t, "10.000000", v.String())

		e, err := msgpack.Marshal(v)
		require.NoError(t, err)
		require.Equal(t, []byte{0xca, 0x41, 0x20, 0x00, 0x00}, e)
		e, err = msgpack.Marshal(float32(fv))
		require.NoError(t, err)
		require.Equal(t, []byte{0xca, 0x41, 0x20, 0x00, 0x00}, e)

		v, err = msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindFloat32)
		fv = v.(msgpack.Float32)
		require.Equal(t, float32(-10.0), float32(fv))
		require.Equal(t, "-10.000000", v.String())

		e, err = msgpack.Marshal(v)
		require.NoError(t, err)
		require.Equal(t, []byte{0xca, 0xc1, 0x20, 0x00, 0x00}, e)
		e, err = msgpack.Marshal(float32(fv))
		require.NoError(t, err)
		require.Equal(t, []byte{0xca, 0xc1, 0x20, 0x00, 0x00}, e)
	})

	t.Run("Floats64", func(t *testing.T) {
		// Test some float64 values
		in := bytes.NewBuffer([]byte{
			0xcb, 0x40, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // 10.0
			0xcb, 0xc0, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // -10.0
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindFloat64)
		fv := v.(msgpack.Float64)
		require.Equal(t, float64(10.0), float64(fv))
		require.Equal(t, "F10.000000", v.String())

		e, err := msgpack.Marshal(v)
		require.NoError(t, err)
		require.Equal(t, []byte{0xcb, 0x40, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, e)
		e, err = msgpack.Marshal(float64(fv))
		require.NoError(t, err)
		require.Equal(t, []byte{0xcb, 0x40, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, e)

		v, err = msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindFloat64)
		fv = v.(msgpack.Float64)
		require.Equal(t, float64(-10.0), float64(fv))
		require.Equal(t, "F-10.000000", v.String())

		e, err = msgpack.Marshal(v)
		require.NoError(t, err)
		require.Equal(t, []byte{0xcb, 0xc0, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, e)
		e, err = msgpack.Marshal(float64(fv))
		require.NoError(t, err)
		require.Equal(t, []byte{0xcb, 0xc0, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, e)
	})

	t.Run("IncompleteFloat32", func(t *testing.T) {
		// Test incomplete float32
		in := bytes.NewBuffer([]byte{
			0xca, 0x41, 0x20, 0x00, // missing one byte
		})

		_, err := msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("IncompleteFloat64", func(t *testing.T) {
		// Test incomplete float64
		in := bytes.NewBuffer([]byte{
			0xcb, 0x40, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, // missing one byte
		})

		_, err := msgpack.Decode(in)
		require.Error(t, err)
	})
}

func TestStrings(t *testing.T) {
	t.Run("FixStr", func(t *testing.T) {
		// Test fixstr
		in := bytes.NewBuffer([]byte{
			0xa5, 'H', 'e', 'l', 'l', 'o', // "Hello"
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindString)

		sv := v.(msgpack.String)
		require.Equal(t, "Hello", string(sv))

		e, err := msgpack.Marshal(v)
		require.NoError(t, err)
		require.Equal(t, []byte{0xa5, 'H', 'e', 'l', 'l', 'o'}, e)
		e, err = msgpack.Marshal(string(sv))
		require.NoError(t, err)
		require.Equal(t, []byte{0xa5, 'H', 'e', 'l', 'l', 'o'}, e)
	})

	t.Run("IncompleteFixStr", func(t *testing.T) {
		// Test incomplete fixstr
		in := bytes.NewBuffer([]byte{
			0xa5, 'H', 'e', 'l', // missing 'l', 'o'
		})

		_, err := msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("Str8", func(t *testing.T) {
		t.Run("Decode", func(t *testing.T) {
			in := bytes.NewBuffer([]byte{
				0xd9, 0x0b, // str8 with length 11
				'H', 'e', 'l', 'l', 'o', ' ', 'W', 'o', 'r', 'l', 'd',
			})

			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindString)

			sv := v.(msgpack.String)
			require.Equal(t, "Hello World", string(sv))
		})
		t.Run("Encode", func(t *testing.T) {
			str := strings.Repeat("x", 200)
			expected := append([]byte{0xd9, 0xc8}, []byte(str)...)
			e, err := msgpack.Marshal(str)
			require.NoError(t, err)
			require.Equal(t, expected, e)
			e, err = msgpack.Marshal(str)
			require.NoError(t, err)
			require.Equal(t, expected, e)
		})
	})

	t.Run("IncompleteStr8", func(t *testing.T) {
		// Test incomplete str8
		in := bytes.NewBuffer([]byte{0xd9, 0x0b, 'H', 'e', 'l', 'l', 'o'}) // str8 with length 11 missing rest of the string
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xd9}) // str8 missing length and string
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("Str16", func(t *testing.T) {
		t.Run("Decode", func(t *testing.T) {
			in := bytes.NewBuffer([]byte{
				0xda, 0x00, 0x0e, // str16 with length 14
				'H', 'e', 'l', 'l', 'o', ',', ' ', 'M', 's', 'g', 'p', 'a', 'c', 'k',
			})

			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindString)

			sv := v.(msgpack.String)
			require.Equal(t, "Hello, Msgpack", string(sv))
		})
		t.Run("Encode", func(t *testing.T) {
			str := strings.Repeat("x", 400)
			expected := append([]byte{0xda, 0x01, 0x90}, []byte(str)...)

			e, err := msgpack.Marshal(str)
			require.NoError(t, err)
			require.Equal(t, expected, e)
			e, err = msgpack.Marshal(str)
			require.NoError(t, err)
			require.Equal(t, expected, e)
		})
	})

	t.Run("IncompleteStr16", func(t *testing.T) {
		// Test incomplete str16
		in := bytes.NewBuffer([]byte{0xda, 0x00, 0x0c, 'H', 'e', 'l', 'l', 'o', ','}) // str16 with length 12 missing rest of the string
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xda, 0x00}) // str16 missing length and string
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("Str32", func(t *testing.T) {
		t.Run("Decode", func(t *testing.T) {
			in := bytes.NewBuffer([]byte{
				0xdb, 0x00, 0x00, 0x00, 0x11, // str32 with length 17
				'H', 'e', 'l', 'l', 'o', ' ', 'f', 'r', 'o', 'm', ' ', 'S', 't', 'r', '3', '2', '!',
			})

			v, err := msgpack.Decode(in)
			require.NoError(t, err)
			require.Equal(t, v.Kind(), msgpack.ValueKindString)

			sv := v.(msgpack.String)
			require.Equal(t, "Hello from Str32!", string(sv))
		})
		t.Run("Encode", func(t *testing.T) {
			str := strings.Repeat("y", 70000)
			expected := append([]byte{0xdb, 0x00, 0x01, 0x11, 0x70}, []byte(str)...)

			e, err := msgpack.Marshal(str)
			require.NoError(t, err)
			require.Equal(t, expected, e)
			e, err = msgpack.Marshal(str)
			require.NoError(t, err)
			require.Equal(t, expected, e)
		})
	})

	t.Run("IncompleteStr32", func(t *testing.T) {
		// Test incomplete str32
		in := bytes.NewBuffer([]byte{0xdb, 0x00, 0x00, 0x00, 0x11, 'H', 'e', 'l', 'l', 'o'}) // str32 with length 17 missing rest of the string
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xdb, 0x00, 0x00, 0x00}) // str32 missing length and string
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})
}

func TestBinaryDecode(t *testing.T) {
	t.Run("Bin8", func(t *testing.T) {
		// Test bin8
		in := bytes.NewBuffer([]byte{
			0xc4, 0x05, // bin8 with length 5
			0x01, 0x02, 0x03, 0x04, 0x05,
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindBinary)

		bv := v.(msgpack.Binary)
		require.Equal(t, []byte{0x01, 0x02, 0x03, 0x04, 0x05}, []byte(bv))
		require.Equal(t, "B0102030405", v.String())
	})

	t.Run("IncompleteBin8", func(t *testing.T) {
		// Test incomplete bin8
		in := bytes.NewBuffer([]byte{0xc4, 0x05, 0x01, 0x02}) // bin8 with length 5 missing rest of the bytes
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc4}) // bin8 missing length and bytes
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("Bin16", func(t *testing.T) {
		// Test bin16
		in := bytes.NewBuffer([]byte{
			0xc5, 0x00, 0x06, // bin16 with length 6
			0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindBinary)

		bv := v.(msgpack.Binary)
		require.Equal(t, []byte{0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}, []byte(bv))
		require.Equal(t, "B0a0b0c0d0e0f", v.String())
	})

	t.Run("IncompleteBin16", func(t *testing.T) {
		// Test incomplete bin16
		in := bytes.NewBuffer([]byte{0xc5, 0x00, 0x06, 0x0a, 0x0b}) // bin16 with length 6 missing rest of the bytes
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc5, 0x00}) // bin16 missing length and bytes
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("Bin32", func(t *testing.T) {
		// Test bin32
		in := bytes.NewBuffer([]byte{
			0xc6, 0x00, 0x00, 0x00, 0x07, // bin32 with length 7
			0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindBinary)

		bv := v.(msgpack.Binary)
		require.Equal(t, []byte{0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20}, []byte(bv))
	})

	t.Run("IncompleteBin32", func(t *testing.T) {
		// Test incomplete bin32
		in := bytes.NewBuffer([]byte{0xc6, 0x00, 0x00, 0x00, 0x07, 0x1a, 0x1b}) // bin32 with length 7 missing rest of the bytes
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc6, 0x00, 0x00, 0x00}) // bin32 missing length and bytes
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})
}

func TestArraysDecode(t *testing.T) {
	t.Run("FixArray", func(t *testing.T) {
		// Test fixarray
		in := bytes.NewBuffer([]byte{
			0x93,                // array of 3 elements
			0x01,                // element 1: 1
			0xa3, 'f', 'o', 'o', // element 2: "foo"
			0xc3, // element 3: true
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindArray)

		av := v.(msgpack.Array)
		require.Len(t, av, 3)

		{
			elem1 := av[0]
			require.Equal(t, msgpack.ValueKindInteger, elem1.Kind())
			intElem1, ok := elem1.(msgpack.Integer)
			require.True(t, ok)
			i1, ok := intElem1.Int()
			require.True(t, ok)
			require.Equal(t, 1, i1)
		}

		{
			elem2 := av[1]
			require.Equal(t, msgpack.ValueKindString, elem2.Kind())
			strElem2, ok := elem2.(msgpack.String)
			require.True(t, ok)
			require.Equal(t, "foo", string(strElem2))
		}

		{
			elem3 := av[2]
			require.Equal(t, msgpack.ValueKindBool, elem3.Kind())
			boolElem3, ok := elem3.(msgpack.Bool)
			require.True(t, ok)
			require.True(t, bool(boolElem3))
		}
	})

	t.Run("IncompleteFixArray", func(t *testing.T) {
		// Test incomplete fixarray
		in := bytes.NewBuffer([]byte{
			0x93, // array of 3 elements
			0x01, // element 1: 1
			// missing element 2 and 3
		})

		_, err := msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("Array16", func(t *testing.T) {
		// Test array16
		in := bytes.NewBuffer([]byte{
			0xdc, 0x00, 0x03, // array of 3 elements
			0x01,                // element 1: 1
			0xa3, 'f', 'o', 'o', // element 2: "foo"
			0xc3, // element 3: true
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindArray)

		av := v.(msgpack.Array)
		require.Len(t, av, 3)
		require.Equal(t, `[1,"foo",true]`, av.String())
	})

	t.Run("IncompleteArray16", func(t *testing.T) {
		// Test incomplete array16
		in := bytes.NewBuffer([]byte{
			0xdc, 0x00, 0x03, // array of 3 elements
			0x01, // element 1: 1
			// missing element 2 and 3
		})
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{
			0xdc, 0x00, // incomplete length
		})
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("Array32", func(t *testing.T) {
		// Test array32
		in := bytes.NewBuffer([]byte{
			0xdd, 0x00, 0x00, 0x00, 0x03, // array of 3 elements
			0x01,                // element 1: 1
			0xa3, 'f', 'o', 'o', // element 2: "foo"
			0xc3, // element 3: true
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindArray)

		av := v.(msgpack.Array)
		require.Len(t, av, 3)
		require.Equal(t, `[1,"foo",true]`, av.String())
	})

	t.Run("IncompleteArray32", func(t *testing.T) {
		// Test incomplete array32
		in := bytes.NewBuffer([]byte{
			0xdd, 0x00, 0x00, 0x00, 0x03, // array of 3 elements
			0x01, // element 1: 1
			// missing element 2 and 3
		})
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{
			0xdd, 0x00, 0x00, 0x00, // incomplete length
		})
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})
}

func TestMapsDecode(t *testing.T) {
	t.Run("FixMap", func(t *testing.T) {
		// Test fixmap
		in := bytes.NewBuffer([]byte{
			0x82,                         // map of 2 elements
			0x92, 0xA1, 0x31, 0xA1, 0x32, // key: [ "1", "2" ]
			0x01,                // value: 1
			0xa3, 'b', 'a', 'r', // key: "bar"
			0x02, // value: 2
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindMap)
		require.Equal(t, `{["1","2"]:1,"bar":2}`, v.String())
		mv := v.(msgpack.Map)
		require.Equal(t, 2, mv.Len())

		{
			fooKey := msgpack.Array{msgpack.String("1"), msgpack.String("2")}
			oneValue, ok := mv.Get(fooKey)
			require.True(t, ok)
			oneInt, ok := oneValue.(msgpack.Integer)
			require.True(t, ok)
			one, ok := oneInt.Int()
			require.True(t, ok)
			require.Equal(t, 1, one)
		}

		{
			fakeFooKey := msgpack.String(`["1","2"]`)
			_, ok := mv.Get(fakeFooKey)
			require.False(t, ok)
		}

		{
			barKey := msgpack.String("bar")
			twoValue, ok := mv.Get(barKey)
			require.True(t, ok)
			twoInt, ok := twoValue.(msgpack.Integer)
			require.True(t, ok)
			two, ok := twoInt.Int()
			require.True(t, ok)
			require.Equal(t, 2, two)
		}
	})

	t.Run("IncompleteFixMap", func(t *testing.T) {
		// Test incomplete fixmap
		{
			in := bytes.NewBuffer([]byte{
				0x82,                         // map of 2 elements
				0x92, 0xA1, 0x31, 0xA1, 0x32, // key: [ "1", "2" ]
				// missing first value
			})
			_, err := msgpack.Decode(in)
			require.Error(t, err)
		}
		{
			in := bytes.NewBuffer([]byte{
				0x82, // map of 2 elements
				// missing first key
			})
			_, err := msgpack.Decode(in)
			require.Error(t, err)
		}
	})

	t.Run("Map16", func(t *testing.T) {
		// Test map16
		in := bytes.NewBuffer([]byte{
			0xde, 0x00, 0x02, // map of 2 elements
			0x92, 0xA1, 0x31, 0xA1, 0x32, // key: [ "1", "2" ]
			0x01,                // value: 1
			0xa3, 'b', 'a', 'r', // key: "bar"
			0x02, // value: 2
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindMap)
		require.Equal(t, `{["1","2"]:1,"bar":2}`, v.String())
	})

	t.Run("IncompleteMap16", func(t *testing.T) {
		// Test incomplete map16
		in := bytes.NewBuffer([]byte{
			0xde, 0x00, 0x02, // map of 2 elements
			0x92, 0xA1, 0x31, 0xA1, 0x32, // key: [ "1", "2" ]
			// missing first value
		})
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{
			0xde, 0x00, // incomplete length
		})
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("Map32", func(t *testing.T) {
		// Test map32
		in := bytes.NewBuffer([]byte{
			0xdf, 0x00, 0x00, 0x00, 0x02, // map of 2 elements
			0x92, 0xA1, 0x31, 0xA1, 0x32, // key: [ "1", "2" ]
			0x01,                // value: 1
			0xa3, 'b', 'a', 'r', // key: "bar"
			0x02, // value: 2
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindMap)
		require.Equal(t, `{["1","2"]:1,"bar":2}`, v.String())
	})

	t.Run("IncompleteMap32", func(t *testing.T) {
		// Test incomplete map32
		in := bytes.NewBuffer([]byte{
			0xdf, 0x00, 0x00, 0x00, 0x02, // map of 2 elements
			0x92, 0xA1, 0x31, 0xA1, 0x32, // key: [ "1", "2" ]
			// missing first value
		})
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{
			0xdf, 0x00, 0x00, // incomplete length
		})
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})
}

func TestExtensionDecode(t *testing.T) {
	t.Run("FixExt1", func(t *testing.T) {
		// Test fixext1
		in := bytes.NewBuffer([]byte{
			0xd4, 0x05, 0xFF, // type: 5, data: 0xFF
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindExtension)

		ev := v.(msgpack.Extension)
		require.Equal(t, int8(5), ev.Type)
		require.Equal(t, []byte{0xFF}, ev.Data)
		require.Equal(t, "Ext05(ff)", v.String())
	})

	t.Run("IncompleteFixExt1", func(t *testing.T) {
		// Test incomplete fixext1
		in := bytes.NewBuffer([]byte{0xd4, 0x05}) // missing data byte
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xd4}) // missing type and data byte
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("FixExt2", func(t *testing.T) {
		// Test fixext2
		in := bytes.NewBuffer([]byte{
			0xd5, 0x06, 0xAA, 0xBB, // type: 6, data: 0xAABB
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindExtension)

		ev := v.(msgpack.Extension)
		require.Equal(t, int8(6), ev.Type)
		require.Equal(t, []byte{0xAA, 0xBB}, ev.Data)
		require.Equal(t, "Ext06(aabb)", v.String())
	})

	t.Run("IncompleteFixExt2", func(t *testing.T) {
		// Test incomplete fixext2
		in := bytes.NewBuffer([]byte{0xd5, 0x06, 0xAA}) // missing one data byte
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xd5}) // missing type and data bytes
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("FixExt4", func(t *testing.T) {
		// Test fixext4
		in := bytes.NewBuffer([]byte{
			0xd6, 0x07, 0xDE, 0xAD, 0xBE, 0xEF, // type: 7, data: 0xDEADBEEF
		})

		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, v.Kind(), msgpack.ValueKindExtension)

		ev := v.(msgpack.Extension)
		require.Equal(t, int8(7), ev.Type)
		require.Equal(t, []byte{0xDE, 0xAD, 0xBE, 0xEF}, ev.Data)
		require.Equal(t, "Ext07(deadbeef)", v.String())
	})
}

func TestUnusedCode(t *testing.T) {
	// This test is only to cover some unused code paths
	in := bytes.NewBuffer([]byte{0xc1}) // reserved byte

	_, err := msgpack.Decode(in)
	require.Error(t, err)
}

func TestMappingKeyHashing(t *testing.T) {
	amap := msgpack.NewMap()
	amap.Set(msgpack.NewInteger(1), msgpack.NewString("ciao"))

	m := msgpack.NewMap()
	m.Set(msgpack.NewNil(), msgpack.NewInteger(1))
	m.Set(msgpack.NewBool(true), msgpack.NewInteger(2))
	m.Set(msgpack.NewBool(false), msgpack.NewInteger(3))
	m.Set(msgpack.NewInteger(1), msgpack.NewInteger(4))
	m.Set(msgpack.NewInteger(-1), msgpack.NewInteger(5))
	m.Set(msgpack.NewInteger(uint64(0xFFFF_FFFF_FFFF_FFFF)), msgpack.NewInteger(6))
	m.Set(msgpack.NewFloat32(1), msgpack.NewInteger(7))
	m.Set(msgpack.NewFloat64(1), msgpack.NewInteger(8))
	m.Set(msgpack.NewString("true"), msgpack.NewInteger(9))
	m.Set(msgpack.NewString("false"), msgpack.NewInteger(10))
	m.Set(amap, msgpack.NewInteger(11))

	require.Equal(t, 11, m.Len())
	isTrue := func(v msgpack.Value, check bool) msgpack.Value {
		require.True(t, check)
		return v
	}
	require.Equal(t, msgpack.NewInteger(1), isTrue(m.Get(msgpack.NewNil())))
	require.Equal(t, msgpack.NewInteger(2), isTrue(m.Get(msgpack.NewBool(true))))
	require.Equal(t, msgpack.NewInteger(3), isTrue(m.Get(msgpack.NewBool(false))))
	require.Equal(t, msgpack.NewInteger(4), isTrue(m.Get(msgpack.NewInteger(1))))
	require.Equal(t, msgpack.NewInteger(5), isTrue(m.Get(msgpack.NewInteger(-1))))
	require.Equal(t, msgpack.NewInteger(6), isTrue(m.Get(msgpack.NewInteger(uint64(0xFFFF_FFFF_FFFF_FFFF)))))
	require.Equal(t, msgpack.NewInteger(7), isTrue(m.Get(msgpack.NewFloat32(1))))
	require.Equal(t, msgpack.NewInteger(8), isTrue(m.Get(msgpack.NewFloat64(1))))
	require.Equal(t, msgpack.NewInteger(9), isTrue(m.Get(msgpack.NewString("true"))))
	require.Equal(t, msgpack.NewInteger(10), isTrue(m.Get(msgpack.NewString("false"))))
	require.Equal(t, msgpack.NewInteger(11), isTrue(m.Get(amap)))
}

func TestMappingSetGetDelete(t *testing.T) {
	m := msgpack.NewMap()
	m.Set(msgpack.NewString("key1"), msgpack.NewString("value1"))
	m.Set(msgpack.NewString("key2"), msgpack.NewString("value2"))

	require.Equal(t, 2, m.Len())
	v, ok := m.Get(msgpack.NewString("key1"))
	require.True(t, ok)
	require.Equal(t, msgpack.NewString("value1"), v)
	v, ok = m.Get(msgpack.NewString("key2"))
	require.True(t, ok)
	require.Equal(t, msgpack.NewString("value2"), v)

	m.Delete(msgpack.NewString("key1"))
	require.Equal(t, 1, m.Len())
	v, ok = m.Get(msgpack.NewString("key1"))
	require.False(t, ok)
	require.Nil(t, v)
	v, ok = m.Get(msgpack.NewString("key2"))
	require.True(t, ok)
	require.Equal(t, msgpack.NewString("value2"), v)

	m.Delete(msgpack.NewString("key2"))
	require.Equal(t, 0, m.Len())
	v, ok = m.Get(msgpack.NewString("key1"))
	require.False(t, ok)
	require.Nil(t, v)
	v, ok = m.Get(msgpack.NewString("key2"))
	require.False(t, ok)
	require.Nil(t, v)
}

func TestMappingIterator(t *testing.T) {
	m := msgpack.NewMap()
	m.Set(msgpack.NewString("key1"), msgpack.NewString("value1"))
	m.Set(msgpack.NewString("key2"), msgpack.NewString("value2"))
	m.Set(msgpack.NewString("key3"), msgpack.NewInteger(3))
	m.Set(msgpack.NewString("key4"), msgpack.False)
	m.Set(msgpack.NewString("key5"), msgpack.NewNil())
	m.Set(msgpack.NewString("key2"), msgpack.NewString("replaced"))

	keys := []msgpack.Value{}
	values := []msgpack.Value{}
	for k, v := range m.Iterate() {
		keys = append(keys, k)
		values = append(values, v)
	}

	require.Equal(t, []msgpack.Value{
		msgpack.NewString("key1"),
		msgpack.NewString("key3"),
		msgpack.NewString("key4"),
		msgpack.NewString("key5"),
		msgpack.NewString("key2"),
	}, keys)
	require.Equal(t, []msgpack.Value{
		msgpack.NewString("value1"),
		msgpack.NewInteger(3),
		msgpack.False,
		msgpack.NewNil(),
		msgpack.NewString("replaced"),
	}, values)
}
