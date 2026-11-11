// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpack

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
)

// Marshal returns the MessagePack encoding of val. See [Encode] for the
// supported types.
func Marshal(val any) ([]byte, error) {
	writer := bytes.NewBuffer(nil)
	if err := Encode(val, writer); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

// Encode writes the MessagePack encoding of val to out.
//
// val can be any [Value] or one of the following Go types:
//
//   - nil, encoded as nil;
//   - bool;
//   - int, int8, int16, int32, int64, uint, uint8, uint16, uint32 and uint64,
//     encoded with the smallest signed integer format that can hold the value,
//     or as uint 64 for values greater than math.MaxInt64;
//   - float32 and float64, encoded as float 32 and float 64 respectively;
//   - string, encoded as str;
//   - []byte, and any named type based on []byte, encoded as bin;
//   - any other slice or array, encoded as array;
//   - any map, encoded as map. The keys of Go maps are written in the
//     (random) map iteration order; use a [Map] to control the order;
//   - named types based on string or on any integer type, and pointers to
//     string or []byte types.
//
// The elements of slices, arrays and maps are encoded recursively and must be
// of a supported type. A [RawMessage] is written verbatim. Structs are not
// supported.
//
// If an unsupported type is found, Encode returns an error; part of the
// encoding may already have been written to out at that point.
func Encode(val any, out io.Writer) error {
	write8 := func(b byte) error {
		_, err := out.Write([]byte{b})
		return err
	}
	write := func(data []byte) error {
		_, err := out.Write(data)
		return err
	}
	writeBool := func(b bool) error {
		if b {
			return write8(0xc3) // C3 true
		}
		return write8(0xc2) // C2 false
	}
	writeInt64 := func(v int64) error {
		if v >= -32 && v <= 127 {
			// 00..7F positive fixint
			// E0..FF negative fixint
			return write8(byte(v))
		}
		if v >= math.MinInt8 && v <= math.MaxInt8 {
			return write([]byte{0xd0, byte(v)}) // D0 int 8
		}
		if v >= math.MinInt16 && v <= math.MaxInt16 {
			buf := []byte{0xd1, 0, 0} // D1 int 16
			for i := range 2 {
				buf[2-i] = byte(v >> (i * 8))
			}
			return write(buf)
		}
		if v >= math.MinInt32 && v <= math.MaxInt32 {
			buf := []byte{0xd2, 0, 0, 0, 0} // D2 int 32
			for i := range 4 {
				buf[4-i] = byte(v >> (i * 8))
			}
			return write(buf)
		}
		buf := []byte{0xd3, 0, 0, 0, 0, 0, 0, 0, 0} // D3 int 64
		for i := range 8 {
			buf[8-i] = byte(v >> (i * 8))
		}
		return write(buf)
	}
	writeUint64 := func(v uint64) error {
		if v <= math.MaxInt64 {
			return writeInt64(int64(v))
		}
		// Write 64-bit unsigned integer
		buf := []byte{0xcf, 0, 0, 0, 0, 0, 0, 0, 0} // CF uint 64
		for i := range 8 {
			buf[8-i] = byte(v >> (i * 8))
		}
		return write(buf)
	}
	writeStringHeader := func(l uint64) error {
		switch {
		case l <= 31:
			return write8(0xa0 | byte(l)) // A0..BF fixstr
		case l <= math.MaxUint8:
			return write([]byte{0xd9, byte(l)}) // D9 str 8
		case l <= math.MaxUint16:
			return write(binary.BigEndian.AppendUint16([]byte{0xda}, uint16(l))) // DA str 16
		case l <= math.MaxUint32:
			return write(binary.BigEndian.AppendUint32([]byte{0xdb}, uint32(l))) // DB str 32
		default:
			return errors.New("string too large to encode")
		}
	}
	writeBinaryHeader := func(l uint64) error {
		switch {
		case l <= math.MaxUint8:
			return write([]byte{0xc4, byte(l)}) // C4 bin 8
		case l <= math.MaxUint16:
			return write(binary.BigEndian.AppendUint16([]byte{0xc5}, uint16(l))) // C5 bin 16
		case l <= math.MaxUint32:
			return write(binary.BigEndian.AppendUint32([]byte{0xc6}, uint32(l))) // C6 bin 32
		default:
			return errors.New("binary data too large to encode")
		}
	}
	writeArrayHeader := func(l uint64) error {
		switch {
		case l <= 15:
			return write8(0x90 | byte(l)) // 90..9F fixarray
		case l <= math.MaxUint16:
			return write(binary.BigEndian.AppendUint16([]byte{0xdc}, uint16(l))) // DC array 16
		case l <= math.MaxUint32:
			return write(binary.BigEndian.AppendUint32([]byte{0xdd}, uint32(l))) // DD array 32
		default:
			return errors.New("array too large to encode")
		}
	}
	writeMapHeader := func(l uint64) error {
		switch {
		case l <= 15:
			return write8(0x80 | byte(l)) // 80..8F fixmap
		case l <= math.MaxUint16:
			return write(binary.BigEndian.AppendUint16([]byte{0xde}, uint16(l))) // DE map 16
		case l <= math.MaxUint32:
			return write(binary.BigEndian.AppendUint32([]byte{0xdf}, uint32(l))) // DF map 32
		default:
			return errors.New("map too large to encode")
		}
	}
	switch v := val.(type) {
	case Nil:
		return write8(0xc0) // C0 nil
	case nil:
		return write8(0xc0) // C0 nil
	case Bool:
		return writeBool(bool(v))
	case bool:
		return writeBool(v)
	case Integer:
		if v.u64 != 0 {
			return writeUint64(v.u64)
		}
		return writeInt64(v.i64)
	case uint:
		return writeUint64(uint64(v))
	case uint64:
		return writeUint64(v)
	case int:
		return writeInt64(int64(v))
	case int8:
		return writeInt64(int64(v))
	case int16:
		return writeInt64(int64(v))
	case int32:
		return writeInt64(int64(v))
	case int64:
		return writeInt64(v)
	case uint8:
		return writeInt64(int64(v))
	case uint16:
		return writeInt64(int64(v))
	case uint32:
		return writeInt64(int64(v))
	case Float32:
		return write(binary.BigEndian.AppendUint32([]byte{0xca}, math.Float32bits(float32(v)))) // CA float 32
	case float32:
		return write(binary.BigEndian.AppendUint32([]byte{0xca}, math.Float32bits(v))) // CA float 32
	case Float64:
		return write(binary.BigEndian.AppendUint64([]byte{0xcb}, math.Float64bits(float64(v)))) // CB float 64
	case float64:
		return write(binary.BigEndian.AppendUint64([]byte{0xcb}, math.Float64bits(v))) // CB float 64
	case String:
		if err := writeStringHeader(uint64(len(v))); err != nil {
			return err
		}
		return write([]byte(v))
	case string:
		if err := writeStringHeader(uint64(len(v))); err != nil {
			return err
		}
		return write([]byte(v))
	case Binary:
		if err := writeBinaryHeader(uint64(len(v))); err != nil {
			return err
		}
		return write([]byte(v))
	case []byte:
		if err := writeBinaryHeader(uint64(len(v))); err != nil {
			return err
		}
		return write(v)
	case Array:
		if err := writeArrayHeader(uint64(len(v))); err != nil {
			return err
		}
		for _, elem := range v {
			if err := Encode(elem, out); err != nil {
				return err
			}
		}
		return nil
	case []any:
		if err := writeArrayHeader(uint64(len(v))); err != nil {
			return err
		}
		for _, elem := range v {
			if err := Encode(elem, out); err != nil {
				return err
			}
		}
		return nil
	case Map:
		if err := writeMapHeader(uint64(v.Len())); err != nil { //nolint:gosec // lengths are never negative
			return err
		}
		for k, v := range v.Iterate() {
			if err := Encode(k, out); err != nil {
				return err
			}
			if err := Encode(v, out); err != nil {
				return err
			}
		}
		return nil
	case map[any]any:
		if err := writeMapHeader(uint64(len(v))); err != nil {
			return err
		}
		for k, v := range v {
			if err := Encode(k, out); err != nil {
				return err
			}
			if err := Encode(v, out); err != nil {
				return err
			}
		}
		return nil
	case RawMessage:
		_, err := out.Write(v.data)
		return err
	}

	// Detect if the value is a typed map or a typed array by reflection, and encode it.
	reflectVal := reflect.ValueOf(val)
	switch reflectVal.Kind() {
	case reflect.Map:
		keys := reflectVal.MapKeys()
		if err := writeMapHeader(uint64(len(keys))); err != nil {
			return err
		}
		for _, k := range keys {
			v := reflectVal.MapIndex(k)
			if err := Encode(k.Interface(), out); err != nil {
				return err
			}
			if err := Encode(v.Interface(), out); err != nil {
				return err
			}
		}
		return nil
	case reflect.Slice, reflect.Array:
		if reflectVal.Kind() == reflect.Slice && reflectVal.Type().Elem().Kind() == reflect.Uint8 {
			// Named byte slices, like `type MyBytes []byte`, are encoded as binary
			return Encode(reflectVal.Bytes(), out)
		}
		l := reflectVal.Len()
		if err := writeArrayHeader(uint64(l)); err != nil { //nolint:gosec // lengths are never negative
			return err
		}
		for i := range l {
			if err := Encode(reflectVal.Index(i).Interface(), out); err != nil {
				return err
			}
		}
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Named integer types, like `type MyInt int`
		return Encode(reflectVal.Int(), out)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// Named unsigned integer types, like `type MyUint uint8`
		return Encode(reflectVal.Uint(), out)
	}

	// Unwrap type that are alias of base types like `type MyString string`
	if reflectVal.Kind() == reflect.Pointer {
		reflectVal = reflectVal.Elem()
	}
	if reflectVal.Kind() == reflect.String {
		return Encode(reflectVal.String(), out)
	}
	if reflectVal.Kind() == reflect.Slice && reflectVal.Type().Elem().Kind() == reflect.Uint8 {
		return Encode(reflectVal.Bytes(), out)
	}

	return fmt.Errorf("unsupported type: %T", val)
}
