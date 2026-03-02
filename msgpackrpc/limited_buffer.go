// This file is part of arduino-router
//
// Copyright (C) ARDUINO SRL (www.arduino.cc)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-router
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

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
