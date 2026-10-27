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
func (l *Link) baudSilenceTimeout() time.Duration {
	if !l.baudProven {
		return 2 * l.cfg.RetransmitTimeout
	}
	return 2*l.cfg.KeepAliveInterval + l.cfg.RetransmitTimeout
}

// keepAliveInterval is the interval of the keep-alive frames. Shortly after a
// switch they are sent every half retransmission timeout: until the switch is
// proven on both sides (the peer may be proven later), and while a peer that
// did not switch may fall back.
func (l *Link) keepAliveInterval(now time.Time) time.Duration {
	if l.baudPhase == baudFast && (!l.baudProven || now.Sub(l.baudSwitchedAt) < 5*l.cfg.RetransmitTimeout) {
		return max(l.cfg.RetransmitTimeout/2, time.Millisecond)
	}
	return l.cfg.KeepAliveInterval
}

// baudProofDelay is how long after the switch a valid frame must be
// received to prove that the negotiated rate works in both directions: a peer
// that does not receive our frames falls back to the base rate after the
// silence timeout (plus a margin, because it may have switched a bit later),
// while a peer still at the negotiated rate after that time hears us. The
// frames received earlier only prove that we hear the peer, or may even have
// been sent at the base rate before the peer switched, and read late from the
// port buffers.
func (l *Link) baudProofDelay() time.Duration {
	return 3 * l.cfg.RetransmitTimeout
}

// needsDelimiter returns true if the next frame must be preceded by an extra
// 0x00: the peer may have received garbage while the rates did not match,
// and the delimiter ends it, so this frame is not lost (the receiver ignores
// empty frames). It is needed until the switch is proven. Must be called with
// l.mu held.
func (l *Link) needsDelimiter() bool {
	return l.baudTarget != 0 && (l.baudPhase != baudFast || !l.baudProven)
}

// onValidFrameBaud is called after a valid frame has been processed in the
// established state. Must be called with l.mu held.
func (l *Link) onValidFrameBaud(now time.Time) {
	switch l.baudPhase {
	case baudBase:
		if l.baudTarget != 0 {
			l.startBaudSwitch()
		}
	case baudFast:
		if !l.baudProven && !l.baudSwitchedAt.IsZero() && now.Sub(l.baudSwitchedAt) >= l.baudProofDelay() {
			l.baudProven = true
			l.baudFailures = 0
			l.stats.BaudRateSwitches++
			slog.Debug("Serial link baud rate changed", "baud_rate", l.baudRate)
		}
	}
}

// startBaudSwitch switches to the negotiated rate. The peer is at the base
// rate (we just received a valid frame): answer it first, so that it
// switches too. The answer is sent twice: if it is lost, the switch fails.
// Must be called with l.mu held.
func (l *Link) startBaudSwitch() {
	if n := len(l.outq); n > 0 && l.outq[n-1].typ == frameSynAck {
		l.sendSynAck() // already answered once
	} else {
		for range 2 {
			if l.peerActive {
				l.enqueue(frame{typ: frameAck})
			} else {
				l.sendSynAck() // the peer may not be established yet
			}
		}
	}
	l.enqueue(frame{typ: frameSetBaud, baud: l.baudTarget})
	l.baudPhase = baudFast
	l.baudProven = false
	l.baudSwitchedAt = time.Time{} // set when the rate is applied
}

// onTimerBaud runs the baud rate timer, it returns the next deadline (zero
// if none) and false if the link has been terminated. Must be called with
// l.mu held.
func (l *Link) onTimerBaud(now time.Time) (time.Time, bool) {
	if l.baudPhase != baudFast || l.baudSwitchedAt.IsZero() {
		return time.Time{}, true
	}
	d := l.lastRx
	if d.Before(l.baudSwitchedAt) {
		d = l.baudSwitchedAt
	}
	d = d.Add(l.baudSilenceTimeout())
	if !now.Before(d) {
		return time.Time{}, l.baudFallback(now)
	}
	return d, true
}

// baudFallback goes back to the base rate after a silence at the negotiated
// rate: the switch failed (if not proven yet), or the peer restarted at the
// base rate, or the line is down. It returns false if the link has been
// terminated. Must be called with l.mu held.
func (l *Link) baudFallback(now time.Time) bool {
	l.baudPhase = baudBase
	setBase := frame{typ: frameSetBaud, baud: l.cfg.BaudRate}
	if l.baudProven {
		l.stats.BaudRateFallbacks++
		slog.Debug("Serial link silent, falling back to the base baud rate", "baud_rate", l.cfg.BaudRate)
	} else {
		l.stats.BaudRateFailures++
		l.baudFailures++
		slog.Debug("Serial link baud rate switch failed", "baud_rate", l.baudTarget, "failures", l.baudFailures)
		if l.baudFailures >= maxBaudRateFailures {
			slog.Warn("Serial link: the negotiated baud rate does not work, lower the maximum baud rate", "baud_rate", l.baudTarget)
			l.terminate(fmt.Errorf("%w (%d)", ErrBaudRate, l.baudTarget), false)
			l.outq = append(l.outq, setBase, l.rstFrame())
			return false
		}
	}
	l.enqueue(setBase)
	// If the peer is at the base rate too, this triggers a new switch
	l.enqueue(frame{typ: frameAck})
	// Send again what was lost at the negotiated rate
	l.retries = 0
	l.rtoStart = now
	l.retransmit()
	return true
}
