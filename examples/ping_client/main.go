// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net"

	"github.com/arduino/arduino-router/msgpackrpc"
)

func main() {
	c, err := net.Dial("tcp", ":8900")
	if err != nil {
		panic(err)
	}

	conn := msgpackrpc.NewConnection(c, c, nil, nil, nil)
	defer conn.Close()
	go conn.Run()

	// Client
	reqResult, reqError, err := conn.SendRequest(context.Background(), "ping", "HELLO", 1, true, 5.0)
	if err != nil {
		panic(err)
	}
	fmt.Println(reqResult, reqError)
}
