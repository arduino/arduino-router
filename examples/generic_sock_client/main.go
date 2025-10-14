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
	reqResult, reqError, err := conn.SendRequest(context.Background(), method, args)
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
