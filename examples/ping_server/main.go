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
