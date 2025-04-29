package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/bcmi-labs/arduino-iot-cloud-data-pipeline/pkg/config"
	"github.com/arduino/router/msgpackrpc"
)

func main() {
	type Config struct {
		LogLevel   slog.Level `default:"debug"`
		RouterAddr string     `default:":8900"`
	}
	var cfg Config
	err := config.New().WithParser(config.EnvParser()).Parse(&cfg)
	if err != nil {
		slog.Error("Failed to parse config", "err", err)
		os.Exit(1)
	}

	s, err := net.Dial("tcp", cfg.RouterAddr)
	if err != nil {
		slog.Error("Failed to connect to router", "addr", cfg.RouterAddr, "err", err)
		os.Exit(1)
	}
	slog.Info("Connected to router", "addr", cfg.RouterAddr)
	defer s.Close()

	conn := msgpackrpc.NewConnection(s, s,
		func(ctx context.Context, _ msgpackrpc.FunctionLogger, method string, params []any) (_result any, _err any) {
			slog.Info("Received request", "method", method, "params", params)
			if method == "ping" {
				return params, nil
			}
			return nil, "method not found: " + method
		},
		func(_ msgpackrpc.FunctionLogger, method string, params []any) {
			slog.Info("Received notification", "method", method, "params", params)
		},
		func(err error) {
			slog.Error("Error", "err", err)
		},
	)
	defer conn.Close()
	go conn.Run()

	// Register the ping method
	ctx := context.Background()
	_, reqErr, err := conn.SendRequest(ctx, "$/register", []any{"ping"})
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
