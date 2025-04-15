package main

import (
	"log/slog"
	"net"

	"github.com/bcmi-labs/arduino-iot-cloud-data-pipeline/pkg/config"
	"github.com/arduino/bridge/msgpackrouter"
)

func main() {
	// Server configuration
	type Config struct {
		LogLevel   slog.Level `default:"debug"`
		ListenAddr string     `default:":8900"`
	}
	var cfg Config
	err := config.New().WithParser(config.EnvParser()).Parse(&cfg)
	if err != nil {
		panic(err)
	}

	// Open listening socket
	l, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		panic(err)
	}
	slog.Info("Listening for RPC services", "addr", cfg.ListenAddr)
	defer l.Close()

	router := msgpackrouter.New()
	for {
		conn, err := l.Accept()
		if err != nil {
			slog.Error("Failed to accept connection", "err", err)
			continue
		}

		slog.Info("Accepted connection", "addr", conn.RemoteAddr())
		router.Accept(conn)
	}
}
