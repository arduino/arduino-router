// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"strings"
	"syscall"
	"testing"
)

func TestPermissionHint(t *testing.T) {
	tests := []struct {
		name     string
		server   string
		err      error
		wantHint bool
	}{
		{
			name:     "permission denied on unix socket",
			server:   "/var/run/arduino-router.sock",
			err:      fmt.Errorf("dial unix: %w", syscall.EACCES),
			wantHint: true,
		},
		{
			name:   "permission denied on tcp connection",
			server: "localhost:7500",
			err:    syscall.EACCES,
		},
		{
			name:   "other unix socket error",
			server: "/var/run/arduino-router.sock",
			err:    syscall.ECONNREFUSED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := permissionHint(tt.server, tt.err)
			if tt.wantHint {
				if !strings.Contains(hint, "sudo usermod -aG arduino-router") {
					t.Fatalf("permission hint missing suggested group command: %q", hint)
				}
				return
			}
			if hint != "" {
				t.Fatalf("unexpected permission hint: %q", hint)
			}
		})
	}
}
