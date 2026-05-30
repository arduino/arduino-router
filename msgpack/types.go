// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpack

import (
	"encoding/hex"
	"fmt"
	"iter"
	"math"
	"slices"
	"strconv"
	"strings"
)

// ValueKind represents the kind of a MessagePack value.
type ValueKind int

const (
	ValueKindNil ValueKind = iota
	ValueKindBool
	ValueKindInteger
	ValueKindFloat32
	ValueKindFloat64
	ValueKindString
	ValueKindBinary
	ValueKindArray
	ValueKindMap
	ValueKindExtension
)

// Value represents a MessagePack value.
type Value interface {
	fmt.Stringer

	// Kind returns the kind of the value.
	Kind() ValueKind

	// hash returns a hashable representation of the value to be used in Maps.
	hash() mapHashableKey
}

// Nil represents a nil value.
type Nil struct{}

func NewNil() Nil {
	return Nil{}
}

func (Nil) Kind() ValueKind {
	return ValueKindNil
}

func (Nil) String() string {
	return "nil"
}

func (Nil) hash() mapHashableKey {
	return nil
}

// Bool represents a boolean value.
type Bool bool

func NewBool(b bool) Bool {
	return Bool(b)
}

func (Bool) Kind() ValueKind {
	return ValueKindBool
}

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

// Integer represents an integer value.
type Integer struct {
	i64 int64
	u64 uint64 // this field is used only if the integer cannot be stored in i64 (i.e. if it's an unsigned integer greater than math.MaxInt64)
}

func NewInteger[I int8 | int16 | int32 | int64 | uint8 | uint16 | uint32 | uint64 | uint | int](i I) Integer {
	if i > 0 && uint64(i) > math.MaxInt64 {
		return Integer{u64: uint64(i)}
	}
	return Integer{i64: int64(i)}
}

func (Integer) Kind() ValueKind {
	return ValueKindInteger
}

// IsSigned returns true if the integer could be represented
// with a signed integer.
func (i Integer) IsSigned() bool {
	return i.i64 != 0 || i.u64 <= math.MaxInt64
}

// Int returns the integer as int if possible.
func (i Integer) Int() (int, bool) {
	if i.u64 != 0 {
		return int(i.u64), i.u64 <= math.MaxInt
	}
	return int(i.i64), true
}

// Int64 returns the integer as int64 if possible.
func (i Integer) Int64() (int64, bool) {
	if i.u64 != 0 {
		return int64(i.u64), i.u64 <= math.MaxInt64
	}
	return i.i64, true
}

// Uint64 returns the integer as uint64 if possible.
func (i Integer) Uint64() (uint64, bool) {
	if i.i64 != 0 {
		return uint64(i.i64), i.i64 >= 0
	}
	return i.u64, true
}

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

// Float32 represents a float32 value.
type Float32 float32

func NewFloat32(f float32) Float32 {
	return Float32(f)
}

func (Float32) Kind() ValueKind {
	return ValueKindFloat32
}

func (f Float32) String() string {
	return fmt.Sprintf("%f", float32(f))
}

func (f Float32) hash() mapHashableKey {
	return float32(f)
}

// Float64 represents a float64 value.
type Float64 float64

func NewFloat64(f float64) Float64 {
	return Float64(f)
}

func (Float64) Kind() ValueKind {
	return ValueKindFloat64
}

func (f Float64) String() string {
	return fmt.Sprintf("F%f", float64(f))
}

func (f Float64) hash() mapHashableKey {
	return float64(f)
}

// String represents a string value.
type String string

func NewString(s string) String {
	return String(s)
}

func (String) Kind() ValueKind {
	return ValueKindString
}

func (s String) String() string {
	return strconv.Quote(string(s))
}

func (s String) hash() mapHashableKey {
	return string("S:" + s)
}

// Binary represents a binary value.
type Binary []byte

func NewBinary(b []byte) Binary {
	return Binary(b)
}

func (Binary) Kind() ValueKind {
	return ValueKindBinary
}

func (b Binary) String() string {
	return "B" + hex.EncodeToString(b)
}

func (b Binary) hash() mapHashableKey {
	return "B:" + hex.EncodeToString(b)
}

// Array represents an array value.
type Array []Value

func NewArray() Array {
	return Array(nil)
}

func (a *Array) Append(v Value) {
	*a = append(*a, v)
}

func (Array) Kind() ValueKind {
	return ValueKindArray
}

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

// Map represents a map value.
type Map struct {
	v map[mapHashableKey]Value
	k []Value
}

// NewMap creates a new empty Map.
func NewMap() Map {
	return Map{v: make(map[mapHashableKey]Value)}
}

func (Map) Kind() ValueKind {
	return ValueKindMap
}

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

// Get returns the value associated with the given key.
func (m Map) Get(key Value) (Value, bool) {
	v, ok := m.v[key.hash()]
	return v, ok
}

// Set sets the value for the given key.
func (m *Map) Set(key Value, value Value) {
	keyHash := key.hash()
	if _, exists := m.v[keyHash]; exists {
		// If the key already exists, we move the key to the end of the slice to maintain the insertion order.
		m.k = slices.DeleteFunc(m.k, func(k Value) bool { return k.hash() == keyHash })
	}
	m.k = append(m.k, key)
	m.v[key.hash()] = value
}

// Delete deletes the key-value pair with the given key.
func (m *Map) Delete(key Value) {
	keyHash := key.hash()
	delete(m.v, keyHash)
	m.k = slices.DeleteFunc(m.k, func(k Value) bool { return k.hash() == keyHash })
}

func (m Map) hash() mapHashableKey {
	return "M:" + m.String()
}

// Iterate iterates over the key-value pairs in the map.
func (m Map) Iterate() iter.Seq2[Value, Value] {
	return func(yield func(Value, Value) bool) {
		for _, k := range m.k {
			if !yield(k, m.v[k.hash()]) {
				return
			}
		}
	}
}

// Extension represents an extension value.
type Extension struct {
	Type int8
	Data []byte
}

func (Extension) Kind() ValueKind {
	return ValueKindExtension
}

func (e Extension) String() string {
	return fmt.Sprintf("Ext%02X(%s)", e.Type, hex.EncodeToString(e.Data))
}

func (e Extension) hash() mapHashableKey {
	return fmt.Sprintf("E:%02X:%s", e.Type, hex.EncodeToString(e.Data))
}
