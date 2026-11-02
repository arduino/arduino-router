// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package seriallink

import (
	"fmt"
	"log/slog"
	"time"
)

// Baud rate negotiation, see "Baud rate" in doc.go.

// baudSilenceTimeout is how long a side at the negotiated rate tolerates not
// receiving any valid frame before falling back to the base rate: until the
// switch is proven the keep-alive frames are sent every half retransmission
// timeout, so a failed switch is detected quickly, then a lost keep-alive
// frame must be tolerated.
func (s *session) baudSilenceTimeout() time.Duration {
	if !s.baudProven {
		return 2 * s.cfg.RetransmitTimeout
	}
	return 2*s.cfg.KeepAliveInterval + s.cfg.RetransmitTimeout
}

// keepAliveInterval is the interval of the keep-alive frames. Shortly after a
// switch they are sent every half retransmission timeout: until the switch is
// proven on both sides (the peer may be proven later), and while a peer that
// did not switch may fall back.
func (s *session) keepAliveInterval(now time.Time) time.Duration {
	if s.baudPhase == baudFast && (!s.baudProven || now.Sub(s.baudSwitchedAt) < 5*s.cfg.RetransmitTimeout) {
		return max(s.cfg.RetransmitTimeout/2, time.Millisecond)
	}
	return s.cfg.KeepAliveInterval
}

// baudProofDelay is how long after the switch a valid frame must be
// received to prove that the negotiated rate works in both directions: a peer
// that does not receive our frames falls back to the base rate after the
// silence timeout (plus a margin, because it may have switched a bit later),
// while a peer still at the negotiated rate after that time hears us. The
// frames received earlier only prove that we hear the peer, or may even have
// been sent at the base rate before the peer switched, and read late from the
// port buffers.
func (s *session) baudProofDelay() time.Duration {
	return 3 * s.cfg.RetransmitTimeout
}

// needsDelimiter returns true if the next frame must be preceded by an extra
// 0x00: the peer may have received garbage while the rates did not match,
// and the delimiter ends it, so this frame is not lost (the receiver ignores
// empty frames). It is needed until the switch is proven.
func (s *session) needsDelimiter() bool {
	return s.baudTarget != 0 && (s.baudPhase != baudFast || !s.baudProven)
}

// onValidFrameBaud is called after a valid frame has been processed in the
// established state.
func (s *session) onValidFrameBaud(now time.Time) {
	switch s.baudPhase {
	case baudBase:
		if s.baudTarget != 0 {
			s.startBaudSwitch()
		}
	case baudFast:
		if !s.baudProven && !s.baudSwitchedAt.IsZero() && now.Sub(s.baudSwitchedAt) >= s.baudProofDelay() {
			s.baudProven = true
			s.baudFailures = 0
			s.stats.BaudRateSwitches++
			slog.Debug("Serial link baud rate changed", "baud_rate", s.baudRate)
		}
	}
}

// startBaudSwitch switches to the negotiated rate. The peer is at the base
// rate (we just received a valid frame): answer it first, so that it
// switches too. The answer is sent twice: if it is lost, the switch fails.
func (s *session) startBaudSwitch() {
	if n := len(s.outq); n > 0 && s.outq[n-1].typ == frameSynAck {
		s.sendSynAck() // already answered once
	} else {
		for range 2 {
			if s.peerActive {
				s.enqueue(frame{typ: frameAck})
			} else {
				s.sendSynAck() // the peer may not be established yet
			}
		}
	}
	s.enqueue(frame{typ: frameSetBaud, baud: s.baudTarget})
	s.baudPhase = baudFast
	s.baudProven = false
	s.baudSwitchedAt = time.Time{} // set when the rate is applied
}

// onTimerBaud runs the baud rate timer, it returns the next deadline (zero
// if none) and false if the link has been terminated.
func (s *session) onTimerBaud(now time.Time) (time.Time, bool) {
	if s.baudPhase != baudFast || s.baudSwitchedAt.IsZero() {
		return time.Time{}, true
	}
	d := s.lastRx
	if d.Before(s.baudSwitchedAt) {
		d = s.baudSwitchedAt
	}
	d = d.Add(s.baudSilenceTimeout())
	if !now.Before(d) {
		return time.Time{}, s.baudFallback(now)
	}
	return d, true
}

// baudFallback goes back to the base rate after a silence at the negotiated
// rate: the switch failed (if not proven yet), or the peer restarted at the
// base rate, or the line is down. It returns false if the link has been
// terminated.
func (s *session) baudFallback(now time.Time) bool {
	s.baudPhase = baudBase
	setBase := frame{typ: frameSetBaud, baud: s.cfg.BaudRate}
	if s.baudProven {
		s.stats.BaudRateFallbacks++
		slog.Debug("Serial link silent, falling back to the base baud rate", "baud_rate", s.cfg.BaudRate)
	} else {
		s.stats.BaudRateFailures++
		s.baudFailures++
		slog.Debug("Serial link baud rate switch failed", "baud_rate", s.baudTarget, "failures", s.baudFailures)
		if s.baudFailures >= maxBaudRateFailures {
			slog.Warn("Serial link: the negotiated baud rate does not work, lower the maximum baud rate", "baud_rate", s.baudTarget)
			s.terminate(fmt.Errorf("%w (%d)", ErrBaudRate, s.baudTarget), false)
			s.outq = append(s.outq, setBase, s.rstFrame())
			return false
		}
	}
	s.enqueue(setBase)
	// If the peer is at the base rate too, this triggers a new switch
	s.enqueue(frame{typ: frameAck})
	// Send again what was lost at the negotiated rate
	s.retries = 0
	s.rtoStart = now
	s.retransmit()
	return true
}
