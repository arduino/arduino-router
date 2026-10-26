// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package noisyline simulates an unreliable serial line, for testing.
//
// A Line connects two sides, A and B. Each side opens a Port, an
// io.ReadWriteCloser that behaves like a serial port: what is written on one
// side can be read from the other one, after going through a Wire that may
// corrupt it. Faults are injected independently on each byte, with
// configurable probabilities, using a seeded random generator so that test
// runs are reproducible (as long as the writes happen in the same order).
//
// A Wire can also be turned into a blackhole, dropping everything, to
// simulate a disconnected or unresponsive peer. Closing a Port does not
// affect the Line: a new Port can be opened on the same side, to simulate a
// device reboot or a port being reopened. Bytes in flight are kept and will be
// read by the new Port, as it would happen with a real UART buffer.
//
// Each side has a baud rate, set to DefaultBaudRate when a Port is opened and
// changed with Port.SetBaudRate. Bytes written while the two sides are at
// different rates are received as garbage, as it happens on a real line.
// The line has no transmission delay: a rate change takes effect after the
// bytes already written, as if the port had been drained.
package noisyline

import (
	"errors"
	"io"
	"math/rand/v2"
	"sync"
	"sync/atomic"
)

// DefaultBaudRate is the baud rate of a newly opened Port.
const DefaultBaudRate = 115200

// Faults are the probabilities, in the [0,1] range, of each fault being
// injected on a byte going through a Wire.
type Faults struct {
	BitFlip   float64 // probability of flipping a random bit of the byte
	Drop      float64 // probability of dropping the byte
	Duplicate float64 // probability of delivering the byte twice
	Garbage   float64 // probability of inserting a random byte before it
}

// Stats contains the counters of the bytes that went through a Wire.
type Stats struct {
	Bytes      int // bytes written into the wire
	Flipped    int
	Dropped    int
	Duplicated int
	Garbage    int
	Blackholed int // bytes discarded because the wire was a blackhole
	Mismatched int // bytes turned into garbage because the two sides are at different baud rates
}

// Wire is one direction of a Line.
type Wire struct {
	mu        sync.Mutex
	cond      *sync.Cond
	buf       []byte
	rng       *rand.Rand
	faults    Faults
	blackhole bool
	stats     Stats

	txRate, rxRate *atomic.Int64 // baud rates of the two sides
}

func newWire(seed1, seed2 uint64, txRate, rxRate *atomic.Int64) *Wire {
	w := &Wire{rng: rand.New(rand.NewPCG(seed1, seed2)), txRate: txRate, rxRate: rxRate}
	w.cond = sync.NewCond(&w.mu)
	return w
}

// SetFaults sets the fault probabilities of the wire.
func (w *Wire) SetFaults(f Faults) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.faults = f
}

// SetBlackhole makes the wire drop everything written into it (if true) or
// work normally (if false).
func (w *Wire) SetBlackhole(blackhole bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blackhole = blackhole
}

// Stats returns the counters of the wire.
func (w *Wire) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

func (w *Wire) send(p []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.Bytes += len(p)
	if w.blackhole {
		w.stats.Blackholed += len(p)
		return
	}
	if w.txRate.Load() != w.rxRate.Load() {
		// A UART sampling at the wrong rate gets garbage (and a different
		// number of bytes, here simplified to the same number)
		for range p {
			w.buf = append(w.buf, byte(w.rng.Uint32()))
		}
		w.stats.Mismatched += len(p)
		w.cond.Broadcast()
		return
	}
	f := w.faults
	for _, b := range p {
		if f.Garbage > 0 && w.rng.Float64() < f.Garbage {
			w.buf = append(w.buf, byte(w.rng.Uint32()))
			w.stats.Garbage++
		}
		if f.Drop > 0 && w.rng.Float64() < f.Drop {
			w.stats.Dropped++
			continue
		}
		if f.BitFlip > 0 && w.rng.Float64() < f.BitFlip {
			b ^= 1 << w.rng.IntN(8)
			w.stats.Flipped++
		}
		w.buf = append(w.buf, b)
		if f.Duplicate > 0 && w.rng.Float64() < f.Duplicate {
			w.buf = append(w.buf, b)
			w.stats.Duplicated++
		}
	}
	w.cond.Broadcast()
}

// Line is a simulated bidirectional serial line between side A and side B.
type Line struct {
	ab, ba       *Wire
	rateA, rateB atomic.Int64
}

// New creates a new Line without faults. The seed initializes the random
// generators used for fault injection.
func New(seed uint64) *Line {
	l := &Line{}
	l.rateA.Store(DefaultBaudRate)
	l.rateB.Store(DefaultBaudRate)
	l.ab = newWire(seed, 1, &l.rateA, &l.rateB)
	l.ba = newWire(seed, 2, &l.rateB, &l.rateA)
	return l
}

// AtoB returns the wire carrying data from side A to side B.
func (l *Line) AtoB() *Wire { return l.ab }

// BtoA returns the wire carrying data from side B to side A.
func (l *Line) BtoA() *Wire { return l.ba }

// SetFaults sets the same fault probabilities on both directions.
func (l *Line) SetFaults(f Faults) {
	l.ab.SetFaults(f)
	l.ba.SetFaults(f)
}

// SetBlackhole sets the blackhole state of both directions.
func (l *Line) SetBlackhole(blackhole bool) {
	l.ab.SetBlackhole(blackhole)
	l.ba.SetBlackhole(blackhole)
}

// OpenA opens a port on side A, at DefaultBaudRate.
func (l *Line) OpenA() *Port {
	l.rateA.Store(DefaultBaudRate)
	return &Port{rx: l.ba, tx: l.ab, rate: &l.rateA}
}

// OpenB opens a port on side B, at DefaultBaudRate.
func (l *Line) OpenB() *Port {
	l.rateB.Store(DefaultBaudRate)
	return &Port{rx: l.ab, tx: l.ba, rate: &l.rateB}
}

// Port is one side of a Line. Write never blocks, Read blocks until some data
// is available or the port is closed.
type Port struct {
	rx, tx *Wire
	rate   *atomic.Int64
	closed bool // protected by rx.mu
}

// SetBaudRate changes the baud rate of this side of the line.
func (p *Port) SetBaudRate(rate int) error {
	p.rx.mu.Lock()
	defer p.rx.mu.Unlock()
	if p.closed {
		return errors.New("noisyline: port closed")
	}
	p.rate.Store(int64(rate))
	return nil
}

// BaudRate returns the baud rate of this side of the line.
func (p *Port) BaudRate() int {
	return int(p.rate.Load())
}

// Read reads the data received from the other side.
func (p *Port) Read(b []byte) (int, error) {
	p.rx.mu.Lock()
	defer p.rx.mu.Unlock()
	for len(p.rx.buf) == 0 && !p.closed {
		p.rx.cond.Wait()
	}
	if p.closed {
		return 0, io.EOF
	}
	n := copy(b, p.rx.buf)
	p.rx.buf = p.rx.buf[n:]
	return n, nil
}

// Write sends data to the other side, through the wire.
func (p *Port) Write(b []byte) (int, error) {
	p.rx.mu.Lock()
	closed := p.closed
	p.rx.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	p.tx.send(b)
	return len(b), nil
}

// Close closes the port, unblocking any pending Read.
func (p *Port) Close() error {
	p.rx.mu.Lock()
	defer p.rx.mu.Unlock()
	p.closed = true
	p.rx.cond.Broadcast()
	return nil
}
