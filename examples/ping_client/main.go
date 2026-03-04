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
