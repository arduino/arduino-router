// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"log"
	"os"

	"github.com/arduino/arduino-router/msgpack"

	"github.com/arduino/go-paths-helper"
	"github.com/davecgh/go-spew/spew"
)

func main() {
	f, err := paths.New(os.Args[1]).Open()
	if err != nil {
		log.Fatal(err)
	}
	for {
		if v, err := msgpack.Unmarshal(f); err != nil {
			log.Fatal(err)
		} else {
			spew.Dump(v)
		}
	}
}
