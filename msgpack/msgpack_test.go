// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpack_test

import (
	"bytes"
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/arduino/arduino-router/msgpack"

	"github.com/stretchr/testify/require"
)

// failWriter is an io.Writer that always returns an error.
type failWriter struct{}

func (failWriter) Write(p []byte) (int, error) { return 0, errors.New("write error") }

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
			fitsInt := test.expected <= math.MaxInt
			require.Equal(t, fitsInt, ok)
			if fitsInt {
				require.Equal(t, int(test.expected), vi)
			}
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
			fitsInt := test.expected >= math.MinInt && test.expected <= math.MaxInt
			require.Equal(t, fitsInt, ok)
			if fitsInt {
				require.Equal(t, int(test.expected), iint)
			}
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

func TestEncodeNativeIntegerTypes(t *testing.T) {
	tests := []struct {
		val      any
		expected []byte
	}{
		// Negative fixint range (-32..-1)
		{int(-1), []byte{0xff}},
		{int8(-1), []byte{0xff}},
		// D0 int8 range (-128..-33)
		{int8(-100), []byte{0xd0, 0x9c}},
		{int(-100), []byte{0xd0, 0x9c}},
		// D1 int16 via signed negative
		{int16(-200), []byte{0xd1, 0xff, 0x38}},
		// D1 int16 via uint8 overflow (200 > MaxInt8)
		{uint8(200), []byte{0xd1, 0x00, 0xc8}},
		// D2 int32
		{int32(100000), []byte{0xd2, 0x00, 0x01, 0x86, 0xa0}},
		{int16(1000), []byte{0xd1, 0x03, 0xe8}},
		{uint16(40000), []byte{0xd2, 0x00, 0x00, 0x9c, 0x40}},
		{uint32(100000), []byte{0xd2, 0x00, 0x01, 0x86, 0xa0}},
		// Positive fixint
		{int64(42), []byte{0x2a}},
		{uint(42), []byte{0x2a}},
		{uint64(42), []byte{0x2a}},
	}
	for _, tt := range tests {
		e, err := msgpack.Marshal(tt.val)
		require.NoError(t, err, "for value %v (%T)", tt.val, tt.val)
		require.Equal(t, tt.expected, e, "for value %v (%T)", tt.val, tt.val)
	}
}

func TestEncodeBinary(t *testing.T) {
	t.Run("BinaryType", func(t *testing.T) {
		b := msgpack.NewBinary([]byte{0x01, 0x02, 0x03})
		e, err := msgpack.Marshal(b)
		require.NoError(t, err)
		require.Equal(t, []byte{0xc4, 0x03, 0x01, 0x02, 0x03}, e)
	})

	t.Run("ByteSliceType", func(t *testing.T) {
		b := []byte{0x0a, 0x0b, 0x0c}
		e, err := msgpack.Marshal(b)
		require.NoError(t, err)
		require.Equal(t, []byte{0xc4, 0x03, 0x0a, 0x0b, 0x0c}, e)
	})

	t.Run("Bin16", func(t *testing.T) {
		// 300 bytes triggers bin16 header (> 255)
		data := make([]byte, 300)
		for i := range data {
			data[i] = byte(i)
		}
		e, err := msgpack.Marshal(msgpack.Binary(data))
		require.NoError(t, err)
		require.Equal(t, []byte{0xc5, 0x01, 0x2c}, e[:3]) // 0x012c = 300
		require.Equal(t, data, e[3:])
	})

	t.Run("Bin32", func(t *testing.T) {
		// 70000 bytes triggers bin32 header (> 65535)
		data := make([]byte, 70000)
		for i := range data {
			data[i] = byte(i)
		}
		e, err := msgpack.Marshal(msgpack.Binary(data))
		require.NoError(t, err)
		require.Equal(t, []byte{0xc6, 0x00, 0x01, 0x11, 0x70}, e[:5]) // 0x00011170 = 70000
		require.Equal(t, data, e[5:])
	})
}

func TestEncodeArrayTypes(t *testing.T) {
	t.Run("ArrayType", func(t *testing.T) {
		a := msgpack.Array{msgpack.NewInteger(1), msgpack.NewString("foo")}
		e, err := msgpack.Marshal(a)
		require.NoError(t, err)
		require.Equal(t, []byte{0x92, 0x01, 0xa3, 'f', 'o', 'o'}, e)
	})

	t.Run("AnySlice", func(t *testing.T) {
		a := []any{msgpack.NewInteger(1), msgpack.NewString("foo")}
		e, err := msgpack.Marshal(a)
		require.NoError(t, err)
		require.Equal(t, []byte{0x92, 0x01, 0xa3, 'f', 'o', 'o'}, e)
	})

	t.Run("Array16", func(t *testing.T) {
		// 20 elements → array16 header (> 15)
		a := make(msgpack.Array, 20)
		for i := range a {
			a[i] = msgpack.NewInteger(i)
		}
		e, err := msgpack.Marshal(a)
		require.NoError(t, err)
		require.Equal(t, []byte{0xdc, 0x00, 0x14}, e[:3]) // 0x14 = 20
		require.Len(t, e, 3+20)                           // 20 fixints, each 1 byte
	})

	t.Run("Array32", func(t *testing.T) {
		// 0x12345 elements → array32 header (> 65535 == 0xffff)
		a := msgpack.NewArray()
		for range 0x12345 {
			a = append(a, msgpack.NewInteger(5))
		}
		e, err := msgpack.Marshal(a)
		require.NoError(t, err)
		require.Equal(t, []byte{0xdd, 0x00, 0x01, 0x23, 0x45}, e[:5])
		require.Len(t, e, 5+0x12345)
	})
}

func TestEncodeMapTypes(t *testing.T) {
	t.Run("MapType", func(t *testing.T) {
		m := msgpack.NewMap()
		m.Set(msgpack.NewString("key"), msgpack.NewInteger(42))
		e, err := msgpack.Marshal(m)
		require.NoError(t, err)
		require.Equal(t, []byte{0x81, 0xa3, 'k', 'e', 'y', 0x2a}, e)
	})

	t.Run("AnyMap", func(t *testing.T) {
		// Single entry to avoid non-deterministic ordering issues
		m := map[any]any{msgpack.NewString("key"): msgpack.NewInteger(42)}
		e, err := msgpack.Marshal(m)
		require.NoError(t, err)
		require.Equal(t, []byte{0x81, 0xa3, 'k', 'e', 'y', 0x2a}, e)
	})

	t.Run("Map16", func(t *testing.T) {
		// 20 entries → map16 header (> 15)
		m := msgpack.NewMap()
		for i := range 20 {
			m.Set(msgpack.NewInteger(i), msgpack.NewInteger(i*10))
		}
		e, err := msgpack.Marshal(m)
		require.NoError(t, err)
		require.Equal(t, []byte{0xde, 0x00, 0x14}, e[:3]) // 0x14 = 20
	})

	t.Run("Map32", func(t *testing.T) {
		// 70000 entries → map32 header (> 65535 == 0xffff)
		m := msgpack.NewMap()
		for i := range 70000 {
			m.Set(msgpack.NewInteger(i), msgpack.NewInteger(i*10))
		}
		e, err := msgpack.Marshal(m)
		require.NoError(t, err)
		require.Equal(t, []byte{0xdf, 0x00, 0x01, 0x11, 0x70}, e[:5]) // 0x00011170 = 70000
	})
}

func TestEncodeReflectTypes(t *testing.T) {
	t.Run("TypedMap", func(t *testing.T) {
		m := map[string]int{"a": 1}
		e, err := msgpack.Marshal(m)
		require.NoError(t, err)
		// fixmap(1) + fixstr("a") + fixint(1)
		require.Equal(t, []byte{0x81, 0xa1, 'a', 0x01}, e)
	})

	t.Run("TypedSlice", func(t *testing.T) {
		s := []int{1, 2, 3}
		e, err := msgpack.Marshal(s)
		require.NoError(t, err)
		require.Equal(t, []byte{0x93, 0x01, 0x02, 0x03}, e)
	})

	t.Run("TypedArray", func(t *testing.T) {
		a := [3]int{1, 2, 3}
		e, err := msgpack.Marshal(a)
		require.NoError(t, err)
		require.Equal(t, []byte{0x93, 0x01, 0x02, 0x03}, e)
	})
}

func TestEncodeUnsupportedType(t *testing.T) {
	_, err := msgpack.Marshal(struct{}{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported type")

	// Also exercises the Marshal error-return path
	_, err = msgpack.Marshal(struct{ x int }{1})
	require.Error(t, err)
}

func TestEncodeWriteErrors(t *testing.T) {
	fw := failWriter{}
	require.Error(t, msgpack.Encode(msgpack.NewNil(), fw))
	require.Error(t, msgpack.Encode(msgpack.True, fw))
	require.Error(t, msgpack.Encode(msgpack.NewInteger(1), fw))
	require.Error(t, msgpack.Encode(msgpack.NewFloat32(1.0), fw))
	require.Error(t, msgpack.Encode(msgpack.NewFloat64(1.0), fw))
	require.Error(t, msgpack.Encode(msgpack.NewString("hello"), fw))
	require.Error(t, msgpack.Encode(msgpack.Binary{0x01}, fw))
	require.Error(t, msgpack.Encode([]byte{0x01}, fw))
	require.Error(t, msgpack.Encode(msgpack.Array{msgpack.NewNil()}, fw))
	require.Error(t, msgpack.Encode([]any{1}, fw))
	m := msgpack.NewMap()
	m.Set(msgpack.NewString("k"), msgpack.NewNil())
	require.Error(t, msgpack.Encode(m, fw))
	require.Error(t, msgpack.Encode(map[any]any{"k": 1}, fw))
	require.Error(t, msgpack.Encode([]int{1}, fw))
	require.Error(t, msgpack.Encode(map[string]int{"k": 1}, fw))
}

func TestDecodeExtensionTypes(t *testing.T) {
	t.Run("Ext8", func(t *testing.T) {
		in := bytes.NewBuffer([]byte{0xc7, 0x03, 0x09, 0x01, 0x02, 0x03})
		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		require.Equal(t, msgpack.ValueKindExtension, v.Kind())
		ev := v.(msgpack.Extension)
		require.Equal(t, int8(9), ev.Type)
		require.Equal(t, []byte{0x01, 0x02, 0x03}, ev.Data)
	})

	t.Run("Ext16", func(t *testing.T) {
		in := bytes.NewBuffer([]byte{0xc8, 0x00, 0x03, 0x0a, 0x01, 0x02, 0x03})
		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		ev := v.(msgpack.Extension)
		require.Equal(t, int8(10), ev.Type)
		require.Equal(t, []byte{0x01, 0x02, 0x03}, ev.Data)
	})

	t.Run("Ext32", func(t *testing.T) {
		in := bytes.NewBuffer([]byte{0xc9, 0x00, 0x00, 0x00, 0x03, 0x0b, 0x01, 0x02, 0x03})
		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		ev := v.(msgpack.Extension)
		require.Equal(t, int8(11), ev.Type)
		require.Equal(t, []byte{0x01, 0x02, 0x03}, ev.Data)
	})

	t.Run("FixExt8", func(t *testing.T) {
		in := bytes.NewBuffer([]byte{
			0xd7, 0x01, // fixext8: type=1, 8 data bytes
			0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		})
		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		ev := v.(msgpack.Extension)
		require.Equal(t, int8(1), ev.Type)
		require.Equal(t, []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, ev.Data)
	})

	t.Run("FixExt16", func(t *testing.T) {
		in := bytes.NewBuffer([]byte{
			0xd8, 0x02, // fixext16: type=2, 16 data bytes
			0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
			0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
		})
		v, err := msgpack.Decode(in)
		require.NoError(t, err)
		ev := v.(msgpack.Extension)
		require.Equal(t, int8(2), ev.Type)
		require.Len(t, ev.Data, 16)
	})
}

func TestDecodeIncompleteExtensionTypes(t *testing.T) {
	t.Run("IncompleteExt8", func(t *testing.T) {
		in := bytes.NewBuffer([]byte{0xc7, 0x03, 0x09, 0x01}) // missing 2 data bytes
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc7, 0x03}) // missing type and data
		_, err = msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc7}) // missing length, type and data
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("IncompleteExt16", func(t *testing.T) {
		in := bytes.NewBuffer([]byte{0xc8, 0x00, 0x03, 0x0a, 0x01}) // missing 2 data bytes
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc8, 0x00}) // incomplete length
		_, err = msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc8, 0x00, 0x03}) // missing type and data
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})

	t.Run("IncompleteExt32", func(t *testing.T) {
		in := bytes.NewBuffer([]byte{0xc9, 0x00, 0x00, 0x00, 0x03, 0x0b, 0x01}) // missing 2 data bytes
		_, err := msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc9, 0x00, 0x00}) // incomplete length
		_, err = msgpack.Decode(in)
		require.Error(t, err)

		in = bytes.NewBuffer([]byte{0xc9, 0x00, 0x00, 0x00, 0x03}) // missing type and data
		_, err = msgpack.Decode(in)
		require.Error(t, err)
	})
}

func TestBinaryTypeOperations(t *testing.T) {
	b := msgpack.NewBinary([]byte{0x01, 0x02, 0x03})
	require.Equal(t, msgpack.ValueKindBinary, b.Kind())
	require.Equal(t, "B010203", b.String())

	// Use Binary as a map key to exercise hash()
	m := msgpack.NewMap()
	m.Set(b, msgpack.NewString("value"))
	v, ok := m.Get(msgpack.NewBinary([]byte{0x01, 0x02, 0x03}))
	require.True(t, ok)
	require.Equal(t, msgpack.NewString("value"), v)
}

func TestArrayTypeOperations(t *testing.T) {
	a := msgpack.NewArray()
	require.Empty(t, a)

	a.Append(msgpack.NewInteger(1))
	a.Append(msgpack.NewString("foo"))
	require.Len(t, a, 2)
	require.Equal(t, msgpack.NewInteger(1), a[0])
	require.Equal(t, msgpack.NewString("foo"), a[1])
}

func TestExtensionAsMapKey(t *testing.T) {
	ext := msgpack.Extension{Type: 5, Data: []byte{0x01, 0x02}}
	m := msgpack.NewMap()
	m.Set(ext, msgpack.NewString("ext-value"))
	v, ok := m.Get(ext)
	require.True(t, ok)
	require.Equal(t, msgpack.NewString("ext-value"), v)
}

func TestMapIterateEarlyExit(t *testing.T) {
	m := msgpack.NewMap()
	m.Set(msgpack.NewString("key1"), msgpack.NewInteger(1))
	m.Set(msgpack.NewString("key2"), msgpack.NewInteger(2))
	m.Set(msgpack.NewString("key3"), msgpack.NewInteger(3))

	count := 0
	for range m.Iterate() {
		count++
		break // triggers yield returning false → exercises the early-exit path
	}
	require.Equal(t, 1, count)
}

// failAfterWriter is an io.Writer that accepts the given number of writes
// and fails all the subsequent ones.
type failAfterWriter struct{ writes int }

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.writes <= 0 {
		return 0, errors.New("write error")
	}
	w.writes--
	return len(p), nil
}

func TestEncodeNestedErrors(t *testing.T) {
	unsupported := struct{}{}

	t.Run("UnsupportedNestedElements", func(t *testing.T) {
		_, err := msgpack.Marshal([]any{unsupported})
		require.Error(t, err)
		_, err = msgpack.Marshal(map[any]any{unsupported: 1})
		require.Error(t, err)
		_, err = msgpack.Marshal(map[any]any{"k": unsupported})
		require.Error(t, err)
		_, err = msgpack.Marshal(map[string]any{"k": unsupported})
		require.Error(t, err)
		_, err = msgpack.Marshal(map[struct{}]int{{}: 1})
		require.Error(t, err)
		_, err = msgpack.Marshal([]struct{}{{}})
		require.Error(t, err)
	})

	t.Run("WriteErrorAfterHeader", func(t *testing.T) {
		require.Error(t, msgpack.Encode("hello", failWriter{}))
		require.Error(t, msgpack.Encode("hello", &failAfterWriter{writes: 1}))
		require.Error(t, msgpack.Encode(msgpack.NewString("hello"), &failAfterWriter{writes: 1}))
		require.Error(t, msgpack.Encode(msgpack.Binary{0x01}, &failAfterWriter{writes: 1}))
		require.Error(t, msgpack.Encode([]byte{0x01}, &failAfterWriter{writes: 1}))
		require.Error(t, msgpack.Encode(msgpack.Array{msgpack.NewNil()}, &failAfterWriter{writes: 1}))

		m := msgpack.NewMap()
		m.Set(msgpack.NewString("k"), msgpack.NewNil())
		require.Error(t, msgpack.Encode(m, &failAfterWriter{writes: 1})) // fails on key header
		require.Error(t, msgpack.Encode(m, &failAfterWriter{writes: 3})) // fails on value
	})
}

type myUint uint
type myString string
type myBytes []byte
type myInt int
type myInt8 int8
type myInt64 int64
type myUint8 uint8
type myUint64 uint64

func TestEncodeAliasAndPointerTypes(t *testing.T) {
	e, err := msgpack.Marshal(myUint(300))
	require.NoError(t, err)
	require.Equal(t, []byte{0xd1, 0x01, 0x2c}, e)

	e, err = msgpack.Marshal(myString("ab"))
	require.NoError(t, err)
	require.Equal(t, []byte{0xa2, 'a', 'b'}, e)

	s := "ab"
	e, err = msgpack.Marshal(&s)
	require.NoError(t, err)
	require.Equal(t, []byte{0xa2, 'a', 'b'}, e)

	b := []byte{0x01}
	e, err = msgpack.Marshal(&b)
	require.NoError(t, err)
	require.Equal(t, []byte{0xc4, 0x01, 0x01}, e)

	mb := myBytes{0x01, 0x02}
	e, err = msgpack.Marshal(mb)
	require.NoError(t, err)
	require.Equal(t, []byte{0xc4, 0x02, 0x01, 0x02}, e)
	e, err = msgpack.Marshal(&mb)
	require.NoError(t, err)
	require.Equal(t, []byte{0xc4, 0x02, 0x01, 0x02}, e)
	e, err = msgpack.Marshal(map[string]myBytes{"k": {0x03}})
	require.NoError(t, err)
	require.Equal(t, []byte{0x81, 0xa1, 'k', 0xc4, 0x01, 0x03}, e)

	i := 1
	_, err = msgpack.Marshal(&i)
	require.Error(t, err)
}

func TestEncodeNamedIntegerTypes(t *testing.T) {
	tests := []struct {
		val      any
		expected []byte
	}{
		{myInt(42), []byte{0x2a}},
		{myInt(-1), []byte{0xff}},
		{myInt8(-100), []byte{0xd0, 0x9c}},
		{myInt64(math.MinInt64), []byte{0xd3, 0x80, 0, 0, 0, 0, 0, 0, 0}},
		{myUint8(42), []byte{0x2a}},
		{myUint64(math.MaxUint64), []byte{0xcf, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
		{[]myInt{1, -1}, []byte{0x92, 0x01, 0xff}},
		{map[myInt8]myUint8{1: 2}, []byte{0x81, 0x01, 0x02}},
	}
	for _, tt := range tests {
		e, err := msgpack.Marshal(tt.val)
		require.NoError(t, err, "for value %v (%T)", tt.val, tt.val)
		require.Equal(t, tt.expected, e, "for value %v (%T)", tt.val, tt.val)
	}
}

func TestDecodeSingle(t *testing.T) {
	maxUint64 := uint64(math.MaxUint64)
	tests := []struct {
		name  string
		in    []byte
		kind  msgpack.ValueKind
		count uint
		value msgpack.Value
	}{
		{"PositiveFixInt", []byte{0x05}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(5)},
		{"FixMap", []byte{0x82}, msgpack.ValueKindMap, 2, nil},
		{"FixArray", []byte{0x93}, msgpack.ValueKindArray, 3, nil},
		{"FixStr", []byte{0xa3}, msgpack.ValueKindString, 3, nil},
		{"Nil", []byte{0xc0}, msgpack.ValueKindNil, 0, msgpack.NewNil()},
		{"False", []byte{0xc2}, msgpack.ValueKindBool, 0, msgpack.False},
		{"True", []byte{0xc3}, msgpack.ValueKindBool, 0, msgpack.True},
		{"Bin8", []byte{0xc4, 0x05}, msgpack.ValueKindBinary, 5, nil},
		{"Bin16", []byte{0xc5, 0x01, 0x00}, msgpack.ValueKindBinary, 256, nil},
		{"Bin32", []byte{0xc6, 0x00, 0x01, 0x00, 0x00}, msgpack.ValueKindBinary, 65536, nil},
		{"Ext8", []byte{0xc7, 0x03}, msgpack.ValueKindExtension, 4, nil},
		{"Ext16", []byte{0xc8, 0x01, 0x00}, msgpack.ValueKindExtension, 257, nil},
		{"Ext32", []byte{0xc9, 0x00, 0x01, 0x00, 0x00}, msgpack.ValueKindExtension, 65537, nil},
		{"Float32", []byte{0xca, 0x3f, 0xc0, 0x00, 0x00}, msgpack.ValueKindFloat32, 0, msgpack.NewFloat32(1.5)},
		{"Float64", []byte{0xcb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0}, msgpack.ValueKindFloat64, 0, msgpack.NewFloat64(1.5)},
		{"Uint8", []byte{0xcc, 0xff}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(255)},
		{"Uint16", []byte{0xcd, 0x01, 0x00}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(256)},
		{"Uint32", []byte{0xce, 0x00, 0x01, 0x00, 0x00}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(65536)},
		{"Uint64", []byte{0xcf, 0, 0, 0, 0, 0, 0, 0, 0x01}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(1)},
		{"Uint64Max", []byte{0xcf, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(maxUint64)},
		{"Int8", []byte{0xd0, 0xff}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(-1)},
		{"Int16", []byte{0xd1, 0xff, 0xfe}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(-2)},
		{"Int32", []byte{0xd2, 0xff, 0xff, 0xff, 0xfd}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(-3)},
		{"Int64", []byte{0xd3, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfc}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(-4)},
		{"FixExt1", []byte{0xd4}, msgpack.ValueKindExtension, 2, nil},
		{"FixExt2", []byte{0xd5}, msgpack.ValueKindExtension, 3, nil},
		{"FixExt4", []byte{0xd6}, msgpack.ValueKindExtension, 5, nil},
		{"FixExt8", []byte{0xd7}, msgpack.ValueKindExtension, 9, nil},
		{"FixExt16", []byte{0xd8}, msgpack.ValueKindExtension, 17, nil},
		{"Str8", []byte{0xd9, 0x20}, msgpack.ValueKindString, 32, nil},
		{"Str16", []byte{0xda, 0x01, 0x00}, msgpack.ValueKindString, 256, nil},
		{"Str32", []byte{0xdb, 0x00, 0x01, 0x00, 0x00}, msgpack.ValueKindString, 65536, nil},
		{"Array16", []byte{0xdc, 0x00, 0x10}, msgpack.ValueKindArray, 16, nil},
		{"Array32", []byte{0xdd, 0x00, 0x01, 0x00, 0x00}, msgpack.ValueKindArray, 65536, nil},
		{"Map16", []byte{0xde, 0x00, 0x10}, msgpack.ValueKindMap, 16, nil},
		{"Map32", []byte{0xdf, 0x00, 0x01, 0x00, 0x00}, msgpack.ValueKindMap, 65536, nil},
		{"NegativeFixInt", []byte{0xe0}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(-32)},
		{"NegativeFixIntMinusOne", []byte{0xff}, msgpack.ValueKindInteger, 0, msgpack.NewInteger(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Append a sentinel byte to check that DecodeSingle doesn't over-read
			in := bytes.NewBuffer(append(append([]byte{}, tt.in...), 0xaa))
			readBytes, kind, count, value, err := msgpack.DecodeSingle(in)
			require.NoError(t, err)
			require.Equal(t, tt.in, readBytes)
			require.Equal(t, tt.kind, kind)
			require.Equal(t, tt.count, count)
			require.Equal(t, tt.value, value)
			require.Equal(t, []byte{0xaa}, in.Bytes())
		})
	}

	t.Run("Reserved", func(t *testing.T) {
		readBytes, _, _, _, err := msgpack.DecodeSingle(bytes.NewBuffer([]byte{0xc1}))
		require.Error(t, err)
		require.Equal(t, []byte{0xc1}, readBytes)
	})

	t.Run("Empty", func(t *testing.T) {
		readBytes, _, _, _, err := msgpack.DecodeSingle(bytes.NewBuffer(nil))
		require.ErrorIs(t, err, io.EOF)
		require.Empty(t, readBytes)
	})

	t.Run("Truncated", func(t *testing.T) {
		for _, tt := range tests {
			for l := 1; l < len(tt.in); l++ {
				readBytes, _, _, _, err := msgpack.DecodeSingle(bytes.NewBuffer(tt.in[:l]))
				require.Error(t, err, "%s truncated at %d", tt.name, l)
				// Only the type byte has been consumed successfully
				require.Equal(t, tt.in[:1], readBytes, "%s truncated at %d", tt.name, l)
			}
		}
	})
}

func TestDecodeSingleThenSkip(t *testing.T) {
	// This is the "peek and skip" pattern used by the RPC layer
	for _, msg := range completeMessages() {
		in := bytes.NewBuffer(append(append([]byte{}, msg...), 0xaa))
		peek, _, _, _, err := msgpack.DecodeSingle(in)
		require.NoError(t, err)
		require.NoError(t, msgpack.Skip(io.MultiReader(bytes.NewBuffer(peek), in)), "%x", msg)
		require.Equal(t, []byte{0xaa}, in.Bytes(), "%x", msg)
	}
}

// completeMessages returns a set of well-formed messages covering all the
// MessagePack types.
func completeMessages() [][]byte {
	longString, err := msgpack.Marshal(strings.Repeat("x", 3000))
	if err != nil {
		panic(err)
	}
	return [][]byte{
		{0x05},
		{0xe0},
		{0xc0},
		{0xc2},
		{0xc3},
		{0xc4, 0x02, 0x01, 0x02},
		{0xc5, 0x00, 0x02, 0x01, 0x02},
		{0xc6, 0x00, 0x00, 0x00, 0x02, 0x01, 0x02},
		{0xc7, 0x02, 0x09, 0x01, 0x02},
		{0xc8, 0x00, 0x02, 0x09, 0x01, 0x02},
		{0xc9, 0x00, 0x00, 0x00, 0x02, 0x09, 0x01, 0x02},
		{0xca, 0x3f, 0xc0, 0x00, 0x00},
		{0xcb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0},
		{0xcc, 0x01},
		{0xcd, 0x00, 0x01},
		{0xce, 0x00, 0x00, 0x00, 0x01},
		{0xcf, 0, 0, 0, 0, 0, 0, 0, 0x01},
		{0xd0, 0x01},
		{0xd1, 0x00, 0x01},
		{0xd2, 0x00, 0x00, 0x00, 0x01},
		{0xd3, 0, 0, 0, 0, 0, 0, 0, 0x01},
		{0xd4, 0x01, 0x01},
		{0xd5, 0x01, 0x01, 0x02},
		{0xd6, 0x01, 0x01, 0x02, 0x03, 0x04},
		append([]byte{0xd7, 0x01}, bytes.Repeat([]byte{0x01}, 8)...),
		append([]byte{0xd8, 0x01}, bytes.Repeat([]byte{0x01}, 16)...),
		{0xa2, 'h', 'i'},
		{0xd9, 0x02, 'h', 'i'},
		{0xda, 0x00, 0x02, 'h', 'i'},
		{0xdb, 0x00, 0x00, 0x00, 0x02, 'h', 'i'},
		{0x92, 0x01, 0xa1, 'a'},
		{0xdc, 0x00, 0x02, 0x01, 0x02},
		{0xdd, 0x00, 0x00, 0x00, 0x02, 0x01, 0x02},
		{0x81, 0xa1, 'k', 0x91, 0xc0},
		{0xde, 0x00, 0x01, 0x01, 0x02},
		{0xdf, 0x00, 0x00, 0x00, 0x01, 0x01, 0x02},
		longString, // str16 bigger than the internal skip buffer
	}
}

func TestSkipAndDecodeRaw(t *testing.T) {
	t.Run("Complete", func(t *testing.T) {
		for _, msg := range completeMessages() {
			in := bytes.NewBuffer(append(append([]byte{}, msg...), 0xaa))
			require.NoError(t, msgpack.Skip(in), "%x", msg)
			require.Equal(t, []byte{0xaa}, in.Bytes(), "%x", msg)

			in = bytes.NewBuffer(append(append([]byte{}, msg...), 0xaa))
			raw, err := msgpack.DecodeRaw(in)
			require.NoError(t, err, "%x", msg)
			require.Equal(t, []byte{0xaa}, in.Bytes(), "%x", msg)
			encoded, err := msgpack.Marshal(raw)
			require.NoError(t, err)
			require.Equal(t, msg, encoded)
		}
	})

	t.Run("Truncated", func(t *testing.T) {
		for _, msg := range completeMessages() {
			for l := range len(msg) {
				require.Error(t, msgpack.Skip(bytes.NewBuffer(msg[:l])), "%x truncated at %d", msg, l)
				_, err := msgpack.DecodeRaw(bytes.NewBuffer(msg[:l]))
				require.Error(t, err, "%x truncated at %d", msg, l)
			}
		}
	})

	t.Run("Reserved", func(t *testing.T) {
		require.Error(t, msgpack.Skip(bytes.NewBuffer([]byte{0xc1})))
		require.Error(t, msgpack.Skip(bytes.NewBuffer([]byte{0x91, 0xc1})))
		_, err := msgpack.DecodeRaw(bytes.NewBuffer([]byte{0x81, 0x01, 0xc1}))
		require.Error(t, err)
	})
}

func TestRawMessage(t *testing.T) {
	raw := msgpack.NewRawMessage(0x93, 0x01, 0x02, 0x03)
	require.Equal(t, msgpack.ValueKindRaw, raw.Kind())
	require.Equal(t, "Raw(93010203)", raw.String())
	require.Equal(t, msgpack.Array{msgpack.NewInteger(1), msgpack.NewInteger(2), msgpack.NewInteger(3)}, raw.Decode())
	require.Equal(t, []any{1, 2, 3}, raw.Unmarshal())

	// Invalid content
	invalid := msgpack.NewRawMessage(0xc1)
	require.Nil(t, invalid.Decode())
	require.Nil(t, invalid.Unmarshal())

	// RawMessage is embedded verbatim when encoded
	e, err := msgpack.Marshal([]any{msgpack.NewRawMessage(0xc3), 1})
	require.NoError(t, err)
	require.Equal(t, []byte{0x92, 0xc3, 0x01}, e)
	require.Error(t, msgpack.Encode(raw, failWriter{}))

	// RawMessage can't be used as map key
	require.Panics(t, func() {
		m := msgpack.NewMap()
		m.Set(raw, msgpack.NewNil())
	})
}

func TestUnmarshal(t *testing.T) {
	unmarshal := func(t *testing.T, in []byte) any {
		v, err := msgpack.Unmarshal(bytes.NewBuffer(in))
		require.NoError(t, err)
		return v
	}

	require.Equal(t, 5, unmarshal(t, []byte{0x05}))
	require.Equal(t, -1, unmarshal(t, []byte{0xff}))
	if math.MaxUint == math.MaxUint64 {
		require.Equal(t, uint(math.MaxUint), unmarshal(t, []byte{0xcf, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}))
		require.Equal(t, math.MinInt, unmarshal(t, []byte{0xd3, 0x80, 0, 0, 0, 0, 0, 0, 0}))
	} else {
		require.Equal(t, uint64(math.MaxUint64), unmarshal(t, []byte{0xcf, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}))
		require.Equal(t, int64(math.MinInt64), unmarshal(t, []byte{0xd3, 0x80, 0, 0, 0, 0, 0, 0, 0}))
	}
	require.Equal(t, "hi", unmarshal(t, []byte{0xa2, 'h', 'i'}))
	require.Equal(t, true, unmarshal(t, []byte{0xc3}))
	require.Equal(t, false, unmarshal(t, []byte{0xc2}))
	require.Nil(t, unmarshal(t, []byte{0xc0}))
	require.Equal(t, []byte{0x01, 0x02}, unmarshal(t, []byte{0xc4, 0x02, 0x01, 0x02}))
	require.Equal(t, float32(1.5), unmarshal(t, []byte{0xca, 0x3f, 0xc0, 0x00, 0x00}))
	require.Equal(t, float64(1.5), unmarshal(t, []byte{0xcb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0}))
	require.Equal(t,
		msgpack.Extension{Type: 1, Data: []byte{0x02}},
		unmarshal(t, []byte{0xd4, 0x01, 0x02}))
	require.Equal(t,
		[]any{1, "a", []any{nil}},
		unmarshal(t, []byte{0x93, 0x01, 0xa1, 'a', 0x91, 0xc0}))
	require.Equal(t,
		map[any]any{"a": 1, 2: []any{true}},
		unmarshal(t, []byte{0x82, 0xa1, 'a', 0x01, 0x02, 0x91, 0xc3}))

	// Maps with keys not representable as Go map keys are returned as msgpack.Map
	for _, in := range [][]byte{
		{0x81, 0x90, 0x01},             // array key
		{0x81, 0x80, 0x01},             // map key
		{0x81, 0xd4, 0x01, 0x02, 0x01}, // extension key
	} {
		v := unmarshal(t, in)
		m, ok := v.(msgpack.Map)
		require.True(t, ok, "%x", in)
		require.Equal(t, 1, m.Len())
	}

	// Errors
	_, err := msgpack.Unmarshal(bytes.NewBuffer(nil))
	require.Error(t, err)
	_, err = msgpack.Unmarshal(bytes.NewBuffer([]byte{0x92, 0x01}))
	require.Error(t, err)
}

func TestIntegerUint(t *testing.T) {
	v, ok := msgpack.NewInteger(5).Uint()
	require.True(t, ok)
	require.Equal(t, uint(5), v)

	_, ok = msgpack.NewInteger(-1).Uint()
	require.False(t, ok)

	v, ok = msgpack.NewInteger(uint64(math.MaxUint)).Uint()
	require.True(t, ok)
	require.Equal(t, uint(math.MaxUint), v)

	_, ok = msgpack.NewInteger(uint64(math.MaxUint64)).Uint()
	require.Equal(t, math.MaxUint == math.MaxUint64, ok)
}

func TestToInt(t *testing.T) {
	tests := []struct {
		in  any
		out int
		ok  bool
	}{
		{msgpack.NewInteger(-5), -5, true},
		{msgpack.NewInteger(uint64(math.MaxUint64)), -1, false},
		{int(-1), -1, true},
		{int8(-2), -2, true},
		{int16(-3), -3, true},
		{int32(-4), -4, true},
		{int64(-5), -5, true},
		{uint(6), 6, true},
		{uint(math.MaxUint), -1, false},
		{uint8(7), 7, true},
		{uint16(8), 8, true},
		{uint32(math.MaxInt32), math.MaxInt32, true},
		{uint64(10), 10, true},
		{uint64(math.MaxUint64), -1, false},
		{"5", 0, false},
		{nil, 0, false},
		{1.0, 0, false},
	}
	for _, tt := range tests {
		out, ok := msgpack.ToInt(tt.in)
		require.Equal(t, tt.ok, ok, "%T(%v)", tt.in, tt.in)
		if ok {
			require.Equal(t, tt.out, out, "%T(%v)", tt.in, tt.in)
		}
	}
}

func TestToUint(t *testing.T) {
	tests := []struct {
		in  any
		out uint
		ok  bool
	}{
		{msgpack.NewInteger(5), 5, true},
		{msgpack.NewInteger(-5), 0, false},
		{uint(1), 1, true},
		{uint8(2), 2, true},
		{uint16(3), 3, true},
		{uint32(4), 4, true},
		{uint64(math.MaxUint), math.MaxUint, true},
		{int(6), 6, true},
		{int(-6), 0, false},
		{int8(7), 7, true},
		{int8(-7), 0, false},
		{int16(8), 8, true},
		{int16(-8), 0, false},
		{int32(9), 9, true},
		{int32(-9), 0, false},
		{int64(10), 10, true},
		{int64(-10), 0, false},
		{"5", 0, false},
		{nil, 0, false},
	}
	for _, tt := range tests {
		out, ok := msgpack.ToUint(tt.in)
		require.Equal(t, tt.ok, ok, "%T(%v)", tt.in, tt.in)
		if ok {
			require.Equal(t, tt.out, out, "%T(%v)", tt.in, tt.in)
		}
	}
}

func TestIntegerConversionsOutOfIntRange(t *testing.T) {
	// These values fit in an int (or uint) only on 64-bit platforms, so the
	// conversions must fail on 32-bit platforms instead of truncating.
	is64bit := math.MaxInt > math.MaxInt32

	_, ok := msgpack.NewInteger(int64(math.MaxInt32) + 1).Int()
	require.Equal(t, is64bit, ok)
	_, ok = msgpack.NewInteger(int64(math.MinInt32) - 1).Int()
	require.Equal(t, is64bit, ok)
	_, ok = msgpack.ToInt(int64(math.MaxInt32) + 1)
	require.Equal(t, is64bit, ok)
	_, ok = msgpack.ToInt(int64(math.MinInt32) - 1)
	require.Equal(t, is64bit, ok)
	u32 := uint32(math.MaxUint32)
	out, ok := msgpack.ToInt(u32)
	require.Equal(t, is64bit, ok)
	if ok {
		require.Equal(t, int(u32), out)
	}

	_, ok = msgpack.NewInteger(int64(math.MaxUint32) + 1).Uint()
	require.Equal(t, is64bit, ok)
	_, ok = msgpack.ToUint(int64(math.MaxUint32) + 1)
	require.Equal(t, is64bit, ok)
	_, ok = msgpack.ToUint(uint64(math.MaxUint32) + 1)
	require.Equal(t, is64bit, ok)
}

func TestToString(t *testing.T) {
	s, ok := msgpack.ToString(msgpack.NewString("a"))
	require.True(t, ok)
	require.Equal(t, "a", s)

	s, ok = msgpack.ToString("b")
	require.True(t, ok)
	require.Equal(t, "b", s)

	_, ok = msgpack.ToString(5)
	require.False(t, ok)
	_, ok = msgpack.ToString([]byte("c"))
	require.False(t, ok)
}
