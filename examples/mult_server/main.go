// This file is part of arduino-router.
//
// Copyright (C) Arduino s.r.l. and/or its affiliated companies
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
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
