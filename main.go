// This file is part of arduino-router
//
// Copyright 2025 ARDUINO SA (http://www.arduino.cc/)
//
// This software is released under the GNU General Public License version 3,
// which covers the main part of arduino-router
// The terms of this license can be found at:
// https://www.gnu.org/licenses/gpl-3.0.en.html
//
// You can be released from the requirements of the above licenses by purchasing
// a commercial license. Buying such a license is mandatory if you want to
// modify or otherwise use the software for commercial activities involving the
// Arduino software without disclosing the source code of your own applications.
// To purchase a commercial license, send an email to license@arduino.cc.

package main

import (
	"cmp"
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

	"github.com/arduino/arduino-router/hciapi"
	"github.com/arduino/arduino-router/monitorapi"
	"github.com/arduino/arduino-router/msgpackrouter"
	"github.com/arduino/arduino-router/msgpackrpc"
	networkapi "github.com/arduino/arduino-router/network-api"

	"github.com/spf13/cobra"
	"go.bug.st/f"
	"go.bug.st/serial"
)

// Version will be set a build time with -ldflags
var Version string = "0.0.0-dev"

// Server configuration
type Config struct {
	LogLevel        slog.Level
	ListenTCPAddr   string
	ListenUnixAddr  string
	SerialPortAddr  string
	SerialBaudRate  int
	MonitorPortAddr string
}

func main() {
	var cfg Config
	var verbose bool
	var printVersion bool
	cmd := &cobra.Command{
		Use:  "arduino-router",
		Long: "Arduino router for msgpack RPC service protocol",
		Run: func(cmd *cobra.Command, args []string) {
			if printVersion {
				fmt.Println("Arduino router v" + Version)
				return
			}
			if verbose {
				cfg.LogLevel = slog.LevelDebug
			} else {
				cfg.LogLevel = slog.LevelInfo
			}
			if !cmd.Flags().Changed("unix-port") {
				cfg.ListenUnixAddr = cmp.Or(os.Getenv("ARDUINO_ROUTER_SOCKET"), cfg.ListenUnixAddr)
			}
			if err := startRouter(cfg); err != nil {
				slog.Error("Failed to start router", "err", err)
				os.Exit(1)
			}
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	cmd.Flags().BoolVar(&printVersion, "version", false, "Print version information")
	cmd.Flags().StringVarP(&cfg.ListenTCPAddr, "listen-port", "l", "", "Listening port for RPC services")
	cmd.Flags().StringVarP(&cfg.ListenUnixAddr, "unix-port", "u", "/var/run/arduino-router.sock", "Listening port for RPC services")
	cmd.Flags().StringVarP(&cfg.SerialPortAddr, "serial-port", "p", "", "Serial port address")
	cmd.Flags().IntVarP(&cfg.SerialBaudRate, "serial-baudrate", "b", 115200, "Serial port baud rate")
	cmd.Flags().StringVarP(&cfg.MonitorPortAddr, "monitor-port", "m", "127.0.0.1:7500", "Listening port for MCU monitor proxy")
	if err := cmd.Execute(); err != nil {
		slog.Error("Error executing command.", "error", err)
	}
}

type MsgpackDebugStream struct {
	Upstream io.ReadWriteCloser
	Name     string
}

func (d *MsgpackDebugStream) Read(p []byte) (n int, err error) {
	n, err = d.Upstream.Read(p)
	if err != nil {
		slog.Debug("Read error from "+d.Name, "err", err)
	} else {
		slog.Debug("Read from "+d.Name, "data", hex.EncodeToString(p[:n]))
	}
	return n, err
}

func (d *MsgpackDebugStream) Write(p []byte) (n int, err error) {
	n, err = d.Upstream.Write(p)
	if err != nil {
		slog.Debug("Write error to "+d.Name, "err", err)
	} else {
		slog.Debug("Write to  "+d.Name, "data", hex.EncodeToString(p[:n]))
	}
	return n, err
}

func (d *MsgpackDebugStream) Close() error {
	return d.Upstream.Close()
}

func startRouter(cfg Config) error {
	slog.SetLogLoggerLevel(cfg.LogLevel)

	var listeners []net.Listener

	// Open listening TCP socket
	if cfg.ListenTCPAddr != "" {
		if l, err := net.Listen("tcp", cfg.ListenTCPAddr); err != nil {
			return fmt.Errorf("failed to listen on TCP port %s: %w", cfg.ListenTCPAddr, err)
		} else {
			slog.Info("Listening on TCP socket", "listen_addr", cfg.ListenTCPAddr)
			listeners = append(listeners, l)
		}
	}

	// Open listening UNIX socket
	if cfg.ListenUnixAddr != "" {
		_ = os.Remove(cfg.ListenUnixAddr) // Remove the socket file if it exists
		if l, err := net.Listen("unix", cfg.ListenUnixAddr); err != nil {
			return fmt.Errorf("failed to listen on UNIX socket %s: %w", cfg.ListenUnixAddr, err)
		} else {
			slog.Info("Listening on Unix socket", "listen_addr", cfg.ListenUnixAddr)
			listeners = append(listeners, l)
		}

		// Allow `arduino` user to write to a socket file owned by `root`
		if err := os.Chmod(cfg.ListenUnixAddr, 0666); err != nil {
			return err
		}
	}

	// Run router
	router := msgpackrouter.New()

	// Register TCP network API methods
	networkapi.Register(router)

	// Register HCI API methods
	hciapi.Register(router)

	// Register monitor API methods
	if err := monitorapi.Register(router, cfg.MonitorPortAddr); err != nil {
		slog.Error("Failed to register monitor API", "err", err)
	}

	// Open serial port if specified
	if cfg.SerialPortAddr != "" {
		var serialLock sync.Mutex
		var serialOpened = sync.NewCond(&serialLock)
		var serialClosed = sync.NewCond(&serialLock)
		var serialCloseSignal = make(chan struct{})
		err := router.RegisterMethod("$/serial/open", func(ctx context.Context, _ *msgpackrpc.Connection, params []any) (result any, err any) {
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
		err = router.RegisterMethod("$/serial/close", func(ctx context.Context, _ *msgpackrpc.Connection, params []any) (result any, err any) {
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
					BaudRate: cfg.SerialBaudRate,
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
				wr := &MsgpackDebugStream{Name: cfg.SerialPortAddr, Upstream: serialPort}

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
					break
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

	// Perform graceful shutdown
	for _, l := range listeners {
		slog.Info("Closing listener", "addr", l.Addr())
		if err := l.Close(); err != nil {
			slog.Error("Failed to close listener", "err", err)
		}
	}

	return nil
}
