// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/arduino/arduino-router/internal/seriallink"
	"github.com/arduino/arduino-router/msgpackrpc"
)

const (
	refreshInterval = 250 * time.Millisecond
	sampleInterval  = time.Second
	pollTimeout     = 2 * time.Second
	historySize     = 120

	// The history graphs are minGraphHeight lines tall, and grow up to
	// maxGraphHeight lines if the terminal has room for them while keeping
	// at least minLogLines lines of the event log.
	minGraphHeight = 3
	maxGraphHeight = 8
	minLogLines    = 5

	// uiRateMax is the value of a rate setting meaning "as fast as possible".
	uiRateMax = math.MaxInt64

	// mcuMaxPayload is the approximate max payload size that fits in the
	// MCU receive buffer (BRIDGE_RPC_BUFFER_SIZE) with the msgpack-rpc envelope.
	mcuMaxPayload = 240
)

var (
	ratePresets     = []int64{0, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, uiRateMax}
	sizePresets     = []int64{1, 2, 4, 8, 16, 32, 64, 128, 200, 240, 256, 512, 1024}
	inflightPresets = []int64{1, 2, 4, 8, 16, 32, 64}
)

// config is the traffic generator configuration that can be changed from the UI.
type config struct {
	echoRate, echoSize, inflight int64
	nullRate, nullSize           int64
	payloadRate                  int64 // rate of the "payload" notifications sent by the MCU
	binary                       int64 // payload type: 0 = str, 1 = bin
}

// setting is an adjustable row of the traffic panel.
type setting struct {
	label   string
	unit    string
	presets []int64
	min     int64
	value   func(c *config) *int64
	format  func(v int64) string // optional, renders the value
}

var settings = []setting{
	{"echo rate", "req/s", ratePresets, 0, func(c *config) *int64 { return &c.echoRate }, nil},
	{"echo size", "B", sizePresets, 1, func(c *config) *int64 { return &c.echoSize }, nil},
	{"echo in flight", "", inflightPresets, 1, func(c *config) *int64 { return &c.inflight }, nil},
	{"null rate", "msg/s", ratePresets, 0, func(c *config) *int64 { return &c.nullRate }, nil},
	{"null size", "B", sizePresets, 1, func(c *config) *int64 { return &c.nullSize }, nil},
	{"payload rate", "msg/s", ratePresets, 0, func(c *config) *int64 { return &c.payloadRate }, nil},
	{"payload type", "", []int64{0, 1}, 0, func(c *config) *int64 { return &c.binary }, func(v int64) string {
		if v == 1 {
			return "bin"
		}
		return "str"
	}},
}

// routerStats is the result of the $/serial/stats method.
type routerStats struct {
	Protocol string           `msgpack:"protocol"`
	Sessions int              `msgpack:"sessions"`
	Current  seriallink.Stats `msgpack:"current"`
	Total    seriallink.Stats `msgpack:"total"`
}

// mcuStats is the result of the "stats" method of the test sketch.
type mcuStats struct {
	Echo, Null, Sent uint64
	Rate             uint64 // current rate of the "payload" notifications
}

type tickMsg time.Time

type routerStatsMsg struct {
	time  time.Time
	stats routerStats
	err   error
}

type mcuStatsMsg struct {
	time  time.Time
	stats mcuStats
	err   error
}

type payloadRateMsg struct {
	rate int64
	err  error
}

// sample is a counter snapshot taken at a given time, used to compute rates.
type sample[T any] struct {
	time time.Time
	v    T
}

func rate(cur, prev uint64, dt time.Duration) float64 {
	if dt <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / dt.Seconds()
}

// sub returns a-b, or a if the counter restarted since b was taken.
func sub(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return a - b
}

type model struct {
	socket       string
	conn         *msgpackrpc.Connection
	gen          *trafficGenerator
	stats        *clientStats
	log          *eventLog
	disconnected *atomic.Bool

	width, height int
	selected      int
	cfg           config
	paused        bool
	started       time.Time
	resetAt       time.Time

	client               clientCounters
	clientCur, clientPrv sample[clientCounters]
	latWindow            latencyStats
	latHistory           []float64

	router, routerPrv sample[routerStats]
	routerBase        seriallink.Stats
	routerErr         error
	routerPending     bool
	routerValid       bool
	linkTxHistory     []float64
	linkRxHistory     []float64

	mcu, mcuPrv   sample[mcuStats]
	mcuBase       mcuStats
	mcuErr        error
	mcuPending    bool
	mcuValid      bool
	lastSampledAt time.Time

	payloadRateSent    int64 // last rate set on the MCU, -1 if unknown
	payloadRatePending bool
}

func newModel(socket string, conn *msgpackrpc.Connection, gen *trafficGenerator, stats *clientStats, log *eventLog, disconnected *atomic.Bool, cfg config) *model {
	now := time.Now()
	m := &model{
		socket:       socket,
		conn:         conn,
		gen:          gen,
		stats:        stats,
		log:          log,
		disconnected: disconnected,
		cfg:          cfg,
		started:      now,
		resetAt:      now,
		clientCur:    sample[clientCounters]{time: now},
		clientPrv:    sample[clientCounters]{time: now},

		payloadRateSent: -1,
	}
	if m.cfg.echoRate < 0 {
		m.cfg.echoRate = uiRateMax
	}
	if m.cfg.nullRate < 0 {
		m.cfg.nullRate = uiRateMax
	}
	if m.cfg.payloadRate < 0 {
		m.cfg.payloadRate = uiRateMax
	}
	return m
}

// apply sends the current configuration to the traffic generator, and the
// returned command sends the payload rate to the MCU if it changed.
func (m *model) apply() tea.Cmd {
	toPacer := func(r int64) int64 {
		if m.paused {
			return 0
		}
		if r == uiRateMax {
			return rateMax
		}
		return r
	}
	m.gen.echoSize.Store(m.cfg.echoSize)
	m.gen.nullSize.Store(m.cfg.nullSize)
	m.gen.binary.Store(m.cfg.binary == 1)
	m.gen.setInflightLimit(m.cfg.inflight)
	m.gen.echo.setRate(toPacer(m.cfg.echoRate))
	m.gen.null.setRate(toPacer(m.cfg.nullRate))
	return m.setPayloadRate()
}

// mcuPayloadRate returns the payload rate to be set on the MCU.
func (m *model) mcuPayloadRate() int64 {
	switch {
	case m.paused:
		return 0
	case m.cfg.payloadRate == uiRateMax:
		// The sketch sends as fast as possible above 1000000 msg/s.
		return math.MaxUint32
	}
	return m.cfg.payloadRate
}

// setPayloadRate sends the "set_payload_rate" request to the MCU, if the rate
// differs from the last one set. If a request is already pending, the rate is
// sent when it completes.
func (m *model) setPayloadRate() tea.Cmd {
	rate := m.mcuPayloadRate()
	if m.payloadRatePending || rate == m.payloadRateSent {
		return nil
	}
	m.payloadRatePending = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()
		_, respErr, err := m.conn.SendRequest(ctx, "set_payload_rate", uint32(rate))
		if err == nil && respErr != nil {
			err = fmt.Errorf("%v", respErr)
		}
		return payloadRateMsg{rate: rate, err: err}
	}
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tick(), m.pollRouter(), m.pollMCU(), m.apply())
}

func (m *model) pollRouter() tea.Cmd {
	m.routerPending = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()
		msg := routerStatsMsg{}
		resp, respErr, err := m.conn.SendRequest(ctx, "$/serial/stats")
		msg.time = time.Now()
		if err != nil {
			msg.err = err
		} else if respErr != nil {
			msg.err = fmt.Errorf("%v", respErr)
		} else if data, err := msgpack.Marshal(resp); err != nil {
			msg.err = err
		} else {
			msg.err = msgpack.Unmarshal(data, &msg.stats)
		}
		return msg
	}
}

func (m *model) pollMCU() tea.Cmd {
	m.mcuPending = true
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
		defer cancel()
		msg := mcuStatsMsg{}
		resp, respErr, err := m.conn.SendRequest(ctx, "stats")
		msg.time = time.Now()
		if err != nil {
			msg.err = err
			return msg
		} else if respErr != nil {
			msg.err = fmt.Errorf("%v", respErr)
			return msg
		}
		values, ok := resp.(map[string]any)
		if !ok {
			msg.err = fmt.Errorf("unexpected response: %v", resp)
			return msg
		}
		get := func(key string) uint64 {
			v, _ := msgpackrpc.ToUint(values[key])
			return uint64(v)
		}
		msg.stats = mcuStats{Echo: get("echo"), Null: get("null"), Sent: get("sent"), Rate: get("rate")}
		return msg
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		return m, m.handleKey(msg)

	case tickMsg:
		now := time.Time(msg)
		m.client = m.stats.snapshot()
		var cmds []tea.Cmd
		if now.Sub(m.lastSampledAt) >= sampleInterval {
			m.lastSampledAt = now
			m.clientPrv = m.clientCur
			m.clientCur = sample[clientCounters]{time: now, v: m.client}
			m.latWindow = m.stats.takeWindow()
			m.latHistory = pushHistory(m.latHistory, m.latWindow.Max.Seconds()*1000)
			if !m.routerPending {
				cmds = append(cmds, m.pollRouter())
			}
			if !m.mcuPending {
				cmds = append(cmds, m.pollMCU())
			}
		}
		cmds = append(cmds, tick())
		return m, tea.Batch(cmds...)

	case routerStatsMsg:
		m.routerPending = false
		m.routerErr = msg.err
		if msg.err == nil {
			if m.routerValid {
				m.routerPrv = m.router
			} else {
				m.routerPrv = sample[routerStats]{time: msg.time, v: msg.stats}
				m.routerBase = msg.stats.Total
			}
			m.router = sample[routerStats]{time: msg.time, v: msg.stats}
			m.routerValid = true
			dt := m.router.time.Sub(m.routerPrv.time)
			cur, prv := m.router.v.Total, m.routerPrv.v.Total
			m.linkTxHistory = pushHistory(m.linkTxHistory, rate(cur.TxAckedBytes, prv.TxAckedBytes, dt))
			m.linkRxHistory = pushHistory(m.linkRxHistory, rate(cur.RxDataBytes, prv.RxDataBytes, dt))
		}

	case mcuStatsMsg:
		m.mcuPending = false
		m.mcuErr = msg.err
		if msg.err == nil {
			if m.mcuValid {
				m.mcuPrv = m.mcu
			} else {
				m.mcuPrv = sample[mcuStats]{time: msg.time, v: msg.stats}
				m.mcuBase = msg.stats
			}
			m.mcu = sample[mcuStats]{time: msg.time, v: msg.stats}
			m.mcuValid = true
			// The MCU may have been reset, restoring the default rate.
			if !m.payloadRatePending && int64(msg.stats.Rate) != m.payloadRateSent {
				m.log.add("MCU payload rate is %d msg/s, setting it again", msg.stats.Rate)
				m.payloadRateSent = -1
				return m, m.setPayloadRate()
			}
		}

	case payloadRateMsg:
		m.payloadRatePending = false
		if msg.err != nil {
			m.log.add("set_payload_rate request failed: %v", msg.err)
			m.payloadRateSent = -1
			return m, nil
		}
		m.payloadRateSent = msg.rate
		// Send the rate changed while the request was pending, if any.
		return m, m.setPayloadRate()
	}
	return m, nil
}

func pushHistory(h []float64, v float64) []float64 {
	h = append(h, v)
	if len(h) > historySize {
		h = h[len(h)-historySize:]
	}
	return h
}

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Escape sequences (like the arrow keys) may be received split in
	// multiple reads over a slow link (like adb shell), and be decoded as a
	// standalone "esc" followed by the rest of the sequence: avoid binding
	// "esc" and "[" to make the UI robust against that.
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "up", "k":
		m.selected = (m.selected + len(settings) - 1) % len(settings)
	case "down", "j", "tab":
		m.selected = (m.selected + 1) % len(settings)
	case "right", "l", "+":
		return m.adjust(1, false)
	case "left", "h", "-":
		return m.adjust(-1, false)
	case "shift+right", "L":
		return m.adjust(1, true)
	case "shift+left", "H":
		return m.adjust(-1, true)
	case "0":
		*settings[m.selected].value(&m.cfg) = settings[m.selected].min
		return m.apply()
	case " ", "p":
		m.paused = !m.paused
		return m.apply()
	case "r":
		m.reset()
	}
	return nil
}

// adjust changes the selected setting to the next preset in the direction
// dir, or by one unit if fine is set.
func (m *model) adjust(dir int64, fine bool) tea.Cmd {
	s := settings[m.selected]
	v := s.value(&m.cfg)
	last := s.presets[len(s.presets)-1]
	switch {
	case fine && *v != uiRateMax:
		*v = max(s.min, *v+dir)
		if last != uiRateMax {
			*v = min(*v, last)
		}
	case dir > 0:
		if i := slices.IndexFunc(s.presets, func(p int64) bool { return p > *v }); i >= 0 {
			*v = s.presets[i]
		}
	default:
		for _, p := range slices.Backward(s.presets) {
			if p < *v {
				*v = p
				break
			}
		}
	}
	return m.apply()
}

// reset restarts all the counters from zero.
func (m *model) reset() {
	now := time.Now()
	m.stats.reset()
	m.client = clientCounters{}
	m.clientCur = sample[clientCounters]{time: now}
	m.clientPrv = m.clientCur
	m.latWindow = latencyStats{}
	m.latHistory = nil
	m.linkTxHistory = nil
	m.linkRxHistory = nil
	m.resetAt = now
	if m.routerValid {
		m.routerBase = m.router.v.Total
	}
	if m.mcuValid {
		m.mcuBase = m.mcu.v
	}
}

// --- rendering ---

var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#0b7285", Dark: "#3bc9db"}
	colorDim    = lipgloss.AdaptiveColor{Light: "#868e96", Dark: "#6c757d"}
	colorBorder = lipgloss.AdaptiveColor{Light: "#ced4da", Dark: "#495057"}
	colorGood   = lipgloss.AdaptiveColor{Light: "#2b8a3e", Dark: "#69db7c"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#e67700", Dark: "#ffd43b"}
	colorBad    = lipgloss.AdaptiveColor{Light: "#c92a2a", Dark: "#ff6b6b"}

	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	dimStyle      = lipgloss.NewStyle().Foreground(colorDim)
	goodStyle     = lipgloss.NewStyle().Foreground(colorGood)
	warnStyle     = lipgloss.NewStyle().Foreground(colorWarn)
	badStyle      = lipgloss.NewStyle().Foreground(colorBad)
	selectedStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorBorder).Padding(0, 1)
)

const (
	columnWidth = 58
	linkWidth   = 82 // width of the serial link table
)

func panel(title, body string, width int) string {
	return panelStyle.Width(width - 2).Render(titleStyle.Render(title) + "\n" + body)
}

// errCount renders a counter that should stay zero.
func errCount(v uint64) string {
	s := fmt.Sprintf("%d", v)
	if v > 0 {
		return badStyle.Render(s)
	}
	return goodStyle.Render(s)
}

func fmtRate(r float64) string {
	return fmt.Sprintf("%.1f/s", r)
}

// fmtPayloadRate renders the payload rate set on the MCU.
func fmtPayloadRate(r uint64) string {
	if r > 1000000 {
		return "max"
	}
	return fmt.Sprintf("%d/s", r)
}

func fmtBytes(v float64) string {
	switch {
	case v >= 1<<20:
		return fmt.Sprintf("%.2f MiB", v/(1<<20))
	case v >= 1<<10:
		return fmt.Sprintf("%.2f KiB", v/(1<<10))
	}
	return fmt.Sprintf("%.0f B", v)
}

func fmtMs(d time.Duration) string {
	return fmt.Sprintf("%.2f", d.Seconds()*1000)
}

func (m *model) View() string {
	if m.width == 0 {
		return "starting..."
	}

	header := m.viewHeader()
	help := dimStyle.Render("↑/↓ select · ←/→ change · shift+←/→ fine · 0 zero · space pause · r reset · q quit")

	body := m.viewBody(minGraphHeight)
	used := lipgloss.Height(header) + lipgloss.Height(body) + lipgloss.Height(help)
	// Make the graphs taller with the spare lines (3 graphs, 3 = log panel
	// border and title).
	if spare := m.height - used - 3 - minLogLines; spare >= 3 {
		body = m.viewBody(min(minGraphHeight+spare/3, maxGraphHeight))
		used = lipgloss.Height(header) + lipgloss.Height(body) + lipgloss.Height(help)
	}

	// Use the remaining lines for the event log.
	logLines := min(m.height-used-3, 10) // 3 = panel border and title
	if logLines > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, body, panel("Events", m.viewLog(logLines, m.width-4), m.width))
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, help)
}

// viewBody renders the panels, with history graphs graphHeight lines tall.
func (m *model) viewBody(graphHeight int) string {
	if m.width >= columnWidth+linkWidth {
		rightWidth := m.width - columnWidth
		left := lipgloss.JoinVertical(lipgloss.Left,
			panel("Outgoing traffic", m.viewTraffic(), columnWidth),
			panel("Client", m.viewClient(), columnWidth),
			panel("MCU", m.viewMCU(), columnWidth),
		)
		right := lipgloss.JoinVertical(lipgloss.Left,
			panel("Serial link", m.viewLink(), rightWidth),
			panel("History", m.viewHistory(rightWidth-4, graphHeight), rightWidth),
		)
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}
	w := m.width
	return lipgloss.JoinVertical(lipgloss.Left,
		panel("Outgoing traffic", m.viewTraffic(), w),
		panel("Client", m.viewClient(), w),
		panel("MCU", m.viewMCU(), w),
		panel("Serial link", m.viewLink(), w),
		panel("History", m.viewHistory(w-4, graphHeight), w),
	)
}

func (m *model) viewHeader() string {
	status := goodStyle.Render("RUNNING")
	if m.paused {
		status = warnStyle.Render("PAUSED")
	}
	if m.disconnected.Load() {
		status = badStyle.Render("DISCONNECTED")
	}
	protocol := "?"
	if m.routerValid {
		protocol = m.router.v.Protocol
		if protocol == "" {
			protocol = "detecting"
		}
	}
	now := time.Now()
	return fmt.Sprintf("%s  %s  %s  protocol: %s  up %s  since reset %s",
		titleStyle.Render("arduino-router client test"),
		status,
		dimStyle.Render(m.socket),
		protocol,
		now.Sub(m.started).Truncate(time.Second),
		now.Sub(m.resetAt).Truncate(time.Second))
}

func (m *model) viewTraffic() string {
	dt := m.clientCur.time.Sub(m.clientPrv.time)
	cur, prv := m.clientCur.v, m.clientPrv.v
	actual := []string{
		fmtRate(rate(cur.EchoSent, prv.EchoSent, dt)),
		"",
		fmt.Sprintf("now %d", m.gen.inflight.Load()),
		fmtRate(rate(cur.NullSent, prv.NullSent, dt)),
		"",
		"rx " + fmtRate(rate(cur.PayloadRx, prv.PayloadRx, dt)),
		"must match the sketch parameters",
	}

	var lines []string
	for i, s := range settings {
		v := *s.value(&m.cfg)
		value := fmt.Sprintf("%d", v)
		if v == uiRateMax {
			value = "max"
		}
		if s.format != nil {
			value = s.format(v)
		}
		row := fmt.Sprintf(" %-15s %6s %-6s", s.label, value, s.unit)
		if i == m.selected {
			row = selectedStyle.Render(row)
		}
		note := dimStyle.Render(actual[i])
		if s.unit == "B" && v > mcuMaxPayload {
			note = warnStyle.Render("may exceed MCU buffer")
		}
		lines = append(lines, row+"  "+note)
	}
	return strings.Join(lines, "\n")
}

func (m *model) viewClient() string {
	c := m.client
	dt := m.clientCur.time.Sub(m.clientPrv.time)
	cur, prv := m.clientCur.v, m.clientPrv.v
	w := m.latWindow
	all := c.Latency

	lines := []string{
		fmt.Sprintf("echo    sent %-9d ok %-9d %s", c.EchoSent, c.EchoOK, fmtRate(rate(cur.EchoOK, prv.EchoOK, dt))),
		fmt.Sprintf("        errors %s  timeouts %s  mismatches %s",
			errCount(c.EchoErrors), errCount(c.EchoTimeouts), errCount(c.EchoMismatches)),
		fmt.Sprintf("latency last 1s  min %s  avg %s  max %s ms", fmtMs(w.Min), fmtMs(w.avg()), fmtMs(w.Max)),
		fmt.Sprintf("        overall  min %s  avg %s  max %s ms", fmtMs(all.Min), fmtMs(all.avg()), fmtMs(all.Max)),
		fmt.Sprintf("null    sent %-9d errors %s  %s", c.NullSent, errCount(c.NullErrors), fmtRate(rate(cur.NullSent, prv.NullSent, dt))),
		fmt.Sprintf("payload rx   %-9d %s  %s/s", c.PayloadRx,
			fmtRate(rate(cur.PayloadRx, prv.PayloadRx, dt)), fmtBytes(rate(cur.PayloadBytes, prv.PayloadBytes, dt))),
		fmt.Sprintf("        lost %s  corrupted %s", errCount(c.PayloadLost), errCount(c.PayloadCorrupted)),
		fmt.Sprintf("ping    rx   %d", c.Pings),
	}
	return strings.Join(lines, "\n")
}

func (m *model) viewMCU() string {
	if !m.mcuValid {
		if m.mcuErr != nil {
			return badStyle.Render("stats not available: " + m.mcuErr.Error())
		}
		return dimStyle.Render("waiting for stats...")
	}
	cur, prv, base := m.mcu.v, m.mcuPrv.v, m.mcuBase
	dt := m.mcu.time.Sub(m.mcuPrv.time)
	lines := []string{
		fmt.Sprintf("echo calls          %-10d %s", sub(cur.Echo, base.Echo), fmtRate(rate(cur.Echo, prv.Echo, dt))),
		fmt.Sprintf("null received       %-10d %s", sub(cur.Null, base.Null), fmtRate(rate(cur.Null, prv.Null, dt))),
		fmt.Sprintf("payload sent        %-10d %-10s set %s", sub(cur.Sent, base.Sent), fmtRate(rate(cur.Sent, prv.Sent, dt)), fmtPayloadRate(cur.Rate)),
	}
	if m.mcuErr != nil {
		lines = append(lines, badStyle.Render("last poll failed: "+m.mcuErr.Error()))
	}
	return strings.Join(lines, "\n")
}

type linkRow struct {
	label string
	get   func(s *seriallink.Stats) uint64
	bytes bool
	bad   bool // the counter signals a problem
}

var linkRows = [][2]*linkRow{
	{
		{label: "frames", get: func(s *seriallink.Stats) uint64 { return s.TxFrames }},
		{label: "frames", get: func(s *seriallink.Stats) uint64 { return s.RxFrames }},
	},
	{
		{label: "data frames", get: func(s *seriallink.Stats) uint64 { return s.TxDataFrames }},
		{label: "data accepted", get: func(s *seriallink.Stats) uint64 { return s.RxDataFrames }},
	},
	{
		{label: "data acked", get: func(s *seriallink.Stats) uint64 { return s.TxAckedFrames }},
		{label: "data bytes", get: func(s *seriallink.Stats) uint64 { return s.RxDataBytes }, bytes: true},
	},
	{
		{label: "acked bytes", get: func(s *seriallink.Stats) uint64 { return s.TxAckedBytes }, bytes: true},
		{label: "duplicates", get: func(s *seriallink.Stats) uint64 { return s.RxDuplicates }, bad: true},
	},
	{
		{label: "retransmits", get: func(s *seriallink.Stats) uint64 { return s.Retransmits }, bad: true},
		{label: "out of order", get: func(s *seriallink.Stats) uint64 { return s.RxOutOfOrder }, bad: true},
	},
	{
		{label: "rtx timeouts", get: func(s *seriallink.Stats) uint64 { return s.RetransmitTimeouts }, bad: true},
		{label: "NAKs", get: func(s *seriallink.Stats) uint64 { return s.RxNaks }, bad: true},
	},
	{
		{label: "NAKs", get: func(s *seriallink.Stats) uint64 { return s.TxNaks }, bad: true},
		{label: "errors", get: func(s *seriallink.Stats) uint64 { return s.RxErrors }, bad: true},
	},
	{
		nil,
		{label: " crc", get: func(s *seriallink.Stats) uint64 { return s.RxCRCErrors }, bad: true},
	},
	{
		nil,
		{label: " framing", get: func(s *seriallink.Stats) uint64 { return s.RxFramingErrors }, bad: true},
	},
	{
		nil,
		{label: " protocol", get: func(s *seriallink.Stats) uint64 { return s.RxProtocolErrors }, bad: true},
	},
}

func (m *model) viewLink() string {
	if !m.routerValid {
		if m.routerErr != nil {
			return badStyle.Render("stats not available: " + m.routerErr.Error())
		}
		return dimStyle.Render("waiting for stats...")
	}
	r := m.router.v
	if r.Protocol != "reliable" && r.Sessions == 0 {
		return dimStyle.Render(fmt.Sprintf("protocol %q: no link statistics available", r.Protocol))
	}

	cur, prv, base := r.Total, m.routerPrv.v.Total, m.routerBase
	dt := m.router.time.Sub(m.routerPrv.time)
	cell := func(row *linkRow) string {
		if row == nil {
			return strings.Repeat(" ", 38)
		}
		v := sub(row.get(&cur), row.get(&base))
		rt := rate(row.get(&cur), row.get(&prv), dt)
		value := fmt.Sprintf("%d", v)
		rateStr := fmtRate(rt)
		if row.bytes {
			value = fmtBytes(float64(v))
			rateStr = fmtBytes(rt) + "/s"
		}
		value = fmt.Sprintf("%-11s", value)
		if row.bad {
			if v > 0 {
				value = badStyle.Render(value)
			} else {
				value = goodStyle.Render(value)
			}
		}
		return fmt.Sprintf("%-13s %s %-12s", row.label, value, dimStyle.Render(fmt.Sprintf("%-12s", rateStr)))
	}

	lines := []string{
		fmt.Sprintf("sessions %d   current session: %d frames tx, %d rx",
			r.Sessions, r.Current.TxFrames, r.Current.RxFrames),
		"",
		titleStyle.Render(fmt.Sprintf("%-38s  %s", "TX (router → MCU)", "RX (MCU → router)")),
	}
	for _, row := range linkRows {
		lines = append(lines, cell(row[0])+"  "+cell(row[1]))
	}
	if m.routerErr != nil {
		lines = append(lines, badStyle.Render("last poll failed: "+m.routerErr.Error()))
	}
	return strings.Join(lines, "\n")
}

func (m *model) viewHistory(width, height int) string {
	label := func(name string, h []float64, f func(float64) string) string {
		cur, peak := 0.0, 0.0
		if len(h) > 0 {
			cur, peak = h[len(h)-1], slices.Max(h)
		}
		return fmt.Sprintf("%s  now %s  peak %s", name, f(cur), f(peak))
	}
	ms := func(v float64) string { return fmt.Sprintf("%.2f ms", v) }
	bps := func(v float64) string { return fmtBytes(v) + "/s" }
	return strings.Join([]string{
		label("echo latency (max/s)", m.latHistory, ms),
		titleStyle.Render(sparkline(m.latHistory, width, height)),
		label("link payload throughput tx (router → MCU)", m.linkTxHistory, bps),
		titleStyle.Render(sparkline(m.linkTxHistory, width, height)),
		label("link payload throughput rx (MCU → router)", m.linkRxHistory, bps),
		titleStyle.Render(sparkline(m.linkRxHistory, width, height)),
	}, "\n")
}

// sparkline renders the most recent values of h as a bar graph, height lines
// tall and scaled to the peak value.
func sparkline(h []float64, width, height int) string {
	// levels[i] is a cell filled for i/8 of its height.
	levels := []rune(" ▁▂▃▄▅▆▇█")
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(h) > width {
		h = h[len(h)-width:]
	}
	peak := 0.0
	if len(h) > 0 {
		peak = slices.Max(h)
	}
	// Height of each bar, in eighths of a line.
	bars := make([]int, len(h))
	for i, v := range h {
		if peak > 0 {
			bars[i] = int(v / peak * float64(height*8))
		}
	}
	rows := make([]string, height)
	for r := range rows {
		base := (height - 1 - r) * 8 // eighths below this row
		var sb strings.Builder
		sb.WriteString(strings.Repeat(" ", width-len(h)))
		for _, b := range bars {
			fill := min(max(b-base, 0), 8)
			if base == 0 {
				fill = max(fill, 1) // keep the baseline visible
			}
			sb.WriteRune(levels[fill])
		}
		rows[r] = sb.String()
	}
	return strings.Join(rows, "\n")
}

func (m *model) viewLog(lines, width int) string {
	entries := m.log.last(lines)
	out := make([]string, 0, lines)
	for _, e := range entries {
		line := e.time.Format("15:04:05.000") + " " + e.msg
		if e.repeats > 1 {
			line += fmt.Sprintf(" (x%d)", e.repeats)
		}
		if len(line) > width {
			line = line[:width]
		}
		out = append(out, line)
	}
	for len(out) < lines {
		out = append(out, "")
	}
	if len(entries) == 0 {
		out[0] = dimStyle.Render("no events")
	}
	return strings.Join(out, "\n")
}
