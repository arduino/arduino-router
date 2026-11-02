// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/arduino/arduino-router/msgpackrpc"
)

// rateMax is the rate value meaning "as fast as possible".
const rateMax = -1

// pacer schedules events at an adjustable rate (events per second).
type pacer struct {
	rate    atomic.Int64 // 0 = stopped, rateMax = no pacing
	changed chan struct{}
	next    time.Time
}

func newPacer(rate int64) *pacer {
	p := &pacer{changed: make(chan struct{}, 1)}
	p.rate.Store(rate)
	return p
}

func (p *pacer) setRate(rate int64) {
	p.rate.Store(rate)
	select {
	case p.changed <- struct{}{}:
	default:
	}
}

// wait blocks until the next event is due, it returns false if ctx is done.
func (p *pacer) wait(ctx context.Context) bool {
	for {
		rate := p.rate.Load()
		if rate == rateMax {
			p.next = time.Now()
			return ctx.Err() == nil
		}
		if rate <= 0 {
			select {
			case <-ctx.Done():
				return false
			case <-p.changed:
				p.next = time.Now()
				continue
			}
		}

		interval := time.Second / time.Duration(rate)
		now := time.Now()
		// Allow a small catch-up to keep the average rate accurate, but
		// avoid bursts after a long stall.
		if now.Sub(p.next) > interval+50*time.Millisecond {
			p.next = now
		}
		if d := p.next.Sub(now); d > 0 {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				t.Stop()
				return false
			case <-p.changed:
				t.Stop()
				p.next = time.Now()
				continue
			case <-t.C:
			}
		}
		p.next = p.next.Add(interval)
		return true
	}
}

// latencyStats accumulates request latencies.
type latencyStats struct {
	Count         uint64
	Sum, Min, Max time.Duration
}

func (l *latencyStats) add(d time.Duration) {
	if l.Count == 0 || d < l.Min {
		l.Min = d
	}
	if d > l.Max {
		l.Max = d
	}
	l.Sum += d
	l.Count++
}

func (l latencyStats) avg() time.Duration {
	if l.Count == 0 {
		return 0
	}
	return l.Sum / time.Duration(l.Count)
}

// clientCounters are the counters of the traffic seen by this client.
type clientCounters struct {
	EchoSent, EchoOK, EchoErrors, EchoTimeouts, EchoMismatches uint64
	NullSent, NullErrors                                       uint64
	PayloadRx, PayloadBytes, PayloadLost, PayloadCorrupted     uint64
	Pings                                                      uint64
	Latency                                                    latencyStats
}

// clientStats is the thread-safe container of the client counters.
type clientStats struct {
	mu          sync.Mutex
	c           clientCounters
	window      latencyStats // latencies since the last takeWindow
	lastPayload int          // first byte of the last payload received, -1 if none
}

func newClientStats() *clientStats {
	return &clientStats{lastPayload: -1}
}

func (s *clientStats) update(f func(c *clientCounters)) {
	s.mu.Lock()
	f(&s.c)
	s.mu.Unlock()
}

func (s *clientStats) snapshot() clientCounters {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c
}

// takeWindow returns the latencies collected since the previous call.
func (s *clientStats) takeWindow() latencyStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.window
	s.window = latencyStats{}
	return w
}

func (s *clientStats) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.c = clientCounters{}
	s.window = latencyStats{}
	s.lastPayload = -1
}

func (s *clientStats) addLatency(d time.Duration) {
	s.c.Latency.add(d)
	s.window.add(d)
}

// payloadReceived checks a "payload" notification sent by the test sketch:
// byte i of the payload is (seq + i) & 0xFF.
func (s *clientStats) payloadReceived(params []any) {
	var data []byte
	if len(params) == 1 {
		switch v := params[0].(type) {
		case []byte:
			data = v
		case string:
			data = []byte(v)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.c.PayloadRx++
	s.c.PayloadBytes += uint64(len(data))
	if len(data) == 0 {
		s.c.PayloadCorrupted++
		return
	}
	for i, b := range data {
		if b != data[0]+byte(i) {
			s.c.PayloadCorrupted++
			return
		}
	}
	if s.lastPayload >= 0 {
		s.c.PayloadLost += uint64((int(data[0]) - s.lastPayload - 1) & 0xFF)
	}
	s.lastPayload = int(data[0])
}

// trafficGenerator sends "echo" requests and "null" notifications to the MCU.
type trafficGenerator struct {
	conn    *msgpackrpc.Connection
	stats   *clientStats
	log     *eventLog
	timeout time.Duration

	echo     *pacer
	echoSize atomic.Int64
	inflight atomic.Int64 // echo requests waiting for a response
	limit    atomic.Int64 // max echo requests in flight
	slotFree chan struct{}

	null     *pacer
	nullSize atomic.Int64

	binary atomic.Bool // send the payloads as bin instead of str
}

func newTrafficGenerator(conn *msgpackrpc.Connection, stats *clientStats, log *eventLog, timeout time.Duration) *trafficGenerator {
	return &trafficGenerator{
		conn:     conn,
		stats:    stats,
		log:      log,
		timeout:  timeout,
		echo:     newPacer(0),
		null:     newPacer(0),
		slotFree: make(chan struct{}, 1),
	}
}

func (g *trafficGenerator) setInflightLimit(n int64) {
	g.limit.Store(n)
	g.signalSlotFree()
}

func (g *trafficGenerator) signalSlotFree() {
	select {
	case g.slotFree <- struct{}{}:
	default:
	}
}

func (g *trafficGenerator) run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { g.runEcho(ctx) })
	wg.Go(func() { g.runNull(ctx) })
	wg.Wait()
}

func (g *trafficGenerator) runEcho(ctx context.Context) {
	var seq byte
	var wg sync.WaitGroup
	defer wg.Wait()
	for g.echo.wait(ctx) {
		for g.inflight.Load() >= g.limit.Load() {
			select {
			case <-ctx.Done():
				return
			case <-g.slotFree:
			}
		}
		payload := makePayload(int(g.echoSize.Load()), seq)
		seq++
		g.inflight.Add(1)
		wg.Go(func() {
			defer g.signalSlotFree()
			defer g.inflight.Add(-1)
			g.sendEcho(ctx, payload, g.binary.Load())
		})
	}
}

// makePayload returns a payload of printable characters, so that it can be
// sent either as str or as bin.
func makePayload(size int, seq byte) []byte {
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = '!' + (seq+byte(i))%94
	}
	return payload
}

// param returns the payload as a str or bin msgpack value.
func param(payload []byte, binary bool) any {
	if binary {
		return payload
	}
	return string(payload)
}

func (g *trafficGenerator) sendEcho(ctx context.Context, payload []byte, binary bool) {
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	g.stats.update(func(c *clientCounters) { c.EchoSent++ })
	start := time.Now()
	resp, respErr, err := g.conn.SendRequest(ctx, "echo", param(payload, binary))
	elapsed := time.Since(start)

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		g.stats.update(func(c *clientCounters) { c.EchoTimeouts++ })
		g.log.add("echo request timed out")
	case errors.Is(err, context.Canceled):
		// shutting down
	case err != nil:
		g.stats.update(func(c *clientCounters) { c.EchoErrors++ })
		g.log.add("echo request failed: %v", err)
	case respErr != nil:
		g.stats.update(func(c *clientCounters) { c.EchoErrors++ })
		g.log.add("echo request returned an error: %v", respErr)
	case !echoMatches(resp, payload):
		g.stats.update(func(c *clientCounters) { c.EchoMismatches++ })
		g.log.add("echo request returned unexpected response")
	default:
		g.stats.mu.Lock()
		g.stats.c.EchoOK++
		g.stats.addLatency(elapsed)
		g.stats.mu.Unlock()
	}
}

func echoMatches(resp any, payload []byte) bool {
	switch r := resp.(type) {
	case []byte:
		return bytes.Equal(r, payload)
	case string:
		return r == string(payload)
	}
	return false
}

func (g *trafficGenerator) runNull(ctx context.Context) {
	for g.null.wait(ctx) {
		payload := makePayload(int(g.nullSize.Load()), 0)
		if err := g.conn.SendNotification("null", param(payload, g.binary.Load())); err != nil {
			g.stats.update(func(c *clientCounters) { c.NullErrors++ })
			g.log.add("null notification failed: %v", err)
			continue
		}
		g.stats.update(func(c *clientCounters) { c.NullSent++ })
	}
}

// eventLog keeps the most recent log messages, collapsing repeated ones.
type eventLog struct {
	mu      sync.Mutex
	entries []logEntry
}

type logEntry struct {
	time    time.Time
	msg     string
	repeats int
}

const eventLogSize = 50

func (l *eventLog) add(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.entries); n > 0 && l.entries[n-1].msg == msg {
		l.entries[n-1].repeats++
		l.entries[n-1].time = time.Now()
		return
	}
	l.entries = append(l.entries, logEntry{time: time.Now(), msg: msg, repeats: 1})
	if len(l.entries) > eventLogSize {
		l.entries = l.entries[len(l.entries)-eventLogSize:]
	}
}

// last returns the n most recent entries.
func (l *eventLog) last(n int) []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := max(0, len(l.entries)-n)
	return append([]logEntry(nil), l.entries[start:]...)
}
