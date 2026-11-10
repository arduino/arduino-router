// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// signExtend interprets the lowest n bits of v as a two's complement signed
// integer of n bits, and returns it as an int64.
func signExtend(v uint64, n uint) int64 {
	shift := 64 - n
	return int64(v<<shift) >> shift //nolint:gosec // intentional two's complement reinterpretation
}

func Decode(in io.Reader) (Value, error) {
	read8 := func() (uint64, error) {
		var buf [1]byte
		if _, err := io.ReadFull(in, buf[:]); err != nil {
			return 0, err
		}
		return uint64(buf[0]), nil
	}
	read16 := func() (uint64, error) {
		var buf [2]byte
		if _, err := io.ReadFull(in, buf[:]); err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint16(buf[:])), nil
	}
	read32 := func() (uint64, error) {
		var buf [4]byte
		if _, err := io.ReadFull(in, buf[:]); err != nil {
			return 0, err
		}
		return uint64(binary.BigEndian.Uint32(buf[:])), nil
	}
	read64 := func() (uint64, error) {
		var buf [8]byte
		if _, err := io.ReadFull(in, buf[:]); err != nil {
			return 0, err
		}
		return binary.BigEndian.Uint64(buf[:]), nil
	}
	readN := func(n int) ([]byte, error) {
		buf := make([]byte, n)
		if _, err := io.ReadFull(in, buf); err != nil {
			return nil, err
		}
		return buf, nil
	}
	readArray := func(l int) (Array, error) {
		a := make(Array, l)
		for i := range l {
			if v, err := Decode(in); err != nil {
				return nil, err
			} else {
				a[i] = v
			}
		}
		return a, nil
	}
	readMap := func(l int) (Map, error) {
		m := NewMap()
		for range l {
			if k, err := Decode(in); err != nil {
				return Map{}, err
			} else if v, err := Decode(in); err != nil {
				return Map{}, err
			} else {
				m.Set(k, v)
			}
		}
		return m, nil
	}

	k, err := read8()
	if err != nil {
		return nil, err
	}
	switch {
	// 00..7F positive fixint
	case k <= 0x7f:
		return Integer{i64: int64(k)}, nil
	// 80..8F fixmap
	case k >= 0x80 && k <= 0x8f:
		return readMap(int(k & 0x0f))
	// 90..9F fixarray
	case k >= 0x90 && k <= 0x9f:
		return readArray(int(k & 0x0f))
	// A0..BF fixstr
	case k >= 0xa0 && k <= 0xbf:
		if buf, err := readN(int(k & 0x1f)); err != nil {
			return nil, err
		} else {
			return String(buf), nil
		}
	// C0 nil
	case k == 0xc0:
		return Nil{}, nil
	// C1
	case k == 0xc1:
		// reserved
	// C2 false
	case k == 0xc2:
		return False, nil
	// C3 true
	case k == 0xc3:
		return True, nil
	// C4 bin 8
	case k == 0xc4:
		if len8, err := read8(); err != nil {
			return nil, err
		} else if buf, err := readN(int(len8)); err != nil {
			return nil, err
		} else {
			return Binary(buf), nil
		}
	// C5 bin 16
	case k == 0xc5:
		if len16, err := read16(); err != nil {
			return nil, err
		} else if buf, err := readN(int(len16)); err != nil {
			return nil, err
		} else {
			return Binary(buf), nil
		}
	// C6 bin 32
	case k == 0xc6:
		if l, err := read32(); err != nil {
			return nil, err
		} else if buf, err := readN(int(l)); err != nil {
			return nil, err
		} else {
			return Binary(buf), nil
		}
	// C7 ext 8
	case k == 0xc7:
		if l, err := read8(); err != nil {
			return nil, err
		} else if t, err := read8(); err != nil {
			return nil, err
		} else if data, err := readN(int(l)); err != nil {
			return nil, err
		} else {
			return Extension{Type: int8(t), Data: data}, nil //nolint:gosec // ext type is a signed byte
		}
	// C8 ext 16
	case k == 0xc8:
		if l, err := read16(); err != nil {
			return nil, err
		} else if t, err := read8(); err != nil {
			return nil, err
		} else if data, err := readN(int(l)); err != nil {
			return nil, err
		} else {
			return Extension{Type: int8(t), Data: data}, nil //nolint:gosec // ext type is a signed byte
		}
	// C9 ext 32
	case k == 0xc9:
		if l, err := read32(); err != nil {
			return nil, err
		} else if t, err := read8(); err != nil {
			return nil, err
		} else if data, err := readN(int(l)); err != nil {
			return nil, err
		} else {
			return Extension{Type: int8(t), Data: data}, nil //nolint:gosec // ext type is a signed byte
		}
	// CA float 32
	case k == 0xca:
		if bits, err := read32(); err != nil {
			return nil, err
		} else {
			return Float32(math.Float32frombits(uint32(bits))), nil //nolint:gosec // read32 returns 32 bits
		}
	// CB float 64
	case k == 0xcb:
		if bits, err := read64(); err != nil {
			return nil, err
		} else {
			return Float64(math.Float64frombits(bits)), nil
		}
	// CC uint 8
	case k == 0xcc:
		if v, err := read8(); err != nil {
			return nil, err
		} else {
			return NewInteger(v), nil
		}
	// CD uint 16
	case k == 0xcd:
		if v, err := read16(); err != nil {
			return nil, err
		} else {
			return NewInteger(v), nil
		}
	// CE uint 32
	case k == 0xce:
		if val, err := read32(); err != nil {
			return nil, err
		} else {
			return NewInteger(val), nil
		}
	// CF uint 64
	case k == 0xcf:
		if val, err := read64(); err != nil {
			return nil, err
		} else {
			return NewInteger(val), nil
		}
	// D0 int 8
	case k == 0xd0:
		if v, err := read8(); err != nil {
			return nil, err
		} else {
			return Integer{i64: signExtend(v, 8)}, nil
		}
	// D1 int 16
	case k == 0xd1:
		if val, err := read16(); err != nil {
			return nil, err
		} else {
			return Integer{i64: signExtend(val, 16)}, nil
		}
	// D2 int 32
	case k == 0xd2:
		if val, err := read32(); err != nil {
			return nil, err
		} else {
			return Integer{i64: signExtend(val, 32)}, nil
		}
	// D3 int 64
	case k == 0xd3:
		if val, err := read64(); err != nil {
			return nil, err
		} else {
			return Integer{i64: signExtend(val, 64)}, nil
		}
	// D4...D8 fixext 1/2/4/8/16
	case k >= 0xd4 && k <= 0xd8:
		l := 1 << (k - 0xd4)
		if t, err := read8(); err != nil {
			return nil, err
		} else if data, err := readN(l); err != nil {
			return nil, err
		} else {
			return Extension{Type: int8(t), Data: data}, nil //nolint:gosec // ext type is a signed byte
		}
	// D9 str 8
	case k == 0xd9:
		if len8, err := read8(); err != nil {
			return nil, err
		} else if buf, err := readN(int(len8)); err != nil {
			return nil, err
		} else {
			return String(buf), nil
		}
	// DA str 16
	case k == 0xda:
		if len16, err := read16(); err != nil {
			return nil, err
		} else if buf, err := readN(int(len16)); err != nil {
			return nil, err
		} else {
			return String(buf), nil
		}
	// DB str 32
	case k == 0xdb:
		if l, err := read32(); err != nil {
			return nil, err
		} else if buf, err := readN(int(l)); err != nil {
			return nil, err
		} else {
			return String(buf), nil
		}
	// DC array 16
	case k == 0xdc:
		if length, err := read16(); err != nil {
			return nil, err
		} else {
			return readArray(int(length))
		}
	// DD array 32
	case k == 0xdd:
		if length, err := read32(); err != nil {
			return nil, err
		} else {
			return readArray(int(length))
		}
	// DE map 16
	case k == 0xde:
		if length, err := read16(); err != nil {
			return nil, err
		} else {
			return readMap(int(length))
		}
	// DF map 32
	case k == 0xdf:
		if length, err := read32(); err != nil {
			return nil, err
		} else {
			return readMap(int(length))
		}
	// E0..FF negative fixint
	case k >= 0xe0:
		return Integer{i64: signExtend(k, 8)}, nil
	}
	return nil, fmt.Errorf("unsupported msgpack type 0x%02x", k)
}

// DecodeSingle decodes a MessagePack token from the given reader.
// It returns:
// - the raw bytes of the token,
// - the value kind,
// - the number of elements following (for arrays and maps) or the length of the element (for strings and binary data),
// - the decoded value (if the literal is complete),
// - an error if any occurred.
func DecodeSingle(in io.Reader) (readBytes []byte, kind ValueKind, elementsFollowing uint, decodedValue Value, err error) {
	buff := make([]byte, 9)
	buffRead := 0
	defer func() {
		readBytes = buff[:buffRead]
	}()
	read8 := func() (uint64, error) {
		if _, err := io.ReadFull(in, buff[buffRead:buffRead+1]); err != nil {
			return 0, err
		}
		res := buff[buffRead]
		buffRead += 1
		return uint64(res), nil
	}
	read16 := func() (uint64, error) {
		if _, err := io.ReadFull(in, buff[buffRead:buffRead+2]); err != nil {
			return 0, err
		}
		res := binary.BigEndian.Uint16(buff[buffRead : buffRead+2])
		buffRead += 2
		return uint64(res), nil
	}
	read32 := func() (uint64, error) {
		if _, err := io.ReadFull(in, buff[buffRead:buffRead+4]); err != nil {
			return 0, err
		}
		res := binary.BigEndian.Uint32(buff[buffRead : buffRead+4])
		buffRead += 4
		return uint64(res), nil
	}
	read64 := func() (uint64, error) {
		if _, err := io.ReadFull(in, buff[buffRead:buffRead+8]); err != nil {
			return 0, err
		}
		res := binary.BigEndian.Uint64(buff[buffRead : buffRead+8])
		buffRead += 8
		return res, nil
	}

	k, err := read8()
	if err != nil {
		return nil, 0, 0, nil, err
	}
	switch {
	// 00..7F positive fixint
	case k <= 0x7f:
		return nil, ValueKindInteger, 0, Integer{i64: int64(k)}, nil
	// 80..8F fixmap
	case k >= 0x80 && k <= 0x8f:
		return nil, ValueKindMap, uint(k & 0x0f), nil, nil
	// 90..9F fixarray
	case k >= 0x90 && k <= 0x9f:
		return nil, ValueKindArray, uint(k & 0x0f), nil, nil
	// A0..BF fixstr
	case k >= 0xa0 && k <= 0xbf:
		return nil, ValueKindString, uint(k & 0x1f), nil, nil
	// C0 nil
	case k == 0xc0:
		return nil, ValueKindNil, 0, Nil{}, nil
	// C1
	case k == 0xc1:
		// reserved
		return nil, 0, 0, nil, fmt.Errorf("reserved byte 0xc1 encountered")
	// C2 false
	case k == 0xc2:
		return nil, ValueKindBool, 0, False, nil
	// C3 true
	case k == 0xc3:
		return nil, ValueKindBool, 0, True, nil
	// C4 bin 8
	case k == 0xc4:
		if len8, err := read8(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindBinary, uint(len8), nil, nil
		}
	// C5 bin 16
	case k == 0xc5:
		if len16, err := read16(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindBinary, uint(len16), nil, nil
		}
	// C6 bin 32
	case k == 0xc6:
		if l, err := read32(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindBinary, uint(l), nil, nil
		}
	// C7 ext 8
	case k == 0xc7:
		if l, err := read8(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindExtension, uint(l + 1), nil, nil
		}
	// C8 ext 16
	case k == 0xc8:
		if l, err := read16(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindExtension, uint(l + 1), nil, nil
		}
	// C9 ext 32
	case k == 0xc9:
		if l, err := read32(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindExtension, uint(l + 1), nil, nil
		}
	// CA float 32
	case k == 0xca:
		if bits, err := read32(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindFloat32, 0, Float32(math.Float32frombits(uint32(bits))), nil //nolint:gosec // read32 returns 32 bits
		}
	// CB float 64
	case k == 0xcb:
		if bits, err := read64(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindFloat64, 0, Float64(math.Float64frombits(bits)), nil
		}
	// CC uint 8
	case k == 0xcc:
		if v, err := read8(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindInteger, 0, NewInteger(v), nil
		}
	// CD uint 16
	case k == 0xcd:
		if v, err := read16(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindInteger, 0, NewInteger(v), nil
		}
	// CE uint 32
	case k == 0xce:
		if val, err := read32(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindInteger, 0, NewInteger(val), nil
		}
	// CF uint 64
	case k == 0xcf:
		if val, err := read64(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindInteger, 0, NewInteger(val), nil
		}
	// D0 int 8
	case k == 0xd0:
		if v, err := read8(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindInteger, 0, Integer{i64: signExtend(v, 8)}, nil
		}
	// D1 int 16
	case k == 0xd1:
		if val, err := read16(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindInteger, 0, Integer{i64: signExtend(val, 16)}, nil
		}
	// D2 int 32
	case k == 0xd2:
		if val, err := read32(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindInteger, 0, Integer{i64: signExtend(val, 32)}, nil
		}
	// D3 int 64
	case k == 0xd3:
		if val, err := read64(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindInteger, 0, Integer{i64: signExtend(val, 64)}, nil
		}
	// D4...D8 fixext 1/2/4/8/16
	case k >= 0xd4 && k <= 0xd8:
		l := 1 << (k - 0xd4)
		return nil, ValueKindExtension, uint(l + 1), nil, nil
	// D9 str 8
	case k == 0xd9:
		if len8, err := read8(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindString, uint(len8), nil, nil
		}
	// DA str 16
	case k == 0xda:
		if len16, err := read16(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindString, uint(len16), nil, nil
		}
	// DB str 32
	case k == 0xdb:
		if l, err := read32(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindString, uint(l), nil, nil
		}
	// DC array 16
	case k == 0xdc:
		if length, err := read16(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindArray, uint(length), nil, nil
		}
	// DD array 32
	case k == 0xdd:
		if length, err := read32(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindArray, uint(length), nil, nil
		}
	// DE map 16
	case k == 0xde:
		if length, err := read16(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindMap, uint(length), nil, nil
		}
	// DF map 32
	case k == 0xdf:
		if length, err := read32(); err != nil {
			return nil, 0, 0, nil, err
		} else {
			return nil, ValueKindMap, uint(length), nil, nil
		}
	// E0..FF negative fixint
	case k >= 0xe0:
		return nil, ValueKindInteger, 0, Integer{i64: signExtend(k, 8)}, nil
	}
	return nil, 0, 0, nil, fmt.Errorf("unsupported msgpack type 0x%02x", k)
}

func DecodeRaw(in io.Reader) (RawMessage, error) {
	rawBuff := bytes.NewBuffer(nil)
	err := decodeInto(in, rawBuff)
	if err != nil {
		return RawMessage{}, err
	}
	return RawMessage{data: rawBuff.Bytes()}, nil
}

func Skip(in io.Reader) error {
	return decodeInto(in, io.Discard)
}

func decodeInto(in io.Reader, out io.Writer) error {
	read8 := func() (uint64, error) {
		var buf [1]byte
		if _, err := io.ReadFull(in, buf[:]); err != nil {
			return 0, err
		}
		_, _ = out.Write(buf[:])
		return uint64(buf[0]), nil
	}
	read16 := func() (uint64, error) {
		var buf [2]byte
		if _, err := io.ReadFull(in, buf[:]); err != nil {
			return 0, err
		}
		_, _ = out.Write(buf[:])
		return uint64(binary.BigEndian.Uint16(buf[:])), nil
	}
	read32 := func() (uint64, error) {
		var buf [4]byte
		if _, err := io.ReadFull(in, buf[:]); err != nil {
			return 0, err
		}
		_, _ = out.Write(buf[:])
		return uint64(binary.BigEndian.Uint32(buf[:])), nil
	}
	skipBytes := func(n int) error {
		var buf [1024]byte
		for n > 0 {
			if r, err := io.ReadFull(in, buf[:min(len(buf), n)]); err != nil {
				return err
			} else {
				_, _ = out.Write(buf[:r])
				n -= r
			}
		}
		return nil
	}
	skip := func(n int) error {
		for range n {
			if err := decodeInto(in, out); err != nil {
				return err
			}
		}
		return nil
	}

	k, err := read8()
	if err != nil {
		return err
	}
	switch {
	// 00..7F positive fixint
	case k <= 0x7f:
		return nil
	// 80..8F fixmap
	case k >= 0x80 && k <= 0x8f:
		return skip(int(k&0x0f) * 2)
	// 90..9F fixarray
	case k >= 0x90 && k <= 0x9f:
		return skip(int(k & 0x0f))
	// A0..BF fixstr
	case k >= 0xa0 && k <= 0xbf:
		return skipBytes(int(k & 0x1f))
	// C0 nil
	case k == 0xc0:
		return nil
	// C1
	case k == 0xc1:
		return fmt.Errorf("invalid msgpack type 0xC1")
	// C2 false
	case k == 0xc2:
		return nil
	// C3 true
	case k == 0xc3:
		return nil
	// C4 bin 8
	case k == 0xc4:
		if len8, err := read8(); err != nil {
			return err
		} else {
			return skipBytes(int(len8))
		}
	// C5 bin 16
	case k == 0xc5:
		if len16, err := read16(); err != nil {
			return err
		} else {
			return skipBytes(int(len16))
		}
	// C6 bin 32
	case k == 0xc6:
		if l, err := read32(); err != nil {
			return err
		} else {
			return skipBytes(int(l))
		}
	// C7 ext 8
	case k == 0xc7:
		if l, err := read8(); err != nil {
			return err
		} else {
			return skipBytes(int(l + 1))
		}
	// C8 ext 16
	case k == 0xc8:
		if l, err := read16(); err != nil {
			return err
		} else {
			return skipBytes(int(l) + 1)
		}
	// C9 ext 32
	case k == 0xc9:
		if l, err := read32(); err != nil {
			return err
		} else {
			return skipBytes(int(l) + 1)
		}
	// CA float 32
	case k == 0xca:
		return skipBytes(4)
	// CB float 64
	case k == 0xcb:
		return skipBytes(8)
	// CC uint 8
	case k == 0xcc:
		return skipBytes(1)
	// CD uint 16
	case k == 0xcd:
		return skipBytes(2)
	// CE uint 32
	case k == 0xce:
		return skipBytes(4)
	// CF uint 64
	case k == 0xcf:
		return skipBytes(8)
	// D0 int 8
	case k == 0xd0:
		return skipBytes(1)
	// D1 int 16
	case k == 0xd1:
		return skipBytes(2)
	// D2 int 32
	case k == 0xd2:
		return skipBytes(4)
	// D3 int 64
	case k == 0xd3:
		return skipBytes(8)
	// D4...D8 fixext 1/2/4/8/16
	case k >= 0xd4 && k <= 0xd8:
		l := 1 << (k - 0xd4)
		return skipBytes(int(l) + 1)
	// D9 str 8
	case k == 0xd9:
		if l, err := read8(); err != nil {
			return err
		} else {
			return skipBytes(int(l))
		}
	// DA str 16
	case k == 0xda:
		if l, err := read16(); err != nil {
			return err
		} else {
			return skipBytes(int(l))
		}
	// DB str 32
	case k == 0xdb:
		if l, err := read32(); err != nil {
			return err
		} else {
			return skipBytes(int(l))
		}
	// DC array 16
	case k == 0xdc:
		if l, err := read16(); err != nil {
			return err
		} else {
			return skip(int(l))
		}
	// DD array 32
	case k == 0xdd:
		if l, err := read32(); err != nil {
			return err
		} else {
			return skip(int(l))
		}
	// DE map 16
	case k == 0xde:
		if l, err := read16(); err != nil {
			return err
		} else {
			return skip(int(l) * 2)
		}
	// DF map 32
	case k == 0xdf:
		if l, err := read32(); err != nil {
			return err
		} else {
			return skip(int(l) * 2)
		}
	// E0..FF negative fixint
	case k >= 0xe0:
		return nil
	}
	return fmt.Errorf("unsupported msgpack type 0x%02x", k)
}

// Unmarshalling functions

func Unmarshal(in io.Reader) (any, error) {
	v, err := Decode(in)
	if err != nil {
		return nil, err
	}
	return unmarshal(v), nil
}

func unmarshal(v Value) any {
	switch v := v.(type) {
	case Integer:
		if val, ok := v.Int(); ok {
			return val
		}
		val, _ := v.Uint()
		return val
	case String:
		return string(v)
	case Array:
		res := make([]any, len(v))
		for i, item := range v {
			res[i] = unmarshal(item)
		}
		return res
	case Map:
		res := make(map[any]any)
		for k, val := range v.Iterate() {
			kind := k.Kind()
			if kind == ValueKindMap || kind == ValueKindArray || kind == ValueKindExtension {
				// Return the original map if it has non-unmarshallable keys
				return v
			}
			res[unmarshal(k)] = unmarshal(val)
		}
		return res
	case Bool:
		return bool(v)
	case Nil:
		return nil
	case Binary:
		return []byte(v)
	case Extension:
		return v
	case Float32:
		return float32(v)
	case Float64:
		return float64(v)
	default:
		panic("unsupported value type in unmarshal")
	}
}

func ToInt(v any) (int, bool) {
	switch v := v.(type) {
	case Integer:
		return v.Int()
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case uint:
		return int(v), v <= math.MaxInt
	case uint8:
		return int(v), true
	case uint16:
		return int(v), true
	case uint32:
		return int(v), uint(v) <= math.MaxInt
	case uint64:
		return int(v), v <= math.MaxInt
	}
	return 0, false
}

func ToUint(v any) (uint, bool) {
	switch v := v.(type) {
	case Integer:
		return v.Uint()
	case uint:
		return v, true
	case uint8:
		return uint(v), true
	case uint16:
		return uint(v), true
	case uint32:
		return uint(v), true
	case uint64:
		return uint(v), true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int8:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int16:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int32:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint(v), true
	}
	return 0, false
}

func ToString(v any) (string, bool) {
	switch v := v.(type) {
	case String:
		return string(v), true
	case string:
		return v, true
	}
	return "", false
}
