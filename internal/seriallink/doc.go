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
//	4 SYN      session request, payload: max_payload u16 | window u8 | nonce u32 [| baud_rates u16]
//	5 SYN_ACK  session accept,  payload: max_payload u16 | window u8 | nonce u32 | echoed_nonce u32 [| baud_rates u16]
//	6 RST      session abort,   payload: nonce u32
//
// All multi-byte integers are little endian. seq/ack/wnd are only meaningful
// in DATA, ACK and NAK frames.
//
// The fields in brackets are optional: a receiver must accept SYN and SYN_ACK
// payloads with missing optional fields, and ignore any trailing bytes it does
// not know about, so that new fields can be added in the future. Whatever its
// max payload, a receiver must accept control frames (SYN, SYN_ACK, RST) with
// a payload of up to 32 bytes, and a sender must not send larger ones.
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
//
// # Baud rate
//
// Every session starts at the base baud rate (115200 by default), the rate
// the ports are opened at. The two sides can then switch to a faster rate:
// each one lists the rates it supports in the baud_rates field of its SYN or
// SYN_ACK, a bit mask where bit i stands for the i-th rate of this table
// (bits 14 and 15 are reserved and must be 0):
//
//	bit  0 115200    bit  5 921600     bit 10 2500000
//	bit  1 230400    bit  6 1000000    bit 11 3000000
//	bit  2 460800    bit  7 1152000    bit 12 3500000
//	bit  3 500000    bit  8 1500000    bit 13 4000000
//	bit  4 576000    bit  9 2000000
//
// A side lists only the rates higher than the base rate that it can generate
// accurately and that it is configured to accept. The negotiated rate is the
// highest rate in both masks: both sides compute the same one, so there is
// nothing more to agree on. If there is none (e.g. the mask is missing), the
// session stays at the base rate.
//
// The switch needs no dedicated messages. An established side at the base
// rate switches when it receives a valid frame from the peer, which proves
// that the peer is at the base rate too:
//
//  1. it answers with two frames at the base rate (SYN_ACK if the peer may
//     not be established yet, ACK otherwise), so that the peer switches too
//     even if one of them is lost;
//  2. it waits until they have been transmitted, and changes the rate.
//
// DATA keeps flowing: the frames lost while the two sides are at different
// rates are retransmitted as usual. The bytes sent while the rates do not
// match are received as garbage, that may swallow the next frame up to its
// delimiter: every frame is preceded by an extra 0x00 (an empty frame,
// ignored by the receiver) that ends the garbage, while at the base rate,
// until the switch is proven, and for two keep-alive intervals after
// changing the rate (the peer may have been proven earlier, and needs that
// time to fall back if it was not).
//
// The switch is proven when a valid frame is received at least one
// retransmission timeout after changing the rate: the frames received
// earlier may have been sent by the peer at the base rate, and read late from
// the port buffers.
//
// A side at the negotiated rate that receives no valid frame for two
// keep-alive intervals goes back to the base rate (and sends an ACK there,
// which makes a peer at the base rate start a new attempt). If the switch was
// not proven yet, the attempt failed: either the peer did not switch (the
// answers were lost) or the rate does not work. After 5 failed attempts in a
// row the rate is considered not working: the side resets the session (RST).
// The rate is not lowered automatically, so the next sessions fail the same
// way: a link that works only some of the time would hide the problem, the
// maximum rate must be lowered by configuration instead.
//
// If the switch was proven, either the line is down (and the link timeout
// applies as usual) or the peer restarted at the base rate, and its SYN must
// be heard to reset the session quickly. While at the base rate the side
// switches again at the next valid frame, as above. When a session ends both
// sides return to the base rate.
package seriallink
