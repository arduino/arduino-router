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
// receiving any valid frame before falling back to the base rate.
func (l *Link) baudSilenceTimeout() time.Duration {
	return 2 * l.cfg.KeepAliveInterval
}

// baudProofDelay is how long after the switch a valid frame must be
// received to prove that the negotiated rate works: the frames received
// earlier may have been sent at the base rate, before the peer switched, and
// read late from the port buffers.
func (l *Link) baudProofDelay() time.Duration {
	return l.cfg.RetransmitTimeout
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
