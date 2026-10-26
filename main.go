// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/arduino/go-paths-helper"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"go.bug.st/cleanup"
	"go.bug.st/f"
	"go.bug.st/serial"

	"github.com/arduino/arduino-router/internal/hciapi"
	"github.com/arduino/arduino-router/internal/monitorapi"
	"github.com/arduino/arduino-router/internal/msgpackrouter"
	networkapi "github.com/arduino/arduino-router/internal/network-api"
	"github.com/arduino/arduino-router/internal/serialdetect"
	"github.com/arduino/arduino-router/internal/seriallink"
	"github.com/arduino/arduino-router/msgpackrpc"
)

// Version will be set a build time with -ldflags
var Version string = "0.0.0-dev"

// Server configuration
type Config struct {
	LogLevel          slog.Level
	ListenTCPAddr     string
	ListenUnixAddr    string
	AfterReadyCommand string
	SerialPortAddr    string
	SerialBaudRate    int
	SerialMaxBaudRate int
	SerialProtocol    string
	MonitorPortAddr   string
}

func main() {
	var cfg Config
	var verbose bool
	cmd := &cobra.Command{
		Use:  "arduino-router",
		Long: "Arduino router for msgpack RPC service protocol",
		Run: func(cmd *cobra.Command, args []string) {
			if verbose {
				cfg.LogLevel = slog.LevelDebug
			} else {
				cfg.LogLevel = slog.LevelInfo
			}
			if !cmd.Flags().Changed("unix-port") {
				cfg.ListenUnixAddr = cmp.Or(os.Getenv("ARDUINO_ROUTER_SOCKET"), cfg.ListenUnixAddr)
			}
			if err := startRouter(cmd.Context(), cfg); err != nil {
				slog.Error("Failed to start router", "err", err)
				os.Exit(1)
			}
		},
	}
	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose logging")
	cmd.Flags().StringVarP(&cfg.ListenTCPAddr, "listen-port", "l", "", "Listening port for RPC services")
	cmd.Flags().StringVarP(&cfg.ListenUnixAddr, "unix-port", "u", "/var/run/arduino-router.sock", "Listening port for RPC services")
	cmd.Flags().StringVar(&cfg.AfterReadyCommand, "after-ready", "", "Execute a command after the router is ready)")
	cmd.Flags().StringVarP(&cfg.SerialPortAddr, "serial-port", "p", "", "Serial port address")
	cmd.Flags().IntVarP(&cfg.SerialBaudRate, "serial-baudrate", "b", 115200, "Serial port baud rate")
	cmd.Flags().IntVar(&cfg.SerialMaxBaudRate, "serial-max-baudrate", 0, "Maximum baud rate negotiated with the MCU, after starting at --serial-baudrate (only with the reliable protocol, 0 to disable the negotiation)")
	cmd.Flags().StringVar(&cfg.SerialProtocol, "serial-protocol", "auto", "Serial port protocol: 'raw' (plain msgpack-rpc), 'reliable' (msgpack-rpc over a framed link with integrity check, retransmission and flow control) or 'auto' (detect the protocol used by the MCU)")
	cmd.Flags().StringVarP(&cfg.MonitorPortAddr, "monitor-port", "m", "127.0.0.1:7500", "Listening port for MCU monitor proxy")
	cmd.AddCommand(&cobra.Command{
		Use:  "version",
		Long: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Arduino Router " + Version)
		},
	})

	ctx, _ := cleanup.InterruptableContext(context.Background())
	if err := cmd.ExecuteContext(ctx); err != nil {
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

func startRouter(ctx context.Context, cfg Config) error {
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

		// Allow members of the `arduino-router` group to access a socket file owned by `root`
		if err := os.Chmod(cfg.ListenUnixAddr, 0660); err != nil {
			return err
		}
		if grp, err := user.LookupGroup("arduino-router"); err != nil {
			slog.Warn("Group arduino-router not found, unix socket group ownership not changed", "err", err)
		} else if gid, err := strconv.Atoi(grp.Gid); err != nil {
			return fmt.Errorf("invalid gid for group arduino-router: %w", err)
		} else if err := os.Chown(cfg.ListenUnixAddr, -1, gid); err != nil {
			return fmt.Errorf("failed to change group of unix socket %s: %w", cfg.ListenUnixAddr, err)
		}
	}

	// Run router
	router := msgpackrouter.New()

	// Register TCP network API methods
	networkapi.Register(router)

	// Register HCI API methods
	hciapi.Register(router)

	// Register monitor version API methods
	if err := router.RegisterMethod("$/version", func(_ *msgpackrpc.Connection, _ []any, res msgpackrouter.RouterResponseHandler) {
		res(Version, nil)
	}); err != nil {
		slog.Error("Failed to register version API", "err", err)
	}

	// Register monitor API methods
	if err := monitorapi.Register(router, cfg.MonitorPortAddr); err != nil {
		slog.Error("Failed to register monitor API", "err", err)
	}

	// Open serial port if specified
	if cfg.SerialPortAddr != "" {
		if cfg.SerialProtocol != "raw" && cfg.SerialProtocol != "reliable" && cfg.SerialProtocol != "auto" {
			return fmt.Errorf("invalid serial protocol: %s", cfg.SerialProtocol)
		}
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
			if address != cfg.SerialPortAddr {
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
			if address != cfg.SerialPortAddr {
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
		var stats serialStats
		err = router.RegisterMethod("$/serial/stats", func(_ *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
			if len(params) != 0 {
				res(nil, []any{1, "Invalid number of parameters"})
				return
			}
			res(stats.get(), nil)
		})
		f.Assert(err == nil, "Failed to register $/serial/stats method")
		serialMode := func(baudRate int) *serial.Mode {
			return &serial.Mode{
				BaudRate: baudRate,
				DataBits: 8,
				StopBits: serial.OneStopBit,
				Parity:   serial.NoParity,
			}
		}
		go func() {
			for {
				serialOpened.L.Lock()
				for serialCloseSignal == nil {
					serialClosed.Broadcast()
					serialOpened.Wait()
				}
				close := serialCloseSignal
				serialOpened.L.Unlock()

				slog.Debug("Opening serial connection", "serial", cfg.SerialPortAddr)
				serialPort, err := serial.Open(cfg.SerialPortAddr, serialMode(cfg.SerialBaudRate))
				if err != nil {
					slog.Debug("Failed to open serial port. Wait for it...", "serial", cfg.SerialPortAddr, "err", err)
					if err := waitForPort(ctx, cfg.SerialPortAddr); err != nil {
						slog.Error("Failed to wait for serial port", "serial", cfg.SerialPortAddr, "err", err)
						time.Sleep(5 * time.Second) // avoid busy loop in case of repeated failures
					}
					continue
				}
				slog.Info("Opened serial connection", "serial", cfg.SerialPortAddr, "protocol", cfg.SerialProtocol)
				newReliable := func(port io.ReadWriteCloser) (io.ReadWriteCloser, error) {
					link, err := seriallink.New(port, seriallink.Config{
						BaudRate:    cfg.SerialBaudRate,
						MaxBaudRate: cfg.SerialMaxBaudRate,
						SetBaudRate: func(rate int) error {
							// The Go runtime signals may interrupt the syscalls
							if err := retryOnEINTR(serialPort.Drain); err != nil {
								return err
							}
							return retryOnEINTR(func() error { return serialPort.SetMode(serialMode(rate)) })
						},
					})
					if err != nil {
						return nil, err
					}
					stats.newSession("reliable", link)
					return link, nil
				}
				var stream io.ReadWriteCloser = serialPort
				switch cfg.SerialProtocol {
				case "raw":
					stats.newSession("raw", nil)
				case "reliable":
					link, err := newReliable(serialPort)
					f.Assert(err == nil, "Failed to create serial link")
					stream = link
				case "auto":
					stats.newSession("", nil)
					stream = serialdetect.NewAutoStream(serialPort, newReliable, func(p serialdetect.Protocol) {
						slog.Info("Detected serial protocol", "serial", cfg.SerialPortAddr, "protocol", p)
						if p == serialdetect.Raw {
							stats.newSession("raw", nil)
						}
					})
				}
				wr := &MsgpackDebugStream{Name: cfg.SerialPortAddr, Upstream: stats.count(stream)}

				// wait for the close command from RPC or for a failure of the serial port (routerExit)
				routerExit := router.Accept(wr)
				select {
				case <-routerExit:
					slog.Debug("Serial port failed connection")
				case <-close:
				}

				// in any case, wait for the router to drop the connection
				wr.Close()
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

	if cfg.AfterReadyCommand != "" {
		go func() {
			// #nosec G204
			p, err := paths.NewProcess(nil, "sh", "-c", cfg.AfterReadyCommand)
			if err != nil {
				slog.Error("Failed to create process for after-ready command", "command", cfg.AfterReadyCommand, "err", err)
				return
			}
			p.RedirectStderrTo(io.Discard)
			p.RedirectStdoutTo(io.Discard)
			if err := p.Run(); err != nil {
				slog.Error("Failed to start after-ready command", "command", cfg.AfterReadyCommand, "err", err)
				return
			}
			slog.Info("Executed after-ready command successfully", "command", cfg.AfterReadyCommand)
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

// serialStats keeps track of the statistics of the serial links: a new link is
// created every time the serial port is opened or the MCU restarts.
type serialStats struct {
	mu       sync.Mutex
	protocol string           // protocol of the current session, empty if not yet known
	link     *seriallink.Link // link of the current session, nil if not using the reliable protocol
	closed   seriallink.Stats // sum of the counters of the previous links
	sessions int              // number of reliable links created

	rxBytes, txBytes atomic.Uint64 // msgpack-rpc stream bytes, any protocol
}

// serialStatsResponse is the result of the $/serial/stats method.
type serialStatsResponse struct {
	Protocol string           `msgpack:"protocol"` // "raw", "reliable" or "" if not yet known
	Sessions int              `msgpack:"sessions"` // number of reliable link sessions since the router started
	Current  seriallink.Stats `msgpack:"current"`  // counters of the current reliable link session
	Total    seriallink.Stats `msgpack:"total"`    // counters of all the reliable link sessions
	RxBytes  uint64           `msgpack:"rx_bytes"` // msgpack-rpc stream bytes received from the MCU, any protocol
	TxBytes  uint64           `msgpack:"tx_bytes"` // msgpack-rpc stream bytes sent to the MCU, any protocol
}

// newSession starts tracking a new session, link is nil if the protocol is not "reliable".
func (s *serialStats) newSession(protocol string, link *seriallink.Link) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.link != nil {
		s.closed.Add(s.link.Stats())
	}
	s.protocol = protocol
	s.link = link
	if link != nil {
		s.sessions++
	}
}

func (s *serialStats) get() serialStatsResponse {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := serialStatsResponse{
		Protocol: s.protocol,
		Sessions: s.sessions,
		Total:    s.closed,
		RxBytes:  s.rxBytes.Load(),
		TxBytes:  s.txBytes.Load(),
	}
	if s.link != nil {
		res.Current = s.link.Stats()
		res.Total.Add(res.Current)
	}
	return res
}

// count returns stream wrapped to count the bytes read and written.
func (s *serialStats) count(stream io.ReadWriteCloser) io.ReadWriteCloser {
	return &countingStream{ReadWriteCloser: stream, stats: s}
}

// countingStream counts the bytes read from and written to a stream.
type countingStream struct {
	io.ReadWriteCloser
	stats *serialStats
}

func (c *countingStream) Read(p []byte) (int, error) {
	n, err := c.ReadWriteCloser.Read(p)
	c.stats.rxBytes.Add(uint64(n))
	return n, err
}

func (c *countingStream) Write(p []byte) (int, error) {
	n, err := c.ReadWriteCloser.Write(p)
	c.stats.txBytes.Add(uint64(n))
	return n, err
}

func waitForPort(ctx context.Context, waitPort string) error {
	target := filepath.Clean(waitPort)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	// add nearest existing ancestor directory to the watcher
	dir := filepath.Dir(target)
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		p := filepath.Dir(dir)
		if p == dir {
			return fmt.Errorf("no existing ancestor directory for %s", target)
		}
		dir = p
	}
	if err := watcher.Add(dir); err != nil {
		return err
	}

	for {
		if _, err := os.Lstat(target); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-watcher.Errors:
			return err
		case ev := <-watcher.Events:
			if ev.Op&fsnotify.Create != 0 {
				// add new directory to the watcher
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					_ = watcher.Add(ev.Name)
				}
			}
		}
	}
}

// retryOnEINTR calls f until it does not fail with EINTR.
func retryOnEINTR(f func() error) error {
	for {
		if err := f(); !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
