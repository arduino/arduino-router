// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cobs implements Consistent Overhead Byte Stuffing (COBS), as
// described by Stuart Cheshire and Mary Baker in "Consistent Overhead Byte
// Stuffing" (IEEE/ACM Transactions on Networking, 1999).
//
// COBS transforms an arbitrary sequence of bytes into a sequence that contains
// no 0x00 bytes. The 0x00 byte can then be used, unambiguously, as a packet
// delimiter on a byte stream: a receiver that loses synchronization (because
// of corrupted, lost or spurious bytes) just discards everything up to the
// next 0x00 and is back in sync, with at most one packet lost. The delimiter
// itself is not part of the encoding: it is up to the caller to append it
// after each encoded packet.
//
// # Encoding
//
// The input is conceptually extended with a trailing 0x00 and split in blocks,
// each one made of up to 254 non-zero bytes followed by a 0x00 (or by the end
// of the input). Each block is encoded as a code byte followed by its non-zero
// bytes, where the code is the number of non-zero bytes plus one:
//
//	code 0x01..0xFE: (code-1) data bytes follow, then the decoder emits a 0x00
//	code 0xFF:       254 data bytes follow, no 0x00 is emitted
//
// In other words, each code byte points to the position of the next 0x00 of
// the input (that is replaced by the next code byte). A 0xFF block is used
// when 254 non-zero bytes are found in a row: it does not consume a 0x00 of
// the input. The 0x00 implied by the last block (the conceptual trailing one)
// is never emitted by the decoder.
//
// Examples (the 0x00 delimiter is not shown):
//
//	input                     encoded
//	(empty)                   01
//	00                        01 01
//	00 00                     01 01 01
//	11 22 00 33               03 11 22 02 33
//	11 22 33 44               05 11 22 33 44
//	11 00 00 00               02 11 01 01 01
//	01 02 ... FE (254 bytes)  FF 01 02 ... FE
//	01 02 ... FF (255 bytes)  FF 01 02 ... FE 02 FF
//
// # Decoding
//
// The decoder reads a code byte, copies the following (code-1) bytes to the
// output and, if the code is not 0xFF and there is more input, emits a 0x00.
// Then it repeats with the next code byte until the input is exhausted. The
// encoding is invalid if it contains a 0x00, or if a code byte points past the
// end of the input.
//
// # Overhead
//
// The encoding adds exactly one byte for empty inputs and for inputs up to 254
// bytes, and at most one byte every 254 bytes in the worst case (long runs of
// non-zero bytes): an n-bytes input is encoded in at most n + n/254 + 1 bytes
// (see MaxEncodedLen). Unlike escape-based schemes, such as SLIP or HDLC, the
// overhead does not depend on the content: COBS never doubles the size of the
// data, no matter how many delimiter-like bytes it contains. Both encoding and
// decoding can be done in a single pass, without lookahead on the decoder side.
package cobs
