// This file is part of arduino-router.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"log"
	"os"

	"github.com/arduino/go-paths-helper"
	"github.com/davecgh/go-spew/spew"
	"github.com/vmihailenco/msgpack/v5"
)

func main() {
	f, err := paths.New(os.Args[1]).Open()
	if err != nil {
		log.Fatal(err)
	}
	d := msgpack.NewDecoder(f)
	for {
		if v, err := d.DecodeInterface(); err != nil {
			log.Fatal(err)
		} else {
			spew.Dump(v)
		}
	}
}
