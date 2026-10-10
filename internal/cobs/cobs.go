// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package cobs

import (
	"errors"
	"slices"
)

// ErrInvalid is returned when decoding an invalid COBS encoding.
var ErrInvalid = errors.New("invalid COBS encoding")

// MaxEncodedLen returns the maximum length of the encoding of n bytes (without
// the delimiter).
func MaxEncodedLen(n int) int {
	return n + n/254 + 1
}

// Encode appends the COBS encoding of src to dst (without the 0x00 delimiter)
// and returns the extended buffer.
func Encode(dst, src []byte) []byte {
	codeIdx := len(dst)
	dst = append(dst, 0)
	code := byte(1)
	for i, b := range src {
		if b != 0 {
			dst = append(dst, b)
			code++
			if code != 0xFF {
				continue
			}
			if i == len(src)-1 {
				break // a full block at the end of the input needs no further block
			}
		}
		dst[codeIdx] = code
		codeIdx = len(dst)
		dst = append(dst, 0)
		code = 1
	}
	dst[codeIdx] = code
	return dst
}

// Decode appends the decoding of the COBS-encoded src (without the 0x00
// delimiter) to dst and returns the extended buffer.
func Decode(dst, src []byte) ([]byte, error) {
	for i := 0; i < len(src); {
		code := int(src[i])
		if code == 0 {
			return nil, ErrInvalid
		}
		i++
		end := i + code - 1
		if end > len(src) {
			return nil, ErrInvalid
		}
		if slices.Contains(src[i:end], 0) {
			return nil, ErrInvalid
		}
		dst = append(dst, src[i:end]...)
		i = end
		if code != 0xFF && i < len(src) {
			dst = append(dst, 0)
		}
	}
	return dst, nil
}
