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
	port    io.ReadWriteCloser
	session *session

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
	l := &Link{
		port:       port,
		session:    newSession(cfg),
		senderDone: make(chan struct{}),
		readerDone: make(chan struct{}),
	}
	go l.sendLoop()
	go l.readLoop()
	go l.timerLoop()
	return l, nil
}

// Read reads stream bytes received from the peer. It blocks until the session
// is established and some data is available.
func (l *Link) Read(p []byte) (int, error) {
	return l.session.Read(p)
}

// Write sends p to the peer. It blocks until the session is established and
// all the data has been sent within the peer receive window (it does not wait
// for the data to be acknowledged).
func (l *Link) Write(p []byte) (int, error) {
	return l.session.Write(p)
}

// Close terminates the session (notifying the peer) and closes the port.
func (l *Link) Close() error {
	l.session.Close()
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
	return l.session.done
}

// BaudRate returns the current baud rate of the port.
func (l *Link) BaudRate() int {
	return l.session.BaudRate()
}

// Stats returns the link counters.
func (l *Link) Stats() Stats {
	return l.session.Stats()
}

func (l *Link) sendLoop() {
	defer close(l.senderDone)
	var buf []byte
	for {
		f, delimiter, ok := l.session.NextFrame()
		if !ok {
			return
		}
		if f.typ == frameSetBaud {
			err := l.session.cfg.SetBaudRate(f.baud)
			l.session.BaudRateApplied(f.baud, err)
			continue
		}

		buf = buf[:0]
		if delimiter {
			buf = append(buf, 0)
		}
		buf = f.appendEncoded(buf)
		_, err := l.port.Write(buf)

		l.session.FrameSent(f, err)
	}
}

func (l *Link) readLoop() {
	defer close(l.readerDone)
	maxEncoded := maxEncodedFrameSize(max(l.session.cfg.MaxPayload, controlPayloadLimit))
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
			l.session.PortReadFailed(err)
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
	l.session.Receive(f, err)
}

func (l *Link) timerLoop() {
	t := time.NewTimer(time.Hour)
	defer t.Stop()
	for {
		t.Reset(time.Until(l.session.OnTimer()))
		select {
		case <-t.C:
		case <-l.session.kick:
		case <-l.session.done:
			return
		}
	}
}

// session is the protocol state machine of a Link.
//
// Locking rule: the methods starting with an uppercase letter take mu, and
// can be called from any goroutine. The methods starting with a lowercase
// letter must be called with mu held: they are helpers of the uppercase ones,
// and must not be called from outside session.
type session struct {
	// These do not change after the creation, and can be used without mu
	cfg  Config
	kick chan struct{} // wakes up the timer goroutine
	done chan struct{} // closed when the link is closed

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
	nakSent    bool      // a NAK has been sent and no DATA frame accepted since
	nakAt      time.Time // when the last NAK has been sent
	lastAdvWnd int
	lastRx     time.Time

	stats Stats
}

func newSession(cfg Config) *session {
	now := time.Now()
	s := &session{
		cfg:         cfg,
		kick:        make(chan struct{}, 1),
		done:        make(chan struct{}),
		state:       stateConnecting,
		nonce:       newNonce(),
		maxPayload:  cfg.MaxPayload,
		recvCap:     cfg.Window * cfg.MaxPayload,
		baudRate:    cfg.BaudRate,
		synDeadline: now,
		lastTx:      now,
		lastRx:      now,
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Read reads stream bytes received from the peer, see Link.Read.
func (s *session) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.recvBuf.Len() == 0 && s.state != stateClosed {
		s.cond.Wait()
	}
	if s.recvBuf.Len() == 0 {
		return 0, s.err
	}
	n, _ := s.recvBuf.Read(p)

	// Send a window update if the peer may be (almost) stalled
	if s.state == stateEstablished && s.recvCredit()-s.lastAdvWnd >= max(1, s.sendWindow/2) {
		s.enqueueAck()
	}
	return n, nil
}

// Write queues p as DATA frames, see Link.Write.
func (s *session) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	n := 0
	for len(p) > 0 {
		for s.state == stateConnecting || (s.state == stateEstablished && len(s.unacked) >= min(s.peerWnd, s.sendWindow)) {
			s.cond.Wait()
		}
		if s.state != stateEstablished {
			return n, s.err
		}
		size := min(len(p), s.maxPayload)
		f := frame{typ: frameData, seq: s.nextSeq, payload: bytes.Clone(p[:size])}
		s.nextSeq++
		s.unacked = append(s.unacked, f)
		s.enqueue(f)
		p = p[size:]
		n += size
	}
	return n, nil
}

// Close terminates the session, notifying the peer if it is established.
func (s *session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminate(ErrClosed, s.state == stateEstablished)
}

// PortReadFailed terminates the session after an error reading from the port.
func (s *session) PortReadFailed(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminate(fmt.Errorf("seriallink: port read: %w", err), false)
}

// BaudRate returns the current baud rate of the port.
func (s *session) BaudRate() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baudRate
}

// Stats returns the link counters.
func (s *session) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// NextFrame waits for the next frame to send, and returns it along with
// whether it must be preceded by a delimiter. It returns false when the
// session is closed and the output queue has been flushed.
func (s *session) NextFrame() (f frame, delimiter bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		for len(s.outq) == 0 && s.state != stateClosed {
			s.cond.Wait()
		}
		if len(s.outq) == 0 {
			return frame{}, false, false
		}
		if f, ok := s.dequeue(time.Now()); ok {
			return f, s.needsDelimiter(), true
		}
	}
}

// FrameSent is called by the sender goroutine after writing a frame to the port.
func (s *session) FrameSent(f frame, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.terminate(fmt.Errorf("seriallink: port write: %w", err), false)
		return
	}
	s.stats.TxFrames++
	if f.typ == frameData {
		s.stats.TxDataFrames++
		s.rtoStart = time.Now()
	}
	s.wake()
}

// BaudRateApplied is called by the sender goroutine after changing the port
// baud rate.
func (s *session) BaudRateApplied(rate int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.terminate(fmt.Errorf("seriallink: setting baud rate %d: %w", rate, err), false)
		return
	}
	s.baudRate = rate
	if s.baudPhase == baudFast && rate == s.baudTarget {
		s.baudSwitchedAt = time.Now()
	}
	s.wake()
}

// Receive processes an incoming frame, err is the decoding error.
func (s *session) Receive(f frame, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if err != nil {
		s.stats.RxErrors++
		switch {
		case errors.Is(err, errFrameVersion), errors.Is(err, errFrameType):
			s.stats.RxProtocolErrors++
			return // not a line error (e.g. a frame type of a future version)
		case errors.Is(err, errFrameCRC):
			s.stats.RxCRCErrors++
		default:
			s.stats.RxFramingErrors++
		}
		if s.state == stateEstablished {
			// It may have been a DATA frame: ask to go back
			s.sendNak(now)
		}
		return
	}
	s.stats.RxFrames++
	s.handleFrame(f, now)
	s.wake()
}

// OnTimer runs the expired timers and returns the next deadline.
func (s *session) OnTimer() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	next := now.Add(time.Hour)
	consider := func(d time.Time) {
		if d.Before(next) {
			next = d
		}
	}

	switch s.state {
	case stateConnecting:
		if !now.Before(s.synDeadline) {
			s.enqueue(frame{typ: frameSyn, payload: s.localParams().marshal(false)})
			s.synDeadline = now.Add(s.cfg.RetransmitTimeout)
		}
		consider(s.synDeadline)

	case stateEstablished:
		if now.Sub(s.lastRx) >= s.cfg.LinkTimeout {
			s.terminate(ErrLinkTimeout, true)
			return next
		}
		consider(s.lastRx.Add(s.cfg.LinkTimeout))

		if d, ok := s.onTimerBaud(now); !ok {
			return next
		} else if !d.IsZero() {
			consider(d)
		}

		if len(s.unacked) > 0 && s.queuedData == 0 {
			rto := s.rtoStart.Add(s.cfg.RetransmitTimeout)
			if !now.Before(rto) {
				s.stats.RetransmitTimeouts++
				if s.baudPhase != baudFast || s.baudProven {
					// While switching, the silence fallback bounds the retries
					s.retries++
				}
				if s.retries > s.cfg.MaxRetries {
					s.terminate(ErrLinkTimeout, true)
					return next
				}
				s.retransmit()
				rto = now.Add(s.cfg.RetransmitTimeout)
				s.rtoStart = now
			}
			consider(rto)
		}

		// The following timers only matter if the output queue is empty:
		// otherwise the next frame sent will carry ack and window anyway.
		if len(s.outq) == 0 && s.ackPending > 0 {
			if !now.Before(s.ackDue) {
				s.enqueueAck()
			} else {
				consider(s.ackDue)
			}
		}
		if len(s.outq) == 0 {
			ka := s.lastTx.Add(s.keepAliveInterval(now))
			if !now.Before(ka) {
				s.enqueueAck()
			} else {
				consider(ka)
			}
		}
	}
	return next
}

// terminate closes the session, if sendRst is true a RST is sent to the peer.
func (s *session) terminate(err error, sendRst bool) {
	if s.state == stateClosed {
		return
	}
	if !errors.Is(err, ErrClosed) {
		slog.Debug("Serial link terminated", "err", err)
	}
	s.state = stateClosed
	s.err = err
	s.outq = nil
	s.queuedData = 0
	s.unacked = nil
	if sendRst {
		s.outq = append(s.outq, s.rstFrame())
	}
	close(s.done)
	s.cond.Broadcast()
}

// enqueue queues a frame for the sender goroutine.
func (s *session) enqueue(f frame) {
	s.outq = append(s.outq, f)
	if f.typ == frameData {
		s.queuedData++
	}
	s.cond.Broadcast()
	s.wake()
}

// enqueueAck queues an ACK, unless another frame is already queued: since ack
// and wnd are filled when a frame is actually sent, any frame carries an
// up-to-date acknowledgement.
func (s *session) enqueueAck() {
	if len(s.outq) == 0 {
		s.enqueue(frame{typ: frameAck})
	}
}

func (s *session) rstFrame() frame {
	return frame{typ: frameRst, payload: binary.LittleEndian.AppendUint32(nil, s.nonce)}
}

func (s *session) localParams() synParams {
	return synParams{
		maxPayload: s.cfg.MaxPayload,
		window:     s.cfg.Window,
		nonce:      s.nonce,
		baudRates:  baudRateMask(s.cfg.BaudRate, s.cfg.MaxBaudRate),
	}
}

// recvCredit returns how many full DATA frames we can still buffer.
func (s *session) recvCredit() int {
	return min((s.recvCap-s.recvBuf.Len())/s.maxPayload, MaxWindowLimit)
}

// wake wakes up the timer goroutine, to recompute the deadlines.
func (s *session) wake() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// retransmit queues again all the unacknowledged DATA frames, in order (Go-Back-N).
func (s *session) retransmit() {
	if len(s.unacked) == 0 {
		return
	}
	// Drop the DATA frames already in the queue: they are queued again below
	s.dropQueuedData()
	for _, f := range s.unacked {
		s.enqueue(f)
	}
	s.stats.Retransmits += uint64(len(s.unacked))
}

// dropQueuedData removes the DATA frames from the output queue (they are
// still in unacked).
func (s *session) dropQueuedData() {
	q := s.outq[:0]
	for _, f := range s.outq {
		if f.typ != frameData {
			q = append(q, f)
		}
	}
	s.outq = q
	s.queuedData = 0
}

// isAcked returns true if the DATA frame with the given seq has already been
// acknowledged.
func (s *session) isAcked(seq uint8) bool {
	base := s.nextSeq - uint8(len(s.unacked))
	return int(seq-base) >= len(s.unacked)
}

// dequeue removes the next frame from the output queue and fills its
// acknowledgement. It returns false if the frame must not be sent anymore.
func (s *session) dequeue(now time.Time) (frame, bool) {
	f := s.outq[0]
	s.outq = s.outq[1:]
	switch f.typ {
	case frameSetBaud:
		return f, true
	case frameData:
		s.queuedData--
		if s.isAcked(f.seq) {
			return f, false // acknowledged while waiting in the queue
		}
		// Not queued anymore: the retransmission timer runs from now on
		s.rtoStart = now
		fallthrough
	case frameAck, frameNak:
		f.ack = s.expected
		f.wnd = uint8(s.recvCredit())
		s.lastAdvWnd = int(f.wnd)
		s.ackPending = 0
	}
	s.lastTx = now
	return f, true
}

// handleFrame processes a valid incoming frame.
func (s *session) handleFrame(f frame, now time.Time) {
	s.handleSessionFrame(f, now)
	if s.state == stateEstablished {
		s.onValidFrameBaud(now)
	}
}

func (s *session) handleSessionFrame(f frame, now time.Time) {
	switch s.state {
	case stateConnecting:
		switch f.typ {
		case frameSyn:
			p, ok := parseSynParams(f.payload, false)
			if !ok {
				return
			}
			s.peer, s.peerKnown = p, true
			s.sendSynAck()
		case frameSynAck:
			p, ok := parseSynParams(f.payload, true)
			if !ok || p.echo != s.nonce {
				return // stale or for someone else
			}
			s.peer, s.peerKnown = p, true
			s.establish(now)
			s.sendSynAck() // let the peer complete the handshake too
		case frameData, frameAck, frameNak:
			if s.peerKnown {
				// The peer got our SYN_ACK and is already established
				s.establish(now)
				s.handleEstablished(f, now)
			} else if now.Sub(s.lastRst) >= s.cfg.RetransmitTimeout {
				// The peer is talking to a previous session
				s.lastRst = now
				s.enqueue(s.rstFrame())
			}
		}
	case stateEstablished:
		s.lastRx = now
		switch f.typ {
		case frameSyn:
			p, ok := parseSynParams(f.payload, false)
			if !ok {
				return
			}
			if p.nonce == s.peer.nonce && !s.peerActive {
				s.sendSynAck() // our SYN_ACK was lost
			} else {
				// The peer restarted. A peer sends SYN only until it is
				// established, so a SYN after DATA/ACK/NAK means a new session
				// even if the nonce is the same (e.g. a weak random source).
				s.terminate(ErrSessionReset, false)
			}
		case frameSynAck:
			// duplicate, ignore
		case frameRst:
			s.terminate(ErrSessionReset, false)
		default:
			s.handleEstablished(f, now)
		}
	}
}

func (s *session) sendSynAck() {
	p := s.localParams()
	p.echo = s.peer.nonce
	s.enqueue(frame{typ: frameSynAck, payload: p.marshal(true)})
}

func (s *session) establish(now time.Time) {
	s.state = stateEstablished
	s.maxPayload = min(s.cfg.MaxPayload, s.peer.maxPayload)
	s.sendWindow = min(s.cfg.Window, s.peer.window)
	s.peerWnd = s.peer.window
	s.lastRx = now
	s.baudTarget = highestBaudRate(baudRateMask(s.cfg.BaudRate, s.cfg.MaxBaudRate) & s.peer.baudRates)
	slog.Debug("Serial link established", "max_payload", s.maxPayload, "window", s.sendWindow, "baud_rate", s.baudTarget)
	s.cond.Broadcast()
}

// handleEstablished processes DATA, ACK and NAK frames.
func (s *session) handleEstablished(f frame, now time.Time) {
	s.peerActive = true

	// Process the (piggybacked) acknowledgement
	if n := int(f.ack - (s.nextSeq - uint8(len(s.unacked)))); n <= len(s.unacked) {
		if n > 0 {
			for _, acked := range s.unacked[:n] {
				s.stats.TxAckedBytes += uint64(len(acked.payload))
			}
			s.stats.TxAckedFrames += uint64(n)
			s.unacked = s.unacked[n:]
			s.retries = 0
			s.rtoStart = now
		}
		s.peerWnd = int(f.wnd)
		s.cond.Broadcast()
	}

	switch f.typ {
	case frameNak:
		s.stats.RxNaks++
		s.retransmit()
	case frameData:
		switch d := f.seq - s.expected; {
		case d == 0:
			if len(f.payload) > s.maxPayload || len(f.payload) > s.recvCap-s.recvBuf.Len() {
				return // the peer did not respect the negotiated limits
			}
			s.recvBuf.Write(f.payload)
			s.stats.RxDataFrames++
			s.stats.RxDataBytes += uint64(len(f.payload))
			s.expected++
			s.nakSent = false
			s.ackPending++
			if s.ackPending == 1 {
				s.ackDue = now.Add(s.cfg.AckDelay)
			}
			if s.ackPending >= max(1, s.sendWindow/2) || s.recvCredit() == 0 {
				s.enqueueAck()
			}
			s.cond.Broadcast()
		case d >= 128:
			// Duplicate: our ack has probably been lost
			s.stats.RxDuplicates++
			s.enqueueAck()
		default:
			// A frame has been lost: ask to go back
			s.stats.RxOutOfOrder++
			s.sendNak(now)
		}
	}
}

// sendNak asks the peer to go back and retransmit from the expected frame.
// The NAK is sent once until a DATA frame is accepted (the rest of the window
// is coming out of order too), or again after a retransmission timeout (the
// NAK or the retransmission may have been lost, or the corrupted frame was not
// a DATA frame).
func (s *session) sendNak(now time.Time) {
	if s.nakSent && now.Sub(s.nakAt) < s.cfg.RetransmitTimeout {
		return
	}
	s.nakSent = true
	s.nakAt = now
	s.stats.TxNaks++
	s.enqueue(frame{typ: frameNak})
}
