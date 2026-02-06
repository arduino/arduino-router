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

package msgpackrouter

type workerPool struct {
	slots chan struct{}
}

// newWorkerPool creates a new worker pool with the specified size.
// If size is less than or equal to 0, it will be treated as 1 (single worker).
func newWorkerPool(size int) *workerPool {
	if size <= 0 {
		size = 1
	}
	return &workerPool{
		slots: make(chan struct{}, size),
	}
}

// Go starts a new worker to execute the given job function.
// If the worker pool is full, it will block until a worker is available.
func (p *workerPool) Go(job func()) {
	p.slots <- struct{}{}
	go func() {
		defer func() { <-p.slots }()
		job()
	}()
}

// Wait blocks until all workers have completed their jobs.
// No new workers are allowed to start after this method is called.
func (p *workerPool) Wait() {
	for i := 0; i < cap(p.slots); i++ {
		p.slots <- struct{}{}
	}
}
