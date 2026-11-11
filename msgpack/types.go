// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpack

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"iter"
	"math"
	"slices"
	"strconv"
	"strings"
)

// ValueKind identifies the MessagePack type of a [Value].
type ValueKind int

const (
	ValueKindNil       ValueKind = iota // a [Nil] value
	ValueKindBool                       // a [Bool] value
	ValueKindInteger                    // an [Integer] value, signed or unsigned
	ValueKindFloat32                    // a [Float32] value
	ValueKindFloat64                    // a [Float64] value
	ValueKindString                     // a [String] value
	ValueKindBinary                     // a [Binary] value
	ValueKindArray                      // an [Array] value
	ValueKindMap                        // a [Map] value
	ValueKindExtension                  // an [Extension] value
	ValueKindRaw                        // a [RawMessage], an encoded but not yet decoded value
)

// Value is a decoded MessagePack value. It is implemented only by the types
// of this package: [Nil], [Bool], [Integer], [Float32], [Float64], [String],
// [Binary], [Array], [Map], [Extension] and [RawMessage].
//
// The String method returns a compact, human readable representation of the
// value, intended for debugging and logging.
type Value interface {
	fmt.Stringer

	// Kind returns the kind of the value.
	Kind() ValueKind

	// hash returns a hashable representation of the value to be used in Maps.
	hash() mapHashableKey
}

// Nil represents the MessagePack nil value.
type Nil struct{}

// NewNil returns a [Nil] value.
func NewNil() Nil {
	return Nil{}
}

// Kind returns [ValueKindNil].
func (Nil) Kind() ValueKind {
	return ValueKindNil
}

// String returns "nil".
func (Nil) String() string {
	return "nil"
}

func (Nil) hash() mapHashableKey {
	return nil
}

// Bool represents a MessagePack boolean value.
type Bool bool

// NewBool returns b as a [Bool] value.
func NewBool(b bool) Bool {
	return Bool(b)
}

// Kind returns [ValueKindBool].
func (Bool) Kind() ValueKind {
	return ValueKindBool
}

// String returns "true" or "false".
func (b Bool) String() string {
	if b {
		return "true"
	}
	return "false"
}

func (b Bool) hash() mapHashableKey {
	return bool(b)
}

// True is a constant representing the boolean value true.
const True = Bool(true)

// False is a constant representing the boolean value false.
const False = Bool(false)

// Integer represents a MessagePack integer. It can hold any value in the
// range [math.MinInt64, math.MaxUint64], so both signed and unsigned 64-bit
// integers are represented without loss.
//
// MessagePack does not distinguish between signed and unsigned integers at
// the value level, so the encoding used on the wire is not preserved: use the
// accessor methods to read the value as the desired Go type.
type Integer struct {
	i64 int64
	u64 uint64 // this field is used only if the integer cannot be stored in i64 (i.e. if it's an unsigned integer greater than math.MaxInt64)
}

// NewInteger returns i as an [Integer] value.
func NewInteger[I int8 | int16 | int32 | int64 | uint8 | uint16 | uint32 | uint64 | uint | int](i I) Integer {
	if i > 0 && uint64(i) > math.MaxInt64 {
		return Integer{u64: uint64(i)}
	}
	return Integer{i64: int64(i)}
}

// Kind returns [ValueKindInteger].
func (Integer) Kind() ValueKind {
	return ValueKindInteger
}

// IsSigned reports whether the integer fits in an int64, i.e. whether it is
// not greater than math.MaxInt64.
func (i Integer) IsSigned() bool {
	return i.i64 != 0 || i.u64 <= math.MaxInt64
}

// Int returns the integer as an int. The boolean result is false if the
// value does not fit in an int, in which case the returned int is truncated.
func (i Integer) Int() (int, bool) {
	if i.u64 != 0 {
		return int(i.u64), i.u64 <= math.MaxInt //nolint:gosec // truncation is documented
	}
	return int(i.i64), true
}

// Uint returns the integer as a uint. The boolean result is false if the
// value is negative or does not fit in a uint, in which case the returned
// uint is truncated.
func (i Integer) Uint() (uint, bool) {
	if i.u64 != 0 {
		return uint(i.u64), i.u64 <= math.MaxUint
	}
	return uint(i.i64), i.i64 >= 0 && i.i64 <= math.MaxInt //nolint:gosec // truncation is documented
}

// Int64 returns the integer as an int64. The boolean result is false if the
// value is greater than math.MaxInt64, in which case the returned int64 is
// truncated.
func (i Integer) Int64() (int64, bool) {
	if i.u64 != 0 {
		return int64(i.u64), i.u64 <= math.MaxInt64 //nolint:gosec // truncation is documented
	}
	return i.i64, true
}

// Uint64 returns the integer as a uint64. The boolean result is false if the
// value is negative, in which case the returned uint64 is the two's
// complement of the value.
func (i Integer) Uint64() (uint64, bool) {
	if i.i64 != 0 {
		return uint64(i.i64), i.i64 >= 0 //nolint:gosec // two's complement is documented
	}
	return i.u64, true
}

// String returns the integer in decimal notation.
func (i Integer) String() string {
	if i.u64 != 0 {
		return fmt.Sprintf("%d", i.u64)
	}
	return fmt.Sprintf("%d", i.i64)
}

func (i Integer) hash() mapHashableKey {
	if i.IsSigned() {
		return i.i64
	}
	return i.u64
}

// Float32 represents a MessagePack single precision floating point number.
type Float32 float32

// NewFloat32 returns f as a [Float32] value.
func NewFloat32(f float32) Float32 {
	return Float32(f)
}

// Kind returns [ValueKindFloat32].
func (Float32) Kind() ValueKind {
	return ValueKindFloat32
}

// String returns the number formatted with the %f verb, for example
// "1.500000".
func (f Float32) String() string {
	return fmt.Sprintf("%f", float32(f))
}

func (f Float32) hash() mapHashableKey {
	return float32(f)
}

// Float64 represents a MessagePack double precision floating point number.
type Float64 float64

// NewFloat64 returns f as a [Float64] value.
func NewFloat64(f float64) Float64 {
	return Float64(f)
}

// Kind returns [ValueKindFloat64].
func (Float64) Kind() ValueKind {
	return ValueKindFloat64
}

// String returns the number formatted with the %f verb and prefixed by "F",
// for example "F1.500000", to tell it apart from a [Float32].
func (f Float64) String() string {
	return fmt.Sprintf("F%f", float64(f))
}

func (f Float64) hash() mapHashableKey {
	return float64(f)
}

// String represents a MessagePack string. The content is not validated as
// UTF-8.
type String string

// NewString returns s as a [String] value.
func NewString(s string) String {
	return String(s)
}

// Kind returns [ValueKindString].
func (String) Kind() ValueKind {
	return ValueKindString
}

// String returns the string as a double-quoted Go string literal.
func (s String) String() string {
	return strconv.Quote(string(s))
}

func (s String) hash() mapHashableKey {
	return string("S:" + s)
}

// Binary represents a MessagePack binary blob.
type Binary []byte

// NewBinary returns b as a [Binary] value. The slice is not copied.
func NewBinary(b []byte) Binary {
	return Binary(b)
}

// Kind returns [ValueKindBinary].
func (Binary) Kind() ValueKind {
	return ValueKindBinary
}

// String returns the data hex-encoded and prefixed by "B", for example
// "B0a0b".
func (b Binary) String() string {
	return "B" + hex.EncodeToString(b)
}

func (b Binary) hash() mapHashableKey {
	return "B:" + hex.EncodeToString(b)
}

// Array represents a MessagePack array.
type Array []Value

// NewArray returns an empty [Array].
func NewArray() Array {
	return Array(nil)
}

// Append adds v at the end of the array.
func (a *Array) Append(v Value) {
	*a = append(*a, v)
}

// Kind returns [ValueKindArray].
func (Array) Kind() ValueKind {
	return ValueKindArray
}

// String returns the elements of the array, comma separated and enclosed in
// square brackets, for example `[1,"a",true]`.
func (a Array) String() string {
	var s strings.Builder
	s.WriteString("[")
	for i, v := range a {
		if i > 0 {
			s.WriteString(",")
		}
		s.WriteString(v.String())
	}
	s.WriteString("]")
	return s.String()
}

func (a Array) hash() mapHashableKey {
	return "A:" + a.String()
}

type mapHashableKey any

// Map represents a MessagePack map.
//
// Unlike a Go map, keys can be of any [Value] type (except [RawMessage]),
// including arrays and maps, and the insertion order of the keys is
// preserved: it is used by [Map.Iterate], [Map.String] and when the map is
// encoded. Two keys are considered equal if they have the same kind and the
// same content, so for example the Integer 1 and the Float64 1.0 are distinct
// keys.
//
// A Map must be created with [NewMap]: calling [Map.Set] on the zero value
// panics. A Map holds internal references, like a slice, so it should be
// modified only through a single variable (or a pointer to it): modifying a
// copy can leave the original in an inconsistent state.
type Map struct {
	v map[mapHashableKey]Value
	k []Value
}

// NewMap creates a new empty Map.
func NewMap() Map {
	return Map{v: make(map[mapHashableKey]Value)}
}

// Kind returns [ValueKindMap].
func (Map) Kind() ValueKind {
	return ValueKindMap
}

// String returns the key-value pairs of the map in insertion order, enclosed
// in curly braces, for example `{"a":1,"b":2}`.
func (m Map) String() string {
	var s strings.Builder
	s.WriteString("{")
	for i, k := range m.k {
		if i > 0 {
			s.WriteString(",")
		}
		v, _ := m.Get(k)
		s.WriteString(k.String())
		s.WriteString(":")
		s.WriteString(v.String())
	}
	s.WriteString("}")
	return s.String()
}

// Len returns the number of key-value pairs in the map.
func (m Map) Len() int {
	return len(m.v)
}

// Get returns the value associated with key. The boolean result reports
// whether the key is present in the map.
func (m Map) Get(key Value) (Value, bool) {
	v, ok := m.v[key.hash()]
	return v, ok
}

// Set associates value with key. If the key is already present, its value is
// replaced and the key is moved to the end of the insertion order.
func (m *Map) Set(key Value, value Value) {
	keyHash := key.hash()
	if _, exists := m.v[keyHash]; exists {
		// If the key already exists, we move the key to the end of the slice to maintain the insertion order.
		m.k = slices.DeleteFunc(m.k, func(k Value) bool { return k.hash() == keyHash })
	}
	m.k = append(m.k, key)
	m.v[key.hash()] = value
}

// Delete removes key and its associated value from the map. It does nothing
// if the key is not present.
func (m *Map) Delete(key Value) {
	keyHash := key.hash()
	delete(m.v, keyHash)
	m.k = slices.DeleteFunc(m.k, func(k Value) bool { return k.hash() == keyHash })
}

func (m Map) hash() mapHashableKey {
	return "M:" + m.String()
}

// Iterate returns an iterator over the key-value pairs of the map, in
// insertion order:
//
//	for k, v := range m.Iterate() {
//		...
//	}
func (m Map) Iterate() iter.Seq2[Value, Value] {
	return func(yield func(Value, Value) bool) {
		for _, k := range m.k {
			if !yield(k, m.v[k.hash()]) {
				return
			}
		}
	}
}

// Extension represents a MessagePack extension value: an application-defined
// Type tag and its opaque payload. Negative types are reserved by the
// MessagePack specification (for example -1 is the timestamp type); this
// package does not interpret them.
type Extension struct {
	Type int8
	Data []byte
}

// Kind returns [ValueKindExtension].
func (Extension) Kind() ValueKind {
	return ValueKindExtension
}

// String returns the type and the hex-encoded payload, for example
// "Ext05(0a0b)".
func (e Extension) String() string {
	return fmt.Sprintf("Ext%02X(%s)", e.Type, hex.EncodeToString(e.Data))
}

func (e Extension) hash() mapHashableKey {
	return fmt.Sprintf("E:%02X:%s", e.Type, hex.EncodeToString(e.Data))
}

// RawMessage holds the encoding of a MessagePack value that has not been
// decoded yet. It is returned by [DecodeRaw] and is written verbatim by
// [Encode], which makes it useful to forward a payload without decoding and
// re-encoding it, or to defer its decoding.
//
// A RawMessage cannot be used as a [Map] key.
type RawMessage struct {
	data []byte
}

// NewRawMessage creates a new RawMessage with the given encoded data. The
// data is not validated, so it must be a single, well-formed MessagePack
// value.
func NewRawMessage(data ...byte) RawMessage {
	return RawMessage{data: data}
}

// Kind returns [ValueKindRaw].
func (RawMessage) Kind() ValueKind {
	return ValueKindRaw
}

// String returns the hex-encoded data, for example "Raw(93010203)".
func (r RawMessage) String() string {
	return fmt.Sprintf("Raw(%s)", hex.EncodeToString(r.data))
}

func (r RawMessage) hash() mapHashableKey {
	panic("RawMessage cannot be used as a map key")
}

// Unmarshal decodes the message as [Unmarshal] does and returns the result.
// It returns nil if the data is not a valid MessagePack value.
func (r RawMessage) Unmarshal() any {
	v, _ := Unmarshal(bytes.NewBuffer(r.data))
	return v
}

// Decode decodes the message as [Decode] does and returns the result as a
// [Value]. It returns nil if the data is not a valid MessagePack value.
func (r RawMessage) Decode() any {
	v, _ := Decode(bytes.NewBuffer(r.data))
	return v
}
