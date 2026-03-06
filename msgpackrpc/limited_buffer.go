// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package msgpackrpc

import "fmt"

type LimitedBuffer struct {
	buf    []byte
	limit  int
	length int
}

type ErrBufferLimitExceeded struct {
	Limit int
}

func (e *ErrBufferLimitExceeded) Error() string {
	return fmt.Sprintf("buffer limit (%d bytes) exceeded", e.Limit)
}

func (e *ErrBufferLimitExceeded) Is(target error) bool {
	_, ok := target.(*ErrBufferLimitExceeded)
	return ok
}

func NewLimitedBuffer(limit int) *LimitedBuffer {
	return &LimitedBuffer{
		buf:   make([]byte, limit),
		limit: limit,
	}
}

func (b *LimitedBuffer) Write(p []byte) (n int, err error) {
	n = copy(b.buf[b.length:], p)
	b.length += n
	if n < len(p) {
		return n, &ErrBufferLimitExceeded{Limit: b.limit}
	}
	return n, nil
}

func (b *LimitedBuffer) Bytes() []byte {
	return b.buf[:b.length]
}

func (b *LimitedBuffer) Reset() {
	b.length = 0
}

func (b *LimitedBuffer) Len() int {
	return b.length
}

func (b *LimitedBuffer) Cap() int {
	return b.limit
}

func (b *LimitedBuffer) String() string {
	return string(b.Bytes())
}
