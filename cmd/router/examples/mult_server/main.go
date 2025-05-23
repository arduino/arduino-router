package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/arduino/router/msgpackrpc"
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
			if method == "mult" {
				if len(params) != 2 {
					return nil, "invalid params"
				}
				a, ok := params[0].(float64)
				if !ok {
					return nil, "invalid param type, expected float32"
				}
				b, ok := params[1].(float64)
				if !ok {
					return nil, "invalid param type, expected float32"
				}
				return a * b, nil
			}
			return nil, "method not found: " + method
		},
		nil,
		nil,
	)
	defer conn.Close()
	go func() {
		conn.Run()
		os.Exit(0)
	}()

	// Register the ping method
	ctx := context.Background()
	_, reqErr, err := conn.SendRequest(ctx, "$/register", []any{"mult"})
	if err != nil {
		panic(err)
	}
	if reqErr != nil {
		slog.Error("Failed to register ping method", "err", reqErr)
		os.Exit(1)
	}

	for {
		// Run forever...
		time.Sleep(time.Minute)
	}
}
