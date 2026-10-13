// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package seriallink implements a reliable, ordered, flow-controlled byte
// stream on top of an unreliable serial line (no integrity guarantees, no
// hardware flow control). A Link wraps the serial port and is itself an
// io.ReadWriteCloser, so the msgpack-rpc layer can run on top of it unchanged.
//
// # Wire format
//
// Each frame is encoded as:
//
//	COBS( header | payload | CRC32 ) 0x00
//
// The 0x00 byte delimits frames: COBS guarantees that it never appears inside
// an encoded frame, so after any corruption the receiver resynchronizes at the
// next 0x00 (see package internal/cobs for a description of the encoding).
// The decoded frame is:
//
//	offset size field
//	0      1    ver_type: protocol version (high nibble, currently 1) | frame type (low nibble)
//	1      1    seq:      sequence number of this DATA frame (mod 256)
//	2      1    ack:      next sequence number expected from the peer (cumulative ack)
//	3      1    wnd:      receive credit, number of DATA frames the peer may send starting at ack
//	4      N    payload   (N <= negotiated max payload)
//	4+N    4    CRC-32 (IEEE 802.3, the same as zlib/Ethernet) of bytes [0, 4+N), little endian
//
// Frames with a bad COBS encoding, a bad CRC, an unknown version or an unknown
// type are silently dropped: retransmission takes care of recovering them.
//
// # Frame types
//
//	1 DATA     carries stream bytes. seq is meaningful, ack/wnd piggyback the reverse direction.
//	2 ACK      acknowledgement and window update only (also used as keep-alive).
//	3 NAK      as ACK, but also asks the sender to go back and retransmit from ack.
//	4 SYN      session request, payload: max_payload u16 | window u8 | nonce u32
//	5 SYN_ACK  session accept,  payload: max_payload u16 | window u8 | nonce u32 | echoed_nonce u32
//	6 RST      session abort,   payload: nonce u32
//
// All multi-byte integers are little endian. seq/ack/wnd are only meaningful
// in DATA, ACK and NAK frames.
//
// # Session
//
// Every time a Link is created it picks a random nonce and sends SYN until it
// gets a SYN_ACK that echoes it back. Upon receiving a SYN, a side answers with
// a SYN_ACK echoing the peer nonce. A side becomes established when it receives
// a SYN_ACK echoing its own nonce (it then sends a SYN_ACK back, so the peer
// can complete too), or when it receives DATA/ACK/NAK from a peer whose SYN it
// already answered. Both sides start with seq=0 and ack=0. The negotiated max
// payload is the minimum of the two, and each side never has more DATA frames
// in flight than the window announced by the peer in its SYN/SYN_ACK.
//
// An established side receiving a RST, or a SYN with a different nonce (the
// peer restarted), terminates the session. A SYN with the same nonce is a
// retransmission (our SYN_ACK was lost) and is answered again with a SYN_ACK,
// unless the peer already sent DATA/ACK/NAK in this session: a peer sends SYN
// only until it is established, so that SYN comes from a restarted peer that
// happened to pick the same nonce, and terminates the session too. When the
// session is terminated, Read and Write fail with ErrSessionReset and the
// upper layer must drop any partially decoded state and open a new Link.
//
// A side that is not established and receives DATA/ACK/NAK from an unknown
// peer answers with RST, so a peer stuck on an old session notices and
// restarts.
//
// # Reliability (Go-Back-N)
//
// The receiver accepts only the DATA frame with seq == expected, anything else
// is dropped: a duplicate (an old seq) is answered with an ACK, a frame from
// the future (a frame was lost) is answered with a NAK (once per expected
// value). Acks are delayed by a few milliseconds to be piggybacked, but are
// sent immediately when half of the window is unacknowledged.
//
// The sender retransmits all the unacknowledged frames, in order, upon
// receiving a NAK or when no progress is made for a retransmission timeout.
// After too many consecutive timeouts the link is declared dead
// (ErrLinkTimeout).
//
// # Flow control
//
// Every DATA/ACK/NAK frame carries wnd: how many DATA frames (of max payload
// size) the receiver can buffer beyond ack. The sender never sends seq >=
// ack+wnd. When the application frees space, the receiver sends a window
// update. Idle sides send an ACK every keep-alive interval: this refreshes the
// window and lets the peer detect a dead link (ErrLinkTimeout) when nothing is
// received for the link timeout.
package seriallink
