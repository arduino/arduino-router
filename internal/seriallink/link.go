// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package seriallink

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/arduino/arduino-router/internal/cobs"
)

// newNonce generates session nonces (replaced in tests).
var newNonce = rand.Uint32

var (
	// ErrClosed is returned by Read and Write after Close has been called.
	ErrClosed = errors.New("seriallink: link closed")
	// ErrSessionReset is returned when the peer restarted or aborted the session.
	ErrSessionReset = errors.New("seriallink: session reset by peer")
	// ErrLinkTimeout is returned when the peer stopped responding.
	ErrLinkTimeout = errors.New("seriallink: link timeout")
	// ErrBaudRate is returned when the session has been reset because the
	// negotiated baud rate does not work. The rate is not lowered
	// automatically: the next sessions will fail the same way until
	// MaxBaudRate (on either side) is lowered.
	ErrBaudRate = errors.New("seriallink: negotiated baud rate not working")
)

// maxBaudRateFailures is the number of failed attempts to switch to the
// negotiated baud rate after which the session is reset.
const maxBaudRateFailures = 5

// Config contains the link parameters. Zero values are replaced by defaults.
type Config struct {
	// MaxPayload is the maximum number of stream bytes carried by a single
	// frame (default 256). The effective value is the minimum between the two
	// peers.
	MaxPayload int
	// Window is the maximum number of unacknowledged frames in flight, and
	// also the size of the receive buffer in frames (default 8).
	Window int
	// RetransmitTimeout is the time without progress after which the
	// unacknowledged frames are sent again (default 250ms). It must be larger
	// than the time needed to transmit a full window on the serial line.
	RetransmitTimeout time.Duration
	// MaxRetries is the number of consecutive retransmission timeouts after
	// which the link is declared dead (default 10).
	MaxRetries int
	// AckDelay is how long an acknowledgement is delayed waiting to be
	// piggybacked on outgoing data (default 10ms).
	AckDelay time.Duration
	// KeepAliveInterval is the interval of the keep-alive frames sent while
	// idle (default 1s).
	KeepAliveInterval time.Duration
	// LinkTimeout is the time without receiving anything from the peer after
	// which the link is declared dead (default 4s).
	LinkTimeout time.Duration

	// BaudRate is the baud rate of the port when the Link is created: every
	// session starts at this rate, and falls back to it if the negotiated
	// rate does not work (default 115200).
	BaudRate int
	// MaxBaudRate is the maximum baud rate that can be negotiated with the
	// peer. If 0 (the default) the baud rate is not negotiated.
	MaxBaudRate int
	// SetBaudRate changes the baud rate of the port. It must wait until all
	// the data written so far has been transmitted (e.g. calling Drain on a
	// go.bug.st/serial port). Required if MaxBaudRate is set.
	SetBaudRate func(rate int) error
}

func (c *Config) setDefaults() error {
	if c.MaxPayload == 0 {
		c.MaxPayload = 256
	}
	if c.Window == 0 {
		c.Window = 8
	}
	if c.RetransmitTimeout == 0 {
		c.RetransmitTimeout = 250 * time.Millisecond
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = 10
	}
	if c.AckDelay == 0 {
		c.AckDelay = 10 * time.Millisecond
	}
	if c.KeepAliveInterval == 0 {
		c.KeepAliveInterval = time.Second
	}
	if c.LinkTimeout == 0 {
		c.LinkTimeout = 4 * time.Second
	}
	if c.BaudRate == 0 {
		c.BaudRate = 115200
	}
	if c.MaxBaudRate > 0 && c.SetBaudRate == nil {
		return errors.New("seriallink: SetBaudRate is required to negotiate the baud rate")
	}
	if c.MaxPayload < 1 || c.MaxPayload > MaxPayloadLimit {
		return fmt.Errorf("seriallink: invalid max payload %d (must be 1..%d)", c.MaxPayload, MaxPayloadLimit)
	}
	if c.Window < 1 || c.Window > MaxWindowLimit {
		return fmt.Errorf("seriallink: invalid window %d (must be 1..%d)", c.Window, MaxWindowLimit)
	}
	if c.LinkTimeout <= c.KeepAliveInterval {
		return errors.New("seriallink: link timeout must be larger than keep-alive interval")
	}
	return nil
}

// Stats contains link counters, useful for diagnostics.
type Stats struct {
	TxFrames           uint64 `msgpack:"tx_frames"`           // frames written to the port (any type)
	TxDataFrames       uint64 `msgpack:"tx_data_frames"`      // DATA frames written to the port (retransmissions included)
	TxAckedFrames      uint64 `msgpack:"tx_acked_frames"`     // DATA frames acknowledged by the peer (successfully transmitted)
	TxAckedBytes       uint64 `msgpack:"tx_acked_bytes"`      // stream bytes acknowledged by the peer
	TxNaks             uint64 `msgpack:"tx_naks"`             // NAK frames queued for sending
	Retransmits        uint64 `msgpack:"retransmits"`         // DATA frames sent again
	RetransmitTimeouts uint64 `msgpack:"retransmit_timeouts"` // retransmission timer expirations
	RxFrames           uint64 `msgpack:"rx_frames"`           // valid frames received (any type)
	RxDataFrames       uint64 `msgpack:"rx_data_frames"`      // DATA frames accepted in order (successfully received)
	RxDataBytes        uint64 `msgpack:"rx_data_bytes"`       // stream bytes accepted
	RxDuplicates       uint64 `msgpack:"rx_duplicates"`       // DATA frames dropped because already received
	RxOutOfOrder       uint64 `msgpack:"rx_out_of_order"`     // DATA frames dropped because a previous one was lost
	RxNaks             uint64 `msgpack:"rx_naks"`             // NAK frames received
	RxErrors           uint64 `msgpack:"rx_errors"`           // frames dropped because corrupted (sum of the following)
	RxCRCErrors        uint64 `msgpack:"rx_crc_errors"`       // frames with a CRC mismatch
	RxFramingErrors    uint64 `msgpack:"rx_framing_errors"`   // frames with a bad COBS encoding, too short or too long
	RxProtocolErrors   uint64 `msgpack:"rx_protocol_errors"`  // frames with an unknown protocol version or frame type
	BaudRateSwitches   uint64 `msgpack:"baud_rate_switches"`  // proven switches to the negotiated baud rate
	BaudRateFailures   uint64 `msgpack:"baud_rate_failures"`  // failed attempts to switch to the negotiated baud rate
	BaudRateFallbacks  uint64 `msgpack:"baud_rate_fallbacks"` // fallbacks to the base rate after a silence at the proven negotiated rate
}

// Add adds the counters of o to s.
func (s *Stats) Add(o Stats) {
	s.TxFrames += o.TxFrames
	s.TxDataFrames += o.TxDataFrames
	s.TxAckedFrames += o.TxAckedFrames
	s.TxAckedBytes += o.TxAckedBytes
	s.TxNaks += o.TxNaks
	s.Retransmits += o.Retransmits
	s.RetransmitTimeouts += o.RetransmitTimeouts
	s.RxFrames += o.RxFrames
	s.RxDataFrames += o.RxDataFrames
	s.RxDataBytes += o.RxDataBytes
	s.RxDuplicates += o.RxDuplicates
	s.RxOutOfOrder += o.RxOutOfOrder
	s.RxNaks += o.RxNaks
	s.RxErrors += o.RxErrors
	s.RxCRCErrors += o.RxCRCErrors
	s.RxFramingErrors += o.RxFramingErrors
	s.RxProtocolErrors += o.RxProtocolErrors
	s.BaudRateSwitches += o.BaudRateSwitches
	s.BaudRateFailures += o.BaudRateFailures
	s.BaudRateFallbacks += o.BaudRateFallbacks
}

type linkState int

const (
	stateConnecting linkState = iota
	stateEstablished
	stateClosed
)

// baudPhase is the state of the switch to the negotiated baud rate.
type baudPhase int

const (
	baudBase baudPhase = iota // at the base rate
	baudFast                  // at the negotiated rate (or switching to it)
)

// Link is a reliable byte stream over an unreliable serial port.
type Link struct {
	port io.ReadWriteCloser
	cfg  Config

	writeMu sync.Mutex // serializes Write calls, so a Write is never interleaved with another

	mu    sync.Mutex
	cond  *sync.Cond // broadcast on every change that may unblock Read, Write or the sender
	state linkState
	err   error // why the link has been closed

	nonce       uint32
	peer        synParams
	peerKnown   bool // we received (and answered) a SYN from the current peer
	peerActive  bool // the peer sent DATA/ACK/NAK in this session (so it is established)
	maxPayload  int  // negotiated
	sendWindow  int  // negotiated
	synDeadline time.Time

	// transmitter
	outq       []frame // frames waiting for the sender goroutine
	queuedData int     // number of DATA frames in outq
	unacked    []frame // DATA frames sent and not yet acknowledged, unacked[0] is the oldest
	nextSeq    uint8
	peerWnd    int
	retries    int
	rtoStart   time.Time // retransmission timer start
	lastTx     time.Time
	lastRst    time.Time

	// baud rate negotiation
	baudTarget     int       // negotiated rate, 0 if none
	baudPhase      baudPhase //
	baudSwitchedAt time.Time // baudFast: when the port switched to the negotiated rate, zero if not yet
	baudProven     bool      // baudFast: the peer has been heard at the negotiated rate
	baudFailures   int       // consecutive failed switches
	baudRate       int       // current port rate

	// receiver
	recvBuf    bytes.Buffer
	recvCap    int
	expected   uint8
	ackPending int // DATA frames received and not yet acknowledged
	ackDue     time.Time
	nakSent    bool
	lastAdvWnd int
	lastRx     time.Time

	stats Stats

	kick       chan struct{} // wakes up the timer goroutine
	done       chan struct{} // closed when the link is closed
	senderDone chan struct{}
	readerDone chan struct{}
	closeOnce  sync.Once
	closeErr   error
}

// New creates a Link over the given port and starts the session handshake.
// The Link takes ownership of the port: closing the Link closes the port.
func New(port io.ReadWriteCloser, cfg Config) (*Link, error) {
	if err := cfg.setDefaults(); err != nil {
		return nil, err
	}
	now := time.Now()
	l := &Link{
		port:        port,
		cfg:         cfg,
		state:       stateConnecting,
		nonce:       newNonce(),
		maxPayload:  cfg.MaxPayload,
		recvCap:     cfg.Window * cfg.MaxPayload,
		baudRate:    cfg.BaudRate,
		synDeadline: now,
		lastTx:      now,
		lastRx:      now,
		kick:        make(chan struct{}, 1),
		done:        make(chan struct{}),
		senderDone:  make(chan struct{}),
		readerDone:  make(chan struct{}),
	}
	l.cond = sync.NewCond(&l.mu)
	go l.sendLoop()
	go l.readLoop()
	go l.timerLoop()
	return l, nil
}

// Read reads stream bytes received from the peer. It blocks until the session
// is established and some data is available.
func (l *Link) Read(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.recvBuf.Len() == 0 && l.state != stateClosed {
		l.cond.Wait()
	}
	if l.recvBuf.Len() == 0 {
		return 0, l.err
	}
	n, _ := l.recvBuf.Read(p)

	// Send a window update if the peer may be (almost) stalled
	if l.state == stateEstablished && l.recvCredit()-l.lastAdvWnd >= max(1, l.sendWindow/2) {
		l.enqueueAck()
	}
	return n, nil
}

// Write sends p to the peer. It blocks until the session is established and
// all the data has been sent within the peer receive window (it does not wait
// for the data to be acknowledged).
func (l *Link) Write(p []byte) (int, error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()

	n := 0
	for len(p) > 0 {
		for l.state == stateConnecting || (l.state == stateEstablished && len(l.unacked) >= min(l.peerWnd, l.sendWindow)) {
			l.cond.Wait()
		}
		if l.state != stateEstablished {
			return n, l.err
		}
		size := min(len(p), l.maxPayload)
		f := frame{typ: frameData, seq: l.nextSeq, payload: bytes.Clone(p[:size])}
		l.nextSeq++
		l.unacked = append(l.unacked, f)
		l.enqueue(f)
		p = p[size:]
		n += size
	}
	return n, nil
}

// Close terminates the session (notifying the peer) and closes the port.
func (l *Link) Close() error {
	l.mu.Lock()
	l.terminate(ErrClosed, l.state == stateEstablished)
	l.mu.Unlock()
	l.closeOnce.Do(func() {
		// Give the sender a chance to flush the RST
		select {
		case <-l.senderDone:
		case <-time.After(100 * time.Millisecond):
		}
		l.closeErr = l.port.Close()
		<-l.senderDone
		<-l.readerDone
	})
	return l.closeErr
}

// Done returns a channel that is closed when the link terminates.
func (l *Link) Done() <-chan struct{} {
	return l.done
}

// BaudRate returns the current baud rate of the port.
func (l *Link) BaudRate() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.baudRate
}

// Stats returns the link counters.
func (l *Link) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats
}

// terminate closes the session, if sendRst is true a RST is sent to the peer.
// Must be called with l.mu held.
func (l *Link) terminate(err error, sendRst bool) {
	if l.state == stateClosed {
		return
	}
	if !errors.Is(err, ErrClosed) {
		slog.Debug("Serial link terminated", "err", err)
	}
	l.state = stateClosed
	l.err = err
	l.outq = nil
	l.queuedData = 0
	l.unacked = nil
	if sendRst {
		l.outq = append(l.outq, l.rstFrame())
	}
	close(l.done)
	l.cond.Broadcast()
}

// enqueue queues a frame for the sender goroutine. Must be called with l.mu held.
func (l *Link) enqueue(f frame) {
	l.outq = append(l.outq, f)
	if f.typ == frameData {
		l.queuedData++
	}
	l.cond.Broadcast()
	l.wake()
}

// enqueueAck queues an ACK, unless another frame is already queued: since ack
// and wnd are filled when a frame is actually sent, any frame carries an
// up-to-date acknowledgement. Must be called with l.mu held.
func (l *Link) enqueueAck() {
	if len(l.outq) == 0 {
		l.enqueue(frame{typ: frameAck})
	}
}

func (l *Link) rstFrame() frame {
	return frame{typ: frameRst, payload: binary.LittleEndian.AppendUint32(nil, l.nonce)}
}

func (l *Link) localParams() synParams {
	return synParams{
		maxPayload: l.cfg.MaxPayload,
		window:     l.cfg.Window,
		nonce:      l.nonce,
		baudRates:  baudRateMask(l.cfg.BaudRate, l.cfg.MaxBaudRate),
	}
}

// recvCredit returns how many full DATA frames we can still buffer. Must be
// called with l.mu held.
func (l *Link) recvCredit() int {
	return min((l.recvCap-l.recvBuf.Len())/l.maxPayload, MaxWindowLimit)
}

// wake wakes up the timer goroutine, to recompute the deadlines.
func (l *Link) wake() {
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

// retransmit queues again all the unacknowledged DATA frames, in order (Go-Back-N).
// Must be called with l.mu held.
func (l *Link) retransmit() {
	if len(l.unacked) == 0 {
		return
	}
	// Drop the DATA frames already in the queue: they are queued again below
	l.dropQueuedData()
	for _, f := range l.unacked {
		l.enqueue(f)
	}
	l.stats.Retransmits += uint64(len(l.unacked))
}

// dropQueuedData removes the DATA frames from the output queue (they are
// still in unacked). Must be called with l.mu held.
func (l *Link) dropQueuedData() {
	q := l.outq[:0]
	for _, f := range l.outq {
		if f.typ != frameData {
			q = append(q, f)
		}
	}
	l.outq = q
	l.queuedData = 0
}

// isAcked returns true if the DATA frame with the given seq has already been
// acknowledged. Must be called with l.mu held.
func (l *Link) isAcked(seq uint8) bool {
	base := l.nextSeq - uint8(len(l.unacked))
	return int(seq-base) >= len(l.unacked)
}

func (l *Link) sendLoop() {
	defer close(l.senderDone)
	var buf []byte
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		for len(l.outq) == 0 && l.state != stateClosed {
			l.cond.Wait()
		}
		if len(l.outq) == 0 {
			return
		}
		f := l.outq[0]
		l.outq = l.outq[1:]
		if f.typ == frameSetBaud {
			l.applyBaudRate(f.baud)
			continue
		}
		switch f.typ {
		case frameData:
			l.queuedData--
			if l.isAcked(f.seq) {
				continue // acknowledged while waiting in the queue
			}
			fallthrough
		case frameAck, frameNak:
			f.ack = l.expected
			f.wnd = uint8(l.recvCredit())
			l.lastAdvWnd = int(f.wnd)
			l.ackPending = 0
		}
		now := time.Now()
		l.lastTx = now
		buf = buf[:0]
		if l.needsDelimiter() {
			buf = append(buf, 0)
		}
		l.mu.Unlock()

		buf = f.appendEncoded(buf)
		_, err := l.port.Write(buf)

		l.mu.Lock()
		if err != nil {
			l.terminate(fmt.Errorf("seriallink: port write: %w", err), false)
			continue
		}
		l.stats.TxFrames++
		if f.typ == frameData {
			l.stats.TxDataFrames++
			l.rtoStart = time.Now()
		}
		l.wake()
	}
}

// applyBaudRate changes the port baud rate. Must be called by the sender
// goroutine with l.mu held.
func (l *Link) applyBaudRate(rate int) {
	l.mu.Unlock()
	err := l.cfg.SetBaudRate(rate)
	l.mu.Lock()
	if err != nil {
		l.terminate(fmt.Errorf("seriallink: setting baud rate %d: %w", rate, err), false)
		return
	}
	l.baudRate = rate
	if l.baudPhase == baudFast && rate == l.baudTarget {
		l.baudSwitchedAt = time.Now()
	}
	l.wake()
}

func (l *Link) readLoop() {
	defer close(l.readerDone)
	maxEncoded := maxEncodedFrameSize(max(l.cfg.MaxPayload, controlPayloadLimit))
	buf := make([]byte, 1024)
	acc := make([]byte, 0, maxEncoded)
	discarding := false
	for {
		n, err := l.port.Read(buf)
		for _, b := range buf[:n] {
			if b != 0 {
				if !discarding {
					acc = append(acc, b)
					if len(acc) > maxEncoded {
						// Too long, a delimiter was lost: skip until the next one
						discarding = true
						acc = acc[:0]
					}
				}
				continue
			}
			if discarding || len(acc) > 0 {
				l.receive(acc, discarding)
			}
			acc = acc[:0]
			discarding = false
		}
		if err != nil {
			l.mu.Lock()
			l.terminate(fmt.Errorf("seriallink: port read: %w", err), false)
			l.mu.Unlock()
			return
		}
	}
}

func (l *Link) receive(encoded []byte, discarded bool) {
	var f frame
	err := cobs.ErrInvalid
	if !discarded {
		f, err = decodeFrame(encoded)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		l.stats.RxErrors++
		switch {
		case errors.Is(err, errFrameCRC):
			l.stats.RxCRCErrors++
		case errors.Is(err, errFrameVersion), errors.Is(err, errFrameType):
			l.stats.RxProtocolErrors++
		default:
			l.stats.RxFramingErrors++
		}
		return
	}
	l.stats.RxFrames++
	l.handleFrame(f, time.Now())
	l.wake()
}

// handleFrame processes a valid incoming frame. Must be called with l.mu held.
func (l *Link) handleFrame(f frame, now time.Time) {
	l.handleSessionFrame(f, now)
	if l.state == stateEstablished {
		l.onValidFrameBaud(now)
	}
}

func (l *Link) handleSessionFrame(f frame, now time.Time) {
	switch l.state {
	case stateConnecting:
		switch f.typ {
		case frameSyn:
			p, ok := parseSynParams(f.payload, false)
			if !ok {
				return
			}
			l.peer, l.peerKnown = p, true
			l.sendSynAck()
		case frameSynAck:
			p, ok := parseSynParams(f.payload, true)
			if !ok || p.echo != l.nonce {
				return // stale or for someone else
			}
			l.peer, l.peerKnown = p, true
			l.establish(now)
			l.sendSynAck() // let the peer complete the handshake too
		case frameData, frameAck, frameNak:
			if l.peerKnown {
				// The peer got our SYN_ACK and is already established
				l.establish(now)
				l.handleEstablished(f, now)
			} else if now.Sub(l.lastRst) >= l.cfg.RetransmitTimeout {
				// The peer is talking to a previous session
				l.lastRst = now
				l.enqueue(l.rstFrame())
			}
		}
	case stateEstablished:
		l.lastRx = now
		switch f.typ {
		case frameSyn:
			p, ok := parseSynParams(f.payload, false)
			if !ok {
				return
			}
			if p.nonce == l.peer.nonce && !l.peerActive {
				l.sendSynAck() // our SYN_ACK was lost
			} else {
				// The peer restarted. A peer sends SYN only until it is
				// established, so a SYN after DATA/ACK/NAK means a new session
				// even if the nonce is the same (e.g. a weak random source).
				l.terminate(ErrSessionReset, false)
			}
		case frameSynAck:
			// duplicate, ignore
		case frameRst:
			l.terminate(ErrSessionReset, false)
		default:
			l.handleEstablished(f, now)
		}
	}
}

func (l *Link) sendSynAck() {
	p := l.localParams()
	p.echo = l.peer.nonce
	l.enqueue(frame{typ: frameSynAck, payload: p.marshal(true)})
}

func (l *Link) establish(now time.Time) {
	l.state = stateEstablished
	l.maxPayload = min(l.cfg.MaxPayload, l.peer.maxPayload)
	l.sendWindow = min(l.cfg.Window, l.peer.window)
	l.peerWnd = l.peer.window
	l.lastRx = now
	l.baudTarget = highestBaudRate(baudRateMask(l.cfg.BaudRate, l.cfg.MaxBaudRate) & l.peer.baudRates)
	slog.Debug("Serial link established", "max_payload", l.maxPayload, "window", l.sendWindow, "baud_rate", l.baudTarget)
	l.cond.Broadcast()
}

// handleEstablished processes DATA, ACK and NAK frames. Must be called with l.mu held.
func (l *Link) handleEstablished(f frame, now time.Time) {
	l.peerActive = true

	// Process the (piggybacked) acknowledgement
	if n := int(f.ack - (l.nextSeq - uint8(len(l.unacked)))); n <= len(l.unacked) {
		if n > 0 {
			for _, acked := range l.unacked[:n] {
				l.stats.TxAckedBytes += uint64(len(acked.payload))
			}
			l.stats.TxAckedFrames += uint64(n)
			l.unacked = l.unacked[n:]
			l.retries = 0
			l.rtoStart = now
		}
		l.peerWnd = int(f.wnd)
		l.cond.Broadcast()
	}

	switch f.typ {
	case frameNak:
		l.stats.RxNaks++
		l.retransmit()
	case frameData:
		switch d := f.seq - l.expected; {
		case d == 0:
			if len(f.payload) > l.maxPayload || len(f.payload) > l.recvCap-l.recvBuf.Len() {
				return // the peer did not respect the negotiated limits
			}
			l.recvBuf.Write(f.payload)
			l.stats.RxDataFrames++
			l.stats.RxDataBytes += uint64(len(f.payload))
			l.expected++
			l.nakSent = false
			l.ackPending++
			if l.ackPending == 1 {
				l.ackDue = now.Add(l.cfg.AckDelay)
			}
			if l.ackPending >= max(1, l.sendWindow/2) || l.recvCredit() == 0 {
				l.enqueueAck()
			}
			l.cond.Broadcast()
		case d >= 128:
			// Duplicate: our ack has probably been lost
			l.stats.RxDuplicates++
			l.enqueueAck()
		default:
			// A frame has been lost: ask to go back (only once)
			l.stats.RxOutOfOrder++
			if !l.nakSent {
				l.nakSent = true
				l.stats.TxNaks++
				l.enqueue(frame{typ: frameNak})
			}
		}
	}
}

func (l *Link) timerLoop() {
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		l.mu.Lock()
		next := l.onTimer(time.Now())
		l.mu.Unlock()
		t.Reset(time.Until(next))
		select {
		case <-t.C:
		case <-l.kick:
		case <-l.done:
			return
		}
	}
}

// onTimer runs the expired timers and returns the next deadline. Must be
// called with l.mu held.
func (l *Link) onTimer(now time.Time) time.Time {
	next := now.Add(time.Hour)
	consider := func(d time.Time) {
		if d.Before(next) {
			next = d
		}
	}

	switch l.state {
	case stateConnecting:
		if !now.Before(l.synDeadline) {
			l.enqueue(frame{typ: frameSyn, payload: l.localParams().marshal(false)})
			l.synDeadline = now.Add(l.cfg.RetransmitTimeout)
		}
		consider(l.synDeadline)

	case stateEstablished:
		if now.Sub(l.lastRx) >= l.cfg.LinkTimeout {
			l.terminate(ErrLinkTimeout, true)
			return next
		}
		consider(l.lastRx.Add(l.cfg.LinkTimeout))

		if d, ok := l.onTimerBaud(now); !ok {
			return next
		} else if !d.IsZero() {
			consider(d)
		}

		if len(l.unacked) > 0 && l.queuedData == 0 {
			rto := l.rtoStart.Add(l.cfg.RetransmitTimeout)
			if !now.Before(rto) {
				l.stats.RetransmitTimeouts++
				if l.baudPhase != baudFast || l.baudProven {
					// While switching, the silence fallback bounds the retries
					l.retries++
				}
				if l.retries > l.cfg.MaxRetries {
					l.terminate(ErrLinkTimeout, true)
					return next
				}
				l.retransmit()
				rto = now.Add(l.cfg.RetransmitTimeout)
				l.rtoStart = now
			}
			consider(rto)
		}

		// The following timers only matter if the output queue is empty:
		// otherwise the next frame sent will carry ack and window anyway.
		if len(l.outq) == 0 && l.ackPending > 0 {
			if !now.Before(l.ackDue) {
				l.enqueueAck()
			} else {
				consider(l.ackDue)
			}
		}
		if len(l.outq) == 0 {
			ka := l.lastTx.Add(l.keepAliveInterval(now))
			if !now.Before(ka) {
				l.enqueueAck()
			} else {
				consider(ka)
			}
		}
	}
	return next
}
