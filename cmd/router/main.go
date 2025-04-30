package main

import (
	"log/slog"
	"net"

	"github.com/arduino/router/msgpackrouter"

	"github.com/spf13/cobra"
	"go.bug.st/serial"
)

// Server configuration
type Config struct {
	LogLevel       slog.Level
	ListenAddr     string
	SerialPortAddr string
}

func main() {
	var cfg Config
	var verbose bool
	cmd := &cobra.Command{
		Use:  "router",
		Long: "Router for msgpack RPC service protocol",
		Run: func(cmd *cobra.Command, args []string) {
			if verbose {
				cfg.LogLevel = slog.LevelDebug
			} else {
				cfg.LogLevel = slog.LevelInfo
			}
			startRouter(cfg)
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	cmd.Flags().StringVarP(&cfg.ListenAddr, "listen-port", "l", ":8900", "Listening port for RPC services")
	cmd.Flags().StringVarP(&cfg.SerialPortAddr, "serial-port", "p", "", "Serial port address")
	if err := cmd.Execute(); err != nil {
		slog.Error("Error executing command.", "error", err)
	}
}

func startRouter(cfg Config) {
	// Open listening socket
	l, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		panic(err)
	}
	slog.Info("Listening for TCP/IP services", "listen_addr", cfg.ListenAddr)
	defer l.Close()

	// Run router
	router := msgpackrouter.New()

	// Open serial port if specified
	if cfg.SerialPortAddr != "" {
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
		router.Accept(serialPort)
	}

	// Wait for incoming connections
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
