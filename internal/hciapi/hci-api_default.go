// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !linux

package hciapi

import "github.com/arduino/arduino-router/internal/msgpackrouter"

func Register(_ *msgpackrouter.Router) {
}
