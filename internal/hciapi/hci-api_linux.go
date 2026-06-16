// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package hciapi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"

	"golang.org/x/sys/unix"

	"github.com/arduino/arduino-router/internal/msgpackrouter"
	"github.com/arduino/arduino-router/msgpackrpc"
)

var hciSocket atomic.Int32

// Common errors
var errNoHCIDeviceOpen = []any{2, "No HCI device open"}

//nolint:gochecknoinits
func init() {
	hciSocket.Store(-1)
}

// Register registers the HCI API methods with the router.
func Register(router *msgpackrouter.Router) {
	_ = router.RegisterMethod("hci/open", hciOpen)
	_ = router.RegisterMethod("hci/send", hciSend)
	_ = router.RegisterMethod("hci/recv", hciRecv)
	_ = router.RegisterMethod("hci/avail", hciAvail)
	_ = router.RegisterMethod("hci/close", hciClose)
}

// HCIOpen opens an HCI socket bound to the specified device (e.g. "hci0").
func hciOpen(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 {
		res(nil, []any{1, "Expected one parameter: HCI device name (e.g., 'hci0')"})
		return
	}

	deviceName, ok := params[0].(string)
	if !ok {
		res(nil, []any{1, "Invalid parameter type: expected string for device name"})
		return
	}

	if len(deviceName) < 4 || deviceName[:3] != "hci" {
		res(nil, []any{1, "Invalid device name format, expected 'hciX' where X is device number"})
		return
	}

	devNum, err := strconv.Atoi(deviceName[3:])
	if err != nil || devNum < 0 || devNum > 0xFFFF {
		res(nil, []any{1, "Invalid device number in device name"})
		return
	}

	// Close any existing socket
	if fd := hciSocket.Swap(-1); fd >= 0 {
		_ = unix.Close(int(fd))
	}

	// Create raw HCI socket
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		res(nil, []any{3, fmt.Sprintf("Failed to create HCI socket: %v", err)})
		return
	}

	// Bring down the HCI device using ioctl (HCIDEVDOWN)
	const HCIDEVDOWN = 0x400448CA // from <bluetooth/hci.h>

	if err := unix.IoctlSetInt(fd, HCIDEVDOWN, devNum); err != nil {
		unix.Close(fd)
		res(nil, []any{3, "Failed to bring down HCI device: " + err.Error()})
		return
	}
	slog.Info("Brought down HCI device", "device", deviceName)

	// Bind to device (user channel)
	addr := &unix.SockaddrHCI{
		Dev:     uint16(devNum), //nolint:gosec
		Channel: unix.HCI_CHANNEL_USER,
	}

	if err := unix.Bind(fd, addr); err != nil {
		unix.Close(fd)
		res(nil, []any{3, fmt.Sprintf("Failed to bind to HCI device: %v", err)})
		return
	}

	hciSocket.Store(int32(fd)) //nolint:gosec
	slog.Info("Opened HCI device", "device", deviceName, "fd", fd)
	res(true, nil)
}

// HCIClose closes the currently open HCI socket.
func hciClose(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 0 {
		res(nil, []any{1, "Expected no parameters"})
		return
	}

	if fd := hciSocket.Swap(-1); fd >= 0 {
		unix.Close(int(fd))
	}

	slog.Info("Closed HCI device")
	res(true, nil)
}

// HCISend transmits raw data to the open HCI socket.
func hciSend(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 {
		res(nil, []any{1, "Expected one parameter: data to send"})
		return
	}

	var data []byte
	switch v := params[0].(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		res(nil, []any{1, "Invalid parameter type, expected []byte or string"})
		return
	}

	fd := hciSocket.Load()
	if fd < 0 {
		res(nil, errNoHCIDeviceOpen)
		return
	}

	n, err := unix.Write(int(fd), data)
	if err != nil {
		slog.Error("Failed to send HCI packet", "err", err)
		res(nil, []any{3, fmt.Sprintf("Failed to send HCI packet: %v", err)})
		return
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		slog.Debug("Sent HCI packet", "bytes", n, "data", hex.EncodeToString(data))
	}
	res(n, nil)
}

// HCIRecv reads available data from the HCI socket.
func hciRecv(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 1 {
		res(nil, []any{1, "Expected one parameter: max bytes to receive"})
		return
	}

	size, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		res(nil, []any{1, "Invalid parameter type, expected uint for max bytes"})
		return
	}

	fd := hciSocket.Load()
	if fd < 0 {
		res(nil, errNoHCIDeviceOpen)
		return
	}

	buffer := make([]byte, size)

	// Short timeout (1ms) for non-blocking behavior
	tv := unix.Timeval{Usec: 1000}
	if err := unix.SetsockoptTimeval(int(fd), unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		res(nil, []any{3, fmt.Sprintf("Failed to set read timeout: %v", err)})
		return
	}

	n, err := unix.Read(int(fd), buffer)
	if err != nil {
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			slog.Debug("HCI recv timeout - no data available")
			res([]byte{}, nil)
			return
		}
		slog.Error("Failed to receive HCI packet", "err", err)
		res(nil, []any{3, fmt.Sprintf("Failed to receive HCI packet: %v", err)})
		return
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		slog.Debug("Received HCI packet", "bytes", n, "data", hex.EncodeToString(buffer[:n]))
	}
	res(buffer[:n], nil)
}

// HCIAvail checks whether data is available to read on the HCI socket.
func hciAvail(rpc *msgpackrpc.Connection, params []any, res msgpackrouter.RouterResponseHandler) {
	if len(params) != 0 {
		res(nil, []any{1, "Expected no parameters"})
		return
	}

	fd := hciSocket.Load()
	if fd < 0 {
		res(nil, errNoHCIDeviceOpen)
		return
	}

	fds := []unix.PollFd{{
		Fd:     fd,
		Events: unix.POLLIN,
	}}

	n, err := unix.Poll(fds, 0)
	if err != nil {
		if errors.Is(err, unix.EINTR) {
			res(false, nil)
			return
		}
		slog.Error("Failed to poll HCI socket", "err", err)
		res(nil, []any{3, fmt.Sprintf("Poll failed: %v", err)})
		return
	}

	res(n > 0 && (fds[0].Revents&unix.POLLIN) != 0, nil)
}
