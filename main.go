// This file is part of arduino-router
//
// Copyright (C) ARDUINO SRL (www.arduino.cc)
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
	"bytes"
	"cmp"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
	"go.bug.st/f"
	"go.bug.st/serial"

	"github.com/arduino/arduino-router/internal/monitorapi"
	"github.com/arduino/arduino-router/internal/msgpackrouter"
	networkapi "github.com/arduino/arduino-router/internal/network-api"
	"github.com/arduino/arduino-router/msgpackrpc"
)

// Version will be set a build time with -ldflags
var Version string = "0.0.0-dev"

type Config struct {
	LogLevel                    slog.Level `toml:"log_level"`
	ListenPort                  string     `toml:"listen_port"`
	UnixPort                    string     `toml:"unix_port"`
	SerialPort                  string     `toml:"serial_port"`
	SerialBaudrate              int        `toml:"serial_baudrate"`
	MonitorPort                 string     `toml:"monitor_port"`
	MaxPendingRequestsPerClient int        `toml:"max_pending_requests_per_client"`
}

// Default configuration values.
var cfg = Config{
	LogLevel:                    slog.LevelInfo,
	ListenPort:                  ":7502",
	UnixPort:                    "",
	SerialPort:                  "",
	SerialBaudrate:              115200,
	MonitorPort:                 "127.0.0.1:7500",
	MaxPendingRequestsPerClient: 25,
}

func main() {
	// Preliminary flag parsing to get config file path before setting up Cobra
	var cfgPath string
	preFlagSet := flag.NewFlagSet("config", flag.ContinueOnError)
	preFlagSet.SetOutput(io.Discard) // Suppress flag package output
	preFlagSet.StringVar(&cfgPath, "config-file", "", "config file")
	_ = preFlagSet.Parse(os.Args[1:])

	cfg, err := ParseConfig(cfg, cfgPath)
	if err != nil {
		slog.Error("Failed to load config file", "err", err)
		os.Exit(1)
	}

	var verbose bool
	cmd := &cobra.Command{
		Use:  "arduino-router",
		Long: "Arduino router for msgpack RPC service protocol",
		Run: func(cmd *cobra.Command, args []string) {
			if verbose {
				cfg.LogLevel = slog.LevelDebug
			}
			slog.SetLogLoggerLevel(cfg.LogLevel)

			// FIXME: this should be handled in the config parser.
			if !cmd.Flags().Changed("unix-port") {
				cfg.ListenPort = cmp.Or(os.Getenv("ARDUINO_ROUTER_SOCKET"), cfg.ListenPort)
			}

			slog.Debug("Starting Arduino Router", "config", cfg)

			if err := startRouter(cfg); err != nil {
				slog.Error("Failed to start router", "err", err)
				os.Exit(1)
			}
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")

	cmd.Flags().AddGoFlagSet(preFlagSet) // Include preliminary flags in Cobra
	cmd.Flags().StringVarP(&cfg.ListenPort, "listen-port", "l", cfg.ListenPort, "Listening port for RPC services")
	cmd.Flags().StringVarP(&cfg.UnixPort, "unix-port", "u", cfg.UnixPort, "Listening port for RPC services")
	cmd.Flags().StringVarP(&cfg.SerialPort, "serial-port", "p", cfg.SerialPort, "Serial port address")
	cmd.Flags().IntVarP(&cfg.SerialBaudrate, "serial-baudrate", "b", cfg.SerialBaudrate, "Serial port baud rate")
	cmd.Flags().StringVarP(&cfg.MonitorPort, "monitor-port", "m", cfg.MonitorPort, "Listening port for MCU monitor proxy")
	cmd.Flags().IntVar(&cfg.MaxPendingRequestsPerClient, "max-pending-requests", cfg.MaxPendingRequestsPerClient, "Maximum number of pending requests per client connection (0 = unlimited)")
	cmd.AddCommand(&cobra.Command{
		Use:  "version",
		Long: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Arduino Router " + Version)
		},
	})

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

	var listeners []net.Listener

	// Open listening TCP socket
	if cfg.ListenPort != "" {
		if l, err := net.Listen("tcp", cfg.ListenPort); err != nil {
			return fmt.Errorf("failed to listen on TCP port %s: %w", cfg.ListenPort, err)
		} else {
			slog.Info("Listening on TCP socket", "listen_addr", cfg.ListenPort)
			listeners = append(listeners, l)
		}
	}

	// Open listening UNIX socket
	if cfg.UnixPort != "" {
		_ = os.Remove(cfg.UnixPort) // Remove the socket file if it exists
		if l, err := net.Listen("unix", cfg.UnixPort); err != nil {
			return fmt.Errorf("failed to listen on UNIX socket %s: %w", cfg.UnixPort, err)
		} else {
			slog.Info("Listening on Unix socket", "listen_addr", cfg.UnixPort)
			listeners = append(listeners, l)
		}

		// Allow `arduino` user to write to a socket file owned by `root`
		if err := os.Chmod(cfg.UnixPort, 0666); err != nil {
			return err
		}
	}

	// Run router
	router := msgpackrouter.New(cfg.MaxPendingRequestsPerClient)

	// Register TCP network API methods
	networkapi.Register(router)

	// // Register HCI API methods
	// hciapi.Register(router)

	// Register monitor version API methods
	if err := router.RegisterMethod("$/version", func(_ *msgpackrpc.Connection, _ []any, res msgpackrouter.RouterResponseHandler) {
		res(Version, nil)
	}); err != nil {
		slog.Error("Failed to register version API", "err", err)
	}

	// Register monitor API methods
	if err := monitorapi.Register(router, cfg.MonitorPort); err != nil {
		slog.Error("Failed to register monitor API", "err", err)
	}

	// Open serial port if specified
	if cfg.SerialPort != "" {
		var serialLock sync.Mutex
		var serialOpened = sync.NewCond(&serialLock)
		var serialClosed = sync.NewCond(&serialLock)
		var serialCloseSignal = make(chan struct{})
		err := router.RegisterMethod("$/serial/open", func(_ *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
			if len(params) != 1 {
				res(nil, []any{1, "Invalid number of parameters"})
				return
			}
			address, ok := params[0].(string)
			if !ok {
				res(nil, []any{1, "Invalid parameter type"})
				return
			}
			slog.Info("Request for opening serial port", "serial", address)
			if address != cfg.SerialPort {
				res(nil, []any{1, "Invalid serial port address"})
				return
			}
			serialOpened.L.Lock()
			if serialCloseSignal == nil { // check if already opened
				serialCloseSignal = make(chan struct{})
				serialOpened.Broadcast()
			}
			serialOpened.L.Unlock()
			res(true, nil)
		})
		f.Assert(err == nil, "Failed to register $/serial/open method")
		err = router.RegisterMethod("$/serial/close", func(_ *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
			if len(params) != 1 {
				res(nil, []any{1, "Invalid number of parameters"})
				return
			}
			address, ok := params[0].(string)
			if !ok {
				res(nil, []any{1, "Invalid parameter type"})
				return
			}
			slog.Info("Request for closing serial port", "serial", address)
			if address != cfg.SerialPort {
				res(nil, []any{1, "Invalid serial port address"})
				return
			}
			serialClosed.L.Lock()
			if serialCloseSignal != nil { // check if already closed
				close(serialCloseSignal)
				serialCloseSignal = nil
				serialClosed.Wait()
			}
			serialClosed.L.Unlock()
			res(true, nil)
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

				slog.Info("Opening serial connection", "serial", cfg.SerialPort)
				serialPort, err := serial.Open(cfg.SerialPort, &serial.Mode{
					BaudRate: cfg.SerialBaudrate,
					DataBits: 8,
					StopBits: serial.OneStopBit,
					Parity:   serial.NoParity,
				})
				if err != nil {
					slog.Error("Failed to open serial port. Retrying in 5 seconds...", "serial", cfg.SerialPort, "err", err)
					time.Sleep(5 * time.Second)
					continue
				}
				slog.Info("Opened serial connection", "serial", cfg.SerialPort)
				wr := &MsgpackDebugStream{Name: cfg.SerialPort, Upstream: serialPort}

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

func ParseConfig[T any](base T, cfgPath string) (T, error) {
	if cfgPath == "" {
		return base, nil
	}

	f, err := os.Open(cfgPath)
	if err != nil {
		return base, fmt.Errorf("failed to read config file: %w", err)
	}
	var tomlCfg map[string]T
	if _, err := toml.NewDecoder(f).Decode(&tomlCfg); err != nil {
		return base, fmt.Errorf("failed to parse config file: %w", err)
	}

	fmt.Printf("Parsed config: %+v\n", tomlCfg)

	// overrides with default values.
	if defaultCfg, ok := tomlCfg["default"]; ok {
		base = defaultCfg
	}

	// get board specific override if exists.
	getBoardName := func() string {
		buf, err := os.ReadFile("/sys/class/dmi/id/product_name")
		if err != nil {
			return ""
		}
		return string(bytes.TrimSpace(bytes.ToLower(buf)))
	}
	if name := getBoardName(); name != "" {
		fmt.Printf("Detected board: %s\n", name)
		if override, ok := tomlCfg[name]; ok {
			fmt.Printf("override: %+v\n", override)
			base = override
		}
	}

	return base, nil
}
