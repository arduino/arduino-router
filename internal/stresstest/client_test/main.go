// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// client_test is an interactive traffic generator for the test sketch in
// server_test: it sends "echo" requests and "null" notifications at an
// adjustable rate, checks the "payload" notifications sent by the MCU (whose
// rate is also adjustable), and shows the statistics of the client, of the MCU
// and of the serial link in real time.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arduino/arduino-router/msgpackrpc"
)

func main() {
	socket := flag.String("socket", "/var/run/arduino-router.sock", "path of the router unix socket")
	echoRate := flag.Int64("echo-rate", 10, "initial rate of echo requests per second (0 = stopped, -1 = max)")
	echoSize := flag.Int64("echo-size", 25, "initial size in bytes of the echo payload")
	inflight := flag.Int64("inflight", 1, "initial max number of echo requests in flight")
	nullRate := flag.Int64("null-rate", 0, "initial rate of null notifications per second (0 = stopped, -1 = max)")
	nullSize := flag.Int64("null-size", 32, "initial size in bytes of the null payload")
	payloadRate := flag.Int64("payload-rate", 10, "initial rate of payload notifications per second sent by the MCU (0 = stopped, -1 = max)")
	binary := flag.Bool("bin", false, "send the echo and null payloads as msgpack bin instead of str")
	timeout := flag.Duration("timeout", 5*time.Second, "timeout of the echo requests")
	flag.Parse()

	cfg := config{
		echoRate: *echoRate,
		echoSize: *echoSize,
		inflight: max(*inflight, 1),
		nullRate: *nullRate,
		nullSize: *nullSize,

		payloadRate: *payloadRate,
	}
	if *binary {
		cfg.binary = 1
	}
	if err := run(*socket, *timeout, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(socket string, timeout time.Duration, cfg config) error {
	c, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("connecting to router: %w", err)
	}

	log := &eventLog{}
	stats := newClientStats()
	conn := msgpackrpc.NewConnection(c, c,
		func(_ msgpackrpc.FunctionLogger, method string, params []any, res msgpackrpc.ResponseSender) {
			if method == "ping" {
				stats.update(func(c *clientCounters) { c.Pings++ })
				_ = res(params, nil)
				return
			}
			log.add("received request for unknown method %q", method)
			_ = res(nil, "method not found: "+method)
		},
		func(_ msgpackrpc.FunctionLogger, method string, params []any) {
			if method == "payload" {
				stats.payloadReceived(params)
			}
		},
		func(err error) {
			log.add("connection error: %v", err)
		},
	)
	defer conn.Close()

	var disconnected atomic.Bool
	go func() {
		conn.Run()
		disconnected.Store(true)
		log.add("connection to the router closed")
	}()

	ctx := context.Background()
	for _, method := range []string{"ping", "payload"} {
		if _, reqErr, err := conn.SendRequest(ctx, "$/register", method); err != nil {
			return fmt.Errorf("sending register request for %s method: %w", method, err)
		} else if reqErr != nil {
			return fmt.Errorf("registering %s method: %v", method, reqErr)
		}
	}

	gen := newTrafficGenerator(conn, stats, log, timeout)
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { gen.run(ctx) })
	defer wg.Wait()
	defer cancel()

	m := newModel(socket, conn, gen, stats, log, &disconnected, cfg)
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
