/*
* MIT License
*
* Copyright (c) 2018 Mike Taghavi <mitghi[at]me.com>
*
* Permission is hereby granted, free of charge, to any person obtaining a copy
* of this software and associated documentation files (the "Software"), to deal
* in the Software without restriction, including without limitation the rights
* to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
* copies of the Software, and to permit persons to whom the Software is
* furnished to do so, subject to the following conditions:
*
* The above copyright notice and this permission notice shall be included in all
* copies or substantial portions of the Software.
*
* THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
* IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
* FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
* AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
* LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
* OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
* SOFTWARE.
 */

// Package lfring provides Lock-Free Multi-Reader, Multi-Writer Ring Buffer implementation.
package lfring

import "unsafe"

// Defaults
const (
	// ui64MASK is maximum int value
	ui64NMASK = ^uint64(0)
	// cRDSCHDTHRESHOLD is reader's spin threshold before
	// yielding control with `runtime.Gosched()`.
	cRDSCHDTHRESHOLD = 1000
	// cWRSCHDTHRESHOLD is writer's spin threshold
	// before yielding control with `runtime.Gosched()`.
	cWRSCHDTHRESHOLD = 1000
)

// - MARK: Struct section.

// Ring is a aligned struct with size of 64 bytes
// used to implement ring buffer. Note that ring
// capacity is always rounded to next power of 2.
type Ring struct {
	// 64bit aligned
	nodes                  []unsafe.Pointer // storage with capacity `size`, pow2
	wri, rdi, maxrdi, size uint64           // write, read, max-read and size (mask) indexes
	count                  uint64           // occupancy counter
}
