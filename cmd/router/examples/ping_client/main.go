package main

import (
	"context"
	"fmt"
	"net"

	"github.com/arduino/router/msgpackrpc"
)

func main() {
	c, err := net.Dial("tcp", ":8900")
	if err != nil {
		panic(err)
	}

	conn := msgpackrpc.NewConnection(c, c,
		func(ctx context.Context, logger msgpackrpc.FunctionLogger, method string, params []any) (result any, err any) {
			return nil, "method not implemented: " + method
		},
		func(logger msgpackrpc.FunctionLogger, method string, params []any) {
			// ignore notifications
		},
		func(err error) {
			// ignore errors
		})
	defer conn.Close()
	go conn.Run()

	// Client
	reqResult, reqError, err := conn.SendRequest(context.Background(), "ping", []any{"HELLO", 1, true, 5.0})
	if err != nil {
		panic(err)
	}
	fmt.Println(reqResult, reqError)
}
