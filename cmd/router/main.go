package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/arduino/router/msgpackrouter"

	"github.com/arduino/go-paths-helper"
	"github.com/spf13/cobra"
	"go.bug.st/f"
	"go.bug.st/serial"
)

// Server configuration
type Config struct {
	LogLevel       slog.Level
	ListenTCPAddr  string
	ListenUnixAddr string
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
			if err := startRouter(cfg); err != nil {
				slog.Error("Failed to start router", "err", err)
				os.Exit(1)
			}
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	cmd.Flags().StringVarP(&cfg.ListenTCPAddr, "listen-port", "l", ":8900", "Listening port for RPC services")
	defaultUnixAddr := paths.TempDir().Join("msgpack-router.sock").String()
	cmd.Flags().StringVarP(&cfg.ListenUnixAddr, "unix-port", "u", defaultUnixAddr, "Listening port for RPC services")
	cmd.Flags().StringVarP(&cfg.SerialPortAddr, "serial-port", "p", "", "Serial port address")
	if err := cmd.Execute(); err != nil {
		slog.Error("Error executing command.", "error", err)
	}
}

type DebugStream struct {
	upstream io.ReadWriteCloser
}

func (d *DebugStream) Read(p []byte) (n int, err error) {
	n, err = d.upstream.Read(p)
	slog.Debug("Read from stream", "data", hex.EncodeToString(p[:n]), "err", err)
	return n, err
}

func (d *DebugStream) Write(p []byte) (n int, err error) {
	n, err = d.upstream.Write(p)
	slog.Debug("Write to stream", "data", hex.EncodeToString(p[:n]), "err", err)
	return n, err
}

func (d *DebugStream) Close() error {
	err := d.upstream.Close()
	slog.Debug("Closed stream", "err", err)
	return err
}

func startRouter(cfg Config) error {
	var listeners []net.Listener

	// Open listening TCP socket
	if cfg.ListenTCPAddr != "" {
		if l, err := net.Listen("tcp", cfg.ListenTCPAddr); err != nil {
			return fmt.Errorf("failed to listen on TCP port %s: %w", cfg.ListenTCPAddr, err)
		} else {
			slog.Info("Listening on TCP socket", "listen_addr", cfg.ListenTCPAddr)
			listeners = append(listeners, l)
			defer l.Close()
		}
	}

	// Open listening UNIX socket
	if cfg.ListenUnixAddr != "" {
		if l, err := net.Listen("unix", cfg.ListenUnixAddr); err != nil {
			return fmt.Errorf("failed to listen on UNIX socket %s: %w", cfg.ListenUnixAddr, err)
		} else {
			slog.Info("Listening on Unix socket", "listen_addr", cfg.ListenUnixAddr)
			listeners = append(listeners, l)
			defer l.Close()
		}
	}

	// Run router
	router := msgpackrouter.New()

	// Open serial port if specified
	if cfg.SerialPortAddr != "" {
		var serialLock sync.Mutex
		var serialOpened = sync.NewCond(&serialLock)
		var serialClosed = sync.NewCond(&serialLock)
		var serialCloseSignal = make(chan struct{})
		err := router.RegisterMethod("$/serial/open", func(ctx context.Context, params []any) (result any, err any) {
			if len(params) != 1 {
				return nil, []any{1, "Invalid number of parameters"}
			}
			address, ok := params[0].(string)
			if !ok {
				return nil, []any{1, "Invalid parameter type"}
			}
			slog.Info("Request for opening serial port", "serial", address)
			if address != cfg.SerialPortAddr {
				return nil, []any{1, "Invalid serial port address"}
			}
			serialOpened.L.Lock()
			if serialCloseSignal == nil { // check if already opened
				serialCloseSignal = make(chan struct{})
				serialOpened.Broadcast()
			}
			serialOpened.L.Unlock()
			return true, nil
		})
		f.Assert(err == nil, "Failed to register $/serial/open method")
		err = router.RegisterMethod("$/serial/close", func(ctx context.Context, params []any) (result any, err any) {
			if len(params) != 1 {
				return nil, []any{1, "Invalid number of parameters"}
			}
			address, ok := params[0].(string)
			if !ok {
				return nil, []any{1, "Invalid parameter type"}
			}
			slog.Info("Request for closing serial port", "serial", address)
			if address != cfg.SerialPortAddr {
				return nil, []any{1, "Invalid serial port address"}
			}
			serialClosed.L.Lock()
			if serialCloseSignal != nil { // check if already closed
				close(serialCloseSignal)
				serialCloseSignal = nil
				serialClosed.Wait()
			}
			serialClosed.L.Unlock()
			return true, nil
		})
		f.Assert(err == nil, "Failed to register $/serial/close method")
		go func() {
			for {
				serialOpened.L.Lock()
				for serialCloseSignal == nil {
					serialClosed.Broadcast()
					serialOpened.Wait()
				}
				close := serialCloseSignal
				serialOpened.L.Unlock()

				slog.Info("Opening serial connection", "serial", cfg.SerialPortAddr)
				serialPort, err := serial.Open(cfg.SerialPortAddr, &serial.Mode{
					BaudRate: 115200,
					DataBits: 8,
					StopBits: serial.OneStopBit,
					Parity:   serial.NoParity,
				})
				if err != nil {
					slog.Error("Failed to open serial port. Retrying in 5 seconds...", "serial", cfg.SerialPortAddr, "err", err)
					time.Sleep(5 * time.Second)
					continue
				}
				slog.Info("Opened serial connection", "serial", cfg.SerialPortAddr)
				wr := &DebugStream{upstream: serialPort}

				// wait for the close command from RPC or for a failure of the serial port (routerExit)
				routerExit := router.Accept(wr)
				select {
				case <-routerExit:
					slog.Info("Serial port failed connection")
				case <-close:
				}

				// in any case, wait for the router to drop the connection
				serialPort.Close()
				<-routerExit
			}
		}()
	}

	// Wait for incoming connections on all listeners
	for _, l := range listeners {
		go func() {
			for {
				conn, err := l.Accept()
				if err != nil {
					slog.Error("Failed to accept connection", "err", err)
					continue
				}

				slog.Info("Accepted connection", "addr", conn.RemoteAddr())
				router.Accept(conn)
			}
		}()
	}

	// Sleep forever until interrupted
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	<-signalChan

	// Handle graceful shutdown via defers
	return nil
}
