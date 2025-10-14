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

	"github.com/arduino/arduino-router/msgpackrouter"
	"github.com/arduino/arduino-router/msgpackrpc"
)

var hciSocket atomic.Int32

//nolint:gochecknoinits
func init() {
	hciSocket.Store(-1)
}

// Register registers the HCI API methods with the router.
func Register(router *msgpackrouter.Router) {
	_ = router.RegisterMethod("hci/open", HCIOpen)
	_ = router.RegisterMethod("hci/send", HCISend)
	_ = router.RegisterMethod("hci/recv", HCIRecv)
	_ = router.RegisterMethod("hci/avail", HCIAvail)
	_ = router.RegisterMethod("hci/close", HCIClose)
}

// HCIOpen opens an HCI socket bound to the specified device (e.g. "hci0").
func HCIOpen(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_ any, _ any) {
	if len(params) != 1 {
		return nil, []any{1, "Expected one parameter: HCI device name (e.g., 'hci0')"}
	}

	deviceName, ok := params[0].(string)
	if !ok {
		return nil, []any{1, "Invalid parameter type: expected string for device name"}
	}

	if len(deviceName) < 4 || deviceName[:3] != "hci" {
		return nil, []any{1, "Invalid device name format, expected 'hciX' where X is device number"}
	}

	devNum, err := strconv.Atoi(deviceName[3:])
	if err != nil || devNum < 0 || devNum > 0xFFFF {
		return nil, []any{1, "Invalid device number in device name"}
	}

	// Close any existing socket
	if fd := hciSocket.Swap(-1); fd >= 0 {
		_ = unix.Close(int(fd))
	}

	// Create raw HCI socket
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.BTPROTO_HCI)
	if err != nil {
		return nil, []any{3, fmt.Sprintf("Failed to create HCI socket: %v", err)}
	}

	// Bring down the HCI device using ioctl (HCIDEVDOWN)
	const HCIDEVDOWN = 0x400448CA // from <bluetooth/hci.h>

	if err := unix.IoctlSetInt(fd, HCIDEVDOWN, devNum); err != nil {
		unix.Close(fd)
		return nil, []any{3, "Failed to bring down HCI device: " + err.Error()}
	}
	slog.Info("Brought down HCI device", "device", deviceName)

	// Bind to device (user channel)
	addr := &unix.SockaddrHCI{
		Dev:     uint16(devNum), //nolint:gosec
		Channel: unix.HCI_CHANNEL_USER,
	}

	if err := unix.Bind(fd, addr); err != nil {
		unix.Close(fd)
		return nil, []any{3, fmt.Sprintf("Failed to bind to HCI device: %v", err)}
	}

	hciSocket.Store(int32(fd)) //nolint:gosec
	slog.Info("Opened HCI device", "device", deviceName, "fd", fd)
	return true, nil
}

// HCIClose closes the currently open HCI socket.
func HCIClose(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_ any, _ any) {
	if len(params) != 0 {
		return nil, []any{1, "Expected no parameters"}
	}

	if fd := hciSocket.Swap(-1); fd >= 0 {
		unix.Close(int(fd))
	}

	slog.Info("Closed HCI device")
	return true, nil
}

// HCISend transmits raw data to the open HCI socket.
func HCISend(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_ any, _ any) {
	if len(params) != 1 {
		return nil, []any{1, "Expected one parameter: data to send"}
	}

	var data []byte
	switch v := params[0].(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return nil, []any{1, "Invalid parameter type, expected []byte or string"}
	}

	fd := hciSocket.Load()
	if fd < 0 {
		return nil, []any{2, "No HCI device open"}
	}

	n, err := unix.Write(int(fd), data)
	if err != nil {
		slog.Error("Failed to send HCI packet", "err", err)
		return nil, []any{3, fmt.Sprintf("Failed to send HCI packet: %v", err)}
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		slog.Debug("Sent HCI packet", "bytes", n, "data", hex.EncodeToString(data))
	}
	return n, nil
}

// HCIRecv reads available data from the HCI socket.
func HCIRecv(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_ any, _ any) {
	if len(params) != 1 {
		return nil, []any{1, "Expected one parameter: max bytes to receive"}
	}

	size, ok := msgpackrpc.ToUint(params[0])
	if !ok {
		return nil, []any{1, "Invalid parameter type, expected uint for max bytes"}
	}

	fd := hciSocket.Load()
	if fd < 0 {
		return nil, []any{2, "No HCI device open"}
	}

	buffer := make([]byte, size)

	// Short timeout (1ms) for non-blocking behavior
	tv := unix.Timeval{Usec: 1000}
	if err := unix.SetsockoptTimeval(int(fd), unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return nil, []any{3, fmt.Sprintf("Failed to set read timeout: %v", err)}
	}

	n, err := unix.Read(int(fd), buffer)
	if err != nil {
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			slog.Debug("HCI recv timeout - no data available")
			return []byte{}, nil
		}
		slog.Error("Failed to receive HCI packet", "err", err)
		return nil, []any{3, fmt.Sprintf("Failed to receive HCI packet: %v", err)}
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		slog.Debug("Received HCI packet", "bytes", n, "data", hex.EncodeToString(buffer[:n]))
	}
	return buffer[:n], nil
}

// HCIAvail checks whether data is available to read on the HCI socket.
func HCIAvail(ctx context.Context, rpc *msgpackrpc.Connection, params []any) (_ any, _ any) {
	if len(params) != 0 {
		return nil, []any{1, "Expected no parameters"}
	}

	fd := hciSocket.Load()
	if fd < 0 {
		return nil, []any{2, "No HCI device open"}
	}

	fds := []unix.PollFd{{
		Fd:     fd,
		Events: unix.POLLIN,
	}}

	n, err := unix.Poll(fds, 0)
	if err != nil {
		if errors.Is(err, unix.EINTR) {
			return false, nil
		}
		slog.Error("Failed to poll HCI socket", "err", err)
		return nil, []any{3, fmt.Sprintf("Poll failed: %v", err)}
	}

	return n > 0 && (fds[0].Revents&unix.POLLIN) != 0, nil
}
