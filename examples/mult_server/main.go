// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"net"
	"os"

	"github.com/arduino/arduino-router/msgpackrpc"
)

func main() {
	routerAddr := ":8900"
	s, err := net.Dial("tcp", routerAddr)
	if err != nil {
		slog.Error("Failed to connect to router", "addr", routerAddr, "err", err)
		os.Exit(1)
	}
	slog.Info("Connected to router", "addr", routerAddr)
	defer s.Close()

	conn := msgpackrpc.NewConnection(s, s,
		func(_ msgpackrpc.FunctionLogger, method string, params []any, res msgpackrpc.ResponseSender) {
			slog.Info("Received request", "method", method, "params", params)
			if method == "mult" {
				if len(params) != 2 {
					_ = res(nil, "invalid params")
					return
				}
				a, ok := params[0].(float64)
				if !ok {
					_ = res(nil, "invalid param type, expected float64")
					return
				}
				b, ok := params[1].(float64)
				if !ok {
					_ = res(nil, "invalid param type, expected float64")
					return
				}
				_ = res(a*b, nil)
				return
			}
			_ = res(nil, "method not found: "+method)
		},
		nil,
		nil,
	)
	defer conn.Close()
	go conn.Run()

	// Register the ping method
	ctx := context.Background()
	_, reqErr, err := conn.SendRequest(ctx, "$/register", "mult")
	if err != nil {
		slog.Error("Failed to send register request for ping method", "err", err)
		return
	}
	if reqErr != nil {
		slog.Error("Failed to register ping method", "err", reqErr)
		return
	}

	// Wait forever
	select {}
}
