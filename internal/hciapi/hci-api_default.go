//go:build !linux

package hciapi

import "github.com/arduino/arduino-router/internal/msgpackrouter"

func Register(_ *msgpackrouter.Router) {
}
