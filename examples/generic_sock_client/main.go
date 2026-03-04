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
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/arduino/arduino-router/msgpackrpc"

	"github.com/arduino/go-paths-helper"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s <METHOD> [<ARG> [<ARG> ...]]\n", os.Args[0])
		os.Exit(1)
	}

	c, err := net.Dial("unix", paths.TempDir().Join("arduino-router.sock").String())
	if err != nil {
		fmt.Println("Error connecting to server:", err)
		os.Exit(1)
	}

	conn := msgpackrpc.NewConnection(c, c, nil, nil, nil)
	defer conn.Close()
	go conn.Run()

	// Client
	method := os.Args[1]
	args := []any{}
	for _, arg := range os.Args[2:] {
		if arg == "true" {
			args = append(args, true)
		} else if arg == "false" {
			args = append(args, false)
		} else if arg == "nil" {
			args = append(args, nil)
		} else if i, err := strconv.Atoi(arg); err == nil {
			args = append(args, i)
		} else {
			args = append(args, arg)
		}
	}
	reqResult, reqError, err := conn.SendRequest(context.Background(), method, args...)
	if err != nil {
		fmt.Println("Error sending request:", err)
		return
	}
	if reqError != nil {
		fmt.Println("Error in response:", reqError)
	} else {
		fmt.Println("Response:", reqResult)
	}
}
