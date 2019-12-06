/*
* MIT License
*
* Copyright (c) 2017 Milad (Mike) Taghavi <mitghi[at]me/gmail.com>
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

package lfring

import (
	"runtime"
	"sync/atomic"
	"unsafe"

	"github.com/mitghi/x/pointers"
)

/**
* TODO:
* . implement a variant of `RDCSSComplete` to let
*   competitors help with completing second-stage
*   RDCCS operation.
* . implement `Drain(....)`
* . refactor TryPop(....)
* . bound checks for counter
**/

// - MARK: Alloc/Init section.

// NewRing allocates and initializes a new `Ring`
// struct and returns a pointer to it. Note,
// `capacity` is always rounded to nearest power
// of two.
func NewRing(capacity uint64) (r *Ring) {
	r = &Ring{size: roundP2(capacity)}
	r.nodes = make([]unsafe.Pointer, r.size)
	return r
}

// - MARK: Ring section.

// Len returns number of items in ring.
func (r *Ring) Len() uint64 {
	return atomic.LoadUint64(&r.count)
}

// IsFull returns whether ring is full.
func (r *Ring) IsFull() bool {
	return r.Len() == r.size
}

func (r *Ring) IsEmpty() bool {
	// TODO
	// . specify feasible upper
	//   bounds to prevent false-positives
	//   caused by `uint64` decrements.
	return r.Len() == 0
}

// Push atomically writes `data` to next empty
// slot and returns true when successfull. Note,
// when ring is full, false is returned; does
// not overwrite old slots.
func (r *Ring) Push(data interface{}) bool {
	var (
		mask    uint64 = r.size + 1
		currwri uint64
		i       int = 0
	)
	for {
		currwri = atomic.LoadUint64(&r.wri)
		if ((currwri + 1) % mask) == (atomic.LoadUint64(&r.rdi) % mask) {
			return false
		}
		// acquire current slot by pushing
		// competitors forward; dedicated
		// write access.
		if atomic.CompareAndSwapUint64(&r.wri, currwri, currwri+1) {
			break
		}
	}
	// put data pointer in the slot
	if pointers.SetSliceSlot(unsafe.Pointer(&r.nodes), int(currwri%(mask-1)), pointers.ArchPTRSIZE, unsafe.Pointer(&data)) {
		// update readers boundary
		for !atomic.CompareAndSwapUint64(&r.maxrdi, currwri, currwri+1) {
			i++
			if i == cWRSCHDTHRESHOLD {
				// yield control to scheduler
				// and let competitors run.
				runtime.Gosched()
				i = 0
			}
		}
		atomic.AddUint64(&r.count, 1)
		return true
	}
	return false
}

// Pop atomically pops a value when available and
// returns it with a boolean indicating success
// status. This receiver method spins until
// `(currdi % mask) == (maxrdi % mask)` holds
// true. It returns immediately when ring is
// empty.
func (r *Ring) Pop() (interface{}, bool) {
	var (
		mask    uint64         = r.size + 1               // capacity mask
		entry   unsafe.Pointer = unsafe.Pointer(&r.nodes) // nodes pointer ( reference )
		rdiptr  unsafe.Pointer = unsafe.Pointer(&r.rdi)   // read-index pointer
		index   int                                       // linear index of current slot in `r.nodes`
		i       int                                       // yield threshold
		currdi  uint64                                    // current read-index
		maxrdi  uint64                                    // read-index boundary
		data    interface{}                               // data address  ( dereferenced data pointer )
		dataptr unsafe.Pointer                            // data pointer  ( dereferenced slot pointer )
		offset  unsafe.Pointer                            // slot offset   ( reference )
		slotptr unsafe.Pointer                            // slot pointer  ( reference )
	)
	for {
		currdi = atomic.LoadUint64(&r.rdi)
		maxrdi = atomic.LoadUint64(&r.maxrdi)
		if (currdi % mask) == (maxrdi % mask) {
			return nil, false
		}
		// calculate slot address
		// load data pointer from slot address
		// get and store data pointer from current slot
		index = int(currdi % (mask - 1))
		offset = pointers.OffsetSliceSlot(entry, index, pointers.ArchPTRSIZE)
		dataptr = atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(offset)))
		if dptr := (*interface{})(dataptr); dptr != nil {
			data = *dptr
		} else {
			i++
			continue
		}
		if pointers.HasTag(dataptr) {
			// dataptr is `rdcssDescriptor` which
			// indicates ongoing RDCSS operation
			// on current slot.
			i++
			continue
		}
		slotptr = unsafe.Pointer(offset)
		// swap slot value with nil iff read-index
		// is unchanged. this op is performed in
		// two atomic stages. when interrupted
		// after first stage, the state remains
		// valid and exclusive access still belongs
		// to current thread, because `rdcssDescriptor`
		// acts as a barrier and prevents other threads
		// from performing operations.
		if pointers.RDCSS(
			(*unsafe.Pointer)(rdiptr),
			(unsafe.Pointer)(unsafe.Pointer(uintptr(currdi))),
			(*unsafe.Pointer)(unsafe.Pointer(slotptr)),
			(unsafe.Pointer)(dataptr),
			nil,
		) {
			if atomic.CompareAndSwapUint64(&r.rdi, currdi, currdi+1) {
				atomic.AddUint64(&r.count, ui64NMASK)
				// succesfull, return previously acquired data
				return data, true
			}
		}
		i++
		if i == cRDSCHDTHRESHOLD {
			// busy spin; yield to scheduler
			// and wait.
			runtime.Gosched()
			i = 0
		}
	}
}

// TryPop atomically pops a value when available and
// returns it with a boolean indicating success staus
// . It is identical to `Pop(...)` but terminates
// after `maxwait` threshold is reached and yields
// control to scheduler after `maxwait/4` spins. Useful
// when ring has large capacity.
func (r *Ring) TryPop(maxwait int) (interface{}, bool) {
	var (
		mask          uint64         = r.size + 1
		schdthreshold int            = int(maxwait / 4) // yield threshold
		entry         unsafe.Pointer = unsafe.Pointer(&r.nodes)
		rdiptr        unsafe.Pointer = unsafe.Pointer(&r.rdi)
		i             int
		index         int
		waitcnt       int
		currdi        uint64
		maxrdi        uint64
		data          interface{}
		dataptr       unsafe.Pointer
		offset        unsafe.Pointer
		slotptr       unsafe.Pointer
	)
	for i < maxwait {
		currdi = atomic.LoadUint64(&r.rdi)
		maxrdi = atomic.LoadUint64(&r.maxrdi)
		if (currdi % mask) == (maxrdi % mask) {
			return nil, false
		}
		index = int(currdi % (mask - 1))
		offset = pointers.OffsetSliceSlot(entry, index, pointers.ArchPTRSIZE)
		dataptr = atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(offset)))
		// NOTE
		// . `interface{}` loses type information
		//   when used with atomics.
		if dptr := (*interface{})(dataptr); dptr != nil {
			data = *dptr
		} else {
			i++
			waitcnt++
			continue
		}
		if pointers.HasTag(dataptr) {
			i++
			waitcnt++
			continue
		}
		slotptr = unsafe.Pointer(offset)
		if pointers.RDCSS(
			(*unsafe.Pointer)(rdiptr),
			(unsafe.Pointer)(unsafe.Pointer(uintptr(currdi))),
			(*unsafe.Pointer)(unsafe.Pointer(slotptr)),
			(unsafe.Pointer)(dataptr),
			nil,
		) {
			if atomic.CompareAndSwapUint64(&r.rdi, currdi, currdi+1) {
				atomic.AddUint64(&r.count, ui64NMASK)
				return data, true
			}
		}
		i++
		waitcnt++
		if waitcnt == schdthreshold {
			// NOTE:
			// . fast spinning cause starvation.
			runtime.Gosched()
			waitcnt = 0
		}
	}
	return nil, false
}

// - MARK: Utility section.

// roundP2 rounds the given number `v` to nearest
// power of 2.
func roundP2(v uint64) uint64 {
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v |= v >> 32
	v++

	return v
}
