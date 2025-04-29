package main

import (
	"context"
	"fmt"
	"net"

	"github.com/arduino/bridge/msgpackrpc"
)

func main() {
	c, err := net.Dial("tcp", ":8900")
	if err != nil {
		panic(err)
	}

	conn := msgpackrpc.NewConnection(c, c,
		// Server
		func(ctx context.Context, logger msgpackrpc.FunctionLogger, method string, params []any) (result any, err any) {
			if method == "ping" {
				return params[0], nil
			}
			return nil, "method not implemented: " + method
		},
		func(logger msgpackrpc.FunctionLogger, method string, params []any) {

		},
		func(err error) {
			fmt.Println("Error:", err)
		})
	defer conn.Close()
	go conn.Run()

	// Client
	reqResult, reqError, err := conn.SendRequest(context.Background(), "mult", []any{3.0, 5.0})
	if err != nil {
		panic(err)
	}
	fmt.Println(reqResult, reqError)
}
