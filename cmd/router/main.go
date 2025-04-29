package main

import (
	"log/slog"
	"net"

	"github.com/bcmi-labs/arduino-iot-cloud-data-pipeline/pkg/config"
	"github.com/arduino/router/msgpackrouter"
	"go.bug.st/serial"
)

func main() {
	// Server configuration
	type Config struct {
		LogLevel       slog.Level `default:"debug"`
		ListenAddr     string     `default:":8900"`
		SerialPortAddr string     `default:"/dev/ttyACM0"`
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

	// Open serial port
	serialPort, err := serial.Open(cfg.SerialPortAddr, &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	})
	if err != nil {
		panic(err)
	}
	slog.Info("Opened serial connection", "serial", cfg.SerialPortAddr)

	// Run router
	router := msgpackrouter.New()
	router.Accept(serialPort)
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
