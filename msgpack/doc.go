// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package msgpack implements a small, dependency-free encoder and decoder for
// the MessagePack serialization format (https://msgpack.org).
//
// # Value model
//
// Every decoded MessagePack object is represented by a [Value]. The concrete
// types map one-to-one onto the MessagePack type system:
//
//	MessagePack   Go type
//	-----------   ---------------------------------------
//	nil           [Nil]
//	bool          [Bool]
//	int / uint    [Integer] (covers the full int64 and uint64 range)
//	float 32      [Float32]
//	float 64      [Float64]
//	str           [String]
//	bin           [Binary]
//	array         [Array]
//	map           [Map] (preserves insertion order, accepts any Value as key)
//	ext           [Extension]
//
// [RawMessage] is an additional, non-standard Value that holds an already
// encoded MessagePack object. It is used to defer decoding, or to forward a
// payload without decoding it at all.
//
// # Encoding
//
// [Encode] writes a value to an [io.Writer] and [Marshal] returns it as a byte
// slice. Both accept the Value types above as well as native Go values (bool,
// all integer and float types, string, []byte, []any, map[any]any, slices,
// arrays and maps of any element type, and named types based on string, []byte
// or any integer type). Integers are written with the smallest signed format that can hold
// them (fixint, int 8/16/32/64); the uint 64 format is used only for values
// above math.MaxInt64.
//
// # Decoding
//
// The package offers several decoding functions, depending on how much of the
// input the caller wants to interpret:
//
//   - [Decode] reads one complete object and returns it as a [Value].
//   - [Unmarshal] is like Decode but converts the result into native Go values
//     (int, string, []any, map[any]any, ...).
//   - [DecodeRaw] reads one complete object and returns its encoding as a
//     [RawMessage], without building a Value tree.
//   - [Skip] reads one complete object and discards it.
//   - [DecodeSingle] reads only the next token (a scalar, or the header of a
//     string, binary, extension, array or map), allowing streaming parsers to
//     walk the input one element at a time.
//
// The helpers [ToInt], [ToUint] and [ToString] convert values returned by
// Decode or Unmarshal into Go types, checking for type and range errors.
//
// # Untrusted input
//
// The decoders are designed to be safe on untrusted input:
//
//   - Length fields in headers are not trusted: buffers are preallocated only
//     up to a small bound and then grown as data actually arrives, so a short
//     message cannot declare a huge length and force a large allocation.
//   - Nested arrays and maps are limited to [MaxNestingDepth] levels; deeper
//     input fails with [ErrMaxNestingDepthExceeded] instead of overflowing the
//     stack.
//
// The decoders do not limit the total size of a message. Callers that read from
// an untrusted stream should bound the input themselves, for example with
// [io.LimitReader].
package msgpack
