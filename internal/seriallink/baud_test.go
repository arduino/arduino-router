// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package seriallink

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arduino/arduino-router/internal/noisyline"
)

func baudConfig(p *noisyline.Port, maxBaud int) Config {
	cfg := testConfig
	cfg.MaxBaudRate = maxBaud
	cfg.SetBaudRate = p.SetBaudRate
	return cfg
}

func newLinkWithConfig(t *testing.T, p *noisyline.Port, cfg Config) *Link {
	l, err := New(p, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func newBaudLink(t *testing.T, p *noisyline.Port, maxBaud int) *Link {
	return newLinkWithConfig(t, p, baudConfig(p, maxBaud))
}

// requireBaudRate waits until both links are at the given rate, and have
// proven that it works.
func requireBaudRate(t *testing.T, rate int, pa, pb *noisyline.Port, a, b *Link) {
	require.Eventually(t, func() bool {
		return pa.BaudRate() == rate && pb.BaudRate() == rate &&
			a.BaudRate() == rate && b.BaudRate() == rate &&
			a.baudIsProven() && b.baudIsProven()
	}, 2*time.Second, time.Millisecond)
}

func (l *Link) baudIsProven() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.baudPhase == baudFast && l.baudProven
}

func TestBaudRateNegotiation(t *testing.T) {
	l := noisyline.New(1)
	pa, pb := l.OpenA(), l.OpenB()
	a, b := newBaudLink(t, pa, 2000000), newBaudLink(t, pb, 921600)

	// Writes start right away: what is lost during the switch is retransmitted
	exchange(t, a, b, 256*1024)
	requireBaudRate(t, 921600, pa, pb, a, b)
	for _, st := range []Stats{a.Stats(), b.Stats()} {
		require.EqualValues(t, 1, st.BaudRateSwitches)
		require.Zero(t, st.BaudRateFailures)
		require.Zero(t, st.BaudRateFallbacks)
	}
	require.NotZero(t, l.AtoB().Stats().Bytes)
}

func TestBaudRateNotNegotiated(t *testing.T) {
	for _, maxB := range []int{0, 115200} {
		l := noisyline.New(1)
		pa, pb := l.OpenA(), l.OpenB()
		a, b := newBaudLink(t, pa, 2000000), newBaudLink(t, pb, maxB)
		exchange(t, a, b, 64*1024)
		require.Equal(t, 115200, pa.BaudRate())
		require.Equal(t, 115200, pb.BaudRate())
		require.Zero(t, a.Stats().BaudRateSwitches)
		require.Zero(t, l.AtoB().Stats().Mismatched+l.BtoA().Stats().Mismatched)
	}
}

func TestBaudRateNoisyLine(t *testing.T) {
	// The frames that trigger and confirm the switch may be lost
	for _, tc := range []struct {
		name       string
		faults     noisyline.Faults
		maxPayload int
		size       int
	}{
		{"data", testFaults, 0, 16 * 1024},
		{"control", noisyline.Faults{Drop: 0.015, BitFlip: 0.005}, 8, 256}, // ~25% of the control frames lost
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := noisyline.New(1)
			l.SetFaults(tc.faults)
			var total Stats
			for range 30 {
				pa, pb := l.OpenA(), l.OpenB()
				cfgA, cfgB := baudConfig(pa, 1000000), baudConfig(pb, 1000000)
				cfgA.MaxPayload, cfgB.MaxPayload = tc.maxPayload, tc.maxPayload
				cfgA.MaxRetries, cfgB.MaxRetries = 100, 100
				cfgA.LinkTimeout, cfgB.LinkTimeout = 2*time.Second, 2*time.Second
				a, b := newLinkWithConfig(t, pa, cfgA), newLinkWithConfig(t, pb, cfgB)
				exchange(t, a, b, tc.size)
				requireBaudRate(t, 1000000, pa, pb, a, b)
				total.Add(a.Stats())
				total.Add(b.Stats())
				require.NoError(t, a.Close())
				require.NoError(t, b.Close())
			}
			t.Logf("%+v", total)
		})
	}
}

func TestBaudRateSilenceFallback(t *testing.T) {
	l := noisyline.New(1)
	pa, pb := l.OpenA(), l.OpenB()
	a, b := newBaudLink(t, pa, 921600), newBaudLink(t, pb, 921600)
	exchange(t, a, b, 10000)
	requireBaudRate(t, 921600, pa, pb, a, b)

	// The line goes silent, for less than the link timeout: both sides fall
	// back to the base rate, and switch again when the line comes back
	l.SetBlackhole(true)
	require.Eventually(t, func() bool {
		return pa.BaudRate() == 115200 && pb.BaudRate() == 115200
	}, testConfig.LinkTimeout/2, time.Millisecond)
	l.SetBlackhole(false)
	exchange(t, a, b, 10000)
	requireBaudRate(t, 921600, pa, pb, a, b)
	for _, st := range []Stats{a.Stats(), b.Stats()} {
		require.EqualValues(t, 2, st.BaudRateSwitches)
		require.EqualValues(t, 1, st.BaudRateFallbacks)
	}
}

func TestBaudRatePeerReboot(t *testing.T) {
	l := noisyline.New(1)
	pa, pb := l.OpenA(), l.OpenB()
	a, b := newBaudLink(t, pa, 921600), newBaudLink(t, pb, 921600)
	exchange(t, a, b, 1000)
	requireBaudRate(t, 921600, pa, pb, a, b)

	// B reboots silently and restarts at the base rate: A must detect it
	// before the link timeout
	l.BtoA().SetBlackhole(true)
	require.NoError(t, b.Close())
	l.BtoA().SetBlackhole(false)
	start := time.Now()
	pb = l.OpenB()
	b = newBaudLink(t, pb, 921600)
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrSessionReset)
	require.Less(t, time.Since(start), testConfig.LinkTimeout)

	require.NoError(t, a.Close())
	pa = l.OpenA()
	a = newBaudLink(t, pa, 921600)
	exchange(t, a, b, 10000)
	requireBaudRate(t, 921600, pa, pb, a, b)
}

// requireBaudRateFailure checks that the session fails with ErrBaudRate.
func requireBaudRateFailure(t *testing.T, a, b *Link) {
	_, err := a.Read(make([]byte, 10))
	require.ErrorIs(t, err, ErrBaudRate)
	require.EqualValues(t, maxBaudRateFailures, a.Stats().BaudRateFailures)
	_, err = b.Read(make([]byte, 10))
	require.Error(t, err)
	require.NoError(t, a.Close())
	require.NoError(t, b.Close())
}

func TestBaudRateBroken(t *testing.T) {
	// The negotiated rate does not work: the session is reset, and the next
	// one fails the same way (the rate is not lowered automatically)
	l := noisyline.New(1)
	brokenConfig := func(p *noisyline.Port, maxBaud int) Config {
		cfg := baudConfig(p, maxBaud)
		cfg.SetBaudRate = func(rate int) error {
			if rate == 921600 {
				rate++ // the UART can not generate it accurately
			}
			return p.SetBaudRate(rate)
		}
		return cfg
	}
	for range 2 {
		pa, pb := l.OpenA(), l.OpenB()
		a, b := newLinkWithConfig(t, pa, brokenConfig(pa, 2000000)), newBaudLink(t, pb, 921600)
		requireBaudRateFailure(t, a, b)
	}

	// Lowering the max rate fixes it
	pa, pb := l.OpenA(), l.OpenB()
	a, b := newLinkWithConfig(t, pa, brokenConfig(pa, 576000)), newBaudLink(t, pb, 921600)
	exchange(t, a, b, 10000)
	requireBaudRate(t, 576000, pa, pb, a, b)
}

func TestBaudRateBrokenOneWay(t *testing.T) {
	// At the negotiated rate only A->B works: B hears A, but the switch must
	// not be proven on either side, and the session must fail.
	l := noisyline.New(1)
	pa, pb := l.OpenA(), l.OpenB()
	cfgA := baudConfig(pa, 921600)
	cfgA.SetBaudRate = func(rate int) error {
		l.BtoA().SetBlackhole(rate != 115200)
		return pa.SetBaudRate(rate)
	}
	a, b := newLinkWithConfig(t, pa, cfgA), newBaudLink(t, pb, 921600)
	requireBaudRateFailure(t, a, b)
	require.Zero(t, a.Stats().BaudRateSwitches)
	require.Zero(t, b.Stats().BaudRateSwitches)
}

func TestBaudRateConfig(t *testing.T) {
	_, err := New(noisyline.New(1).OpenA(), Config{MaxBaudRate: 921600})
	require.Error(t, err, "SetBaudRate is required")
}
