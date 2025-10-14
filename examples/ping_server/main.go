// This file is part of arduino-router
//
// Copyright 2025 ARDUINO SA (http://www.arduino.cc/)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-router
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

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
		func(ctx context.Context, _ msgpackrpc.FunctionLogger, method string, params []any) (_result any, _err any) {
			slog.Info("Received request", "method", method, "params", params)
			if method == "ping" {
				return params, nil
			}
			return nil, "method not found: " + method
		},
		nil,
		nil,
	)
	defer conn.Close()
	go conn.Run()

	// Register the ping method
	ctx := context.Background()
	_, reqErr, err := conn.SendRequest(ctx, "$/register", []any{"ping"})
	if err != nil {
		slog.Error("Failed to send register request for ping method", "err", reqErr)
		return
	}
	if reqErr != nil {
		slog.Error("Failed to register ping method", "err", reqErr)
		return
	}

	// Wait forever
	select {}
}
