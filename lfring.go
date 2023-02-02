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

// Package lfring provides Lock-Free Multi-Reader, Multi-Writer Ring Buffer implementation.
package lfring

import (
	"errors"
	"runtime"
	"sync/atomic"
	"unsafe"
)

/*
* implementation of Multi-Word Compare-and-Swap
* atomic operation.
 */

/**
* TODO:
* . implement a variant of `RDCSSComplete` to let
*   competitors help with completing second-stage
*   RDCCS operation.
* . implement `Drain(....)`
* . refactor TryPop(....)
* . bound checks for counter
**/

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

const (
	cArchADDRSIZE = 32 << uintptr(^uintptr(0)>>63)
	cArchWORDSIZE = cArchADDRSIZE >> 3
	cArchMAXTAG   = cArchWORDSIZE - 1
	cArchPTRMASK  = ^uintptr((cArchADDRSIZE >> 5) + 1)
)

var (
	EPTRINVAL  error = errors.New("pointer: invalid.")
	EPTRINVALT error = errors.New("pointer: invalid tag.")
)

var (
	_PTR_         unsafe.Pointer
	_INTERFACE_   interface{}
	ArchPTRSIZE   uintptr = unsafe.Sizeof(_PTR_)
	sizeINTERFACE uintptr = unsafe.Sizeof(_INTERFACE_)
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
	if SetSliceSlot(unsafe.Pointer(&r.nodes), int(currwri%(mask-1)), ArchPTRSIZE, unsafe.Pointer(&data)) {
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
		offset = OffsetSliceSlot(entry, index, ArchPTRSIZE)
		dataptr = atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(offset)))
		if dptr := (*interface{})(dataptr); dptr != nil {
			data = *dptr
		} else {
			i++
			continue
		}
		if HasTag(dataptr) {
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
		if RDCSS(
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
		offset = OffsetSliceSlot(entry, index, ArchPTRSIZE)
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
		if HasTag(dataptr) {
			i++
			waitcnt++
			continue
		}
		slotptr = unsafe.Pointer(offset)
		if RDCSS(
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

// - MARK: Multi-Word Compare-and-Swap Operation section.

// RDCSSDescriptor is descriptor for Multi-Word CAS. RDCSS
// is defined as a restricted form of CAS2 operating atomi-
// cally as follow:
//
// word_t RDCSS(word_t *a1,
//              word_t o1,
//              word_t *a2,
//              word_t o2,
//              word_t n) {
//   r = *a2;
//   if ((r  == o2) && (*a1 == o1)) *a2 = n;
//   return r;
// }
type RDCSSDescriptor struct {
	a1 *unsafe.Pointer // control address
	o1 unsafe.Pointer  // expected value
	a2 *unsafe.Pointer // data address
	o2 unsafe.Pointer  // old value
	n  unsafe.Pointer  // new value
}

// RDCSS performs a Double-Compare Single-Swap atomic
// operation. It attempts to change data address pointer
// `a2` to a `rdcssDescriptor` by comparing it against
// old value `o2`. When successfull, the pointer is changed
// to new value `n` or re-instiated to `o2` in case of
// unsuccessfull operation; A descriptor is active when
// referenced from `a2`. Pointer tagging is used to distinct
// `rdcssDescriptor` pointers.
func RDCSS(a1 *unsafe.Pointer, o1 unsafe.Pointer, a2 *unsafe.Pointer, o2 unsafe.Pointer, n unsafe.Pointer) bool {
	// Paper: A Practical Multi-Word Compare-and-Swap Operation
	//        by Timothy L. Harris, Keir Fraser and Ian A. Pratt;
	//        University of Cambridge Computer Laboratory, Cambridge,
	//        UK.
	var (
		desc *RDCSSDescriptor = &RDCSSDescriptor{a1, o1, a2, o2, n}
		dptr unsafe.Pointer
	)
	// add `0x1` tag
	dptr, _ = TaggedPointer(unsafe.Pointer(desc), 1)
	if atomic.CompareAndSwapPointer(
		(*unsafe.Pointer)(unsafe.Pointer(desc.a2)),
		(unsafe.Pointer)(desc.o2),
		(unsafe.Pointer)(dptr),
	) {
		return RDCSSComplete(dptr)
	}
	return false
}

// RDCSSComplete performs the second stage when descriptor
// is succesfully stored in `a2`. It finishes the operation
// by swapping `a2` with target pointer `n`. The operation
// is successfull, when `a2` is not pointing to RDCSSDescriptor.
// In case of unsucessfull operation, `a2` is swapped with `o2` and
// returns false. Note, `RDCSSDescriptor` pointers have a 0x1
// tag attached to low-order bits.
func RDCSSComplete(d unsafe.Pointer) bool {
	var (
		desc   *RDCSSDescriptor
		tgdptr unsafe.Pointer = d
		dptr   unsafe.Pointer = Untag(d)
	)
	desc = (*RDCSSDescriptor)(dptr)
	if (*desc.a1 == desc.o1) && atomic.CompareAndSwapPointer(
		(*unsafe.Pointer)(unsafe.Pointer(desc.a2)),
		(unsafe.Pointer)(unsafe.Pointer(tgdptr)),
		(unsafe.Pointer)(desc.n),
	) {
		return true
	}
	if !atomic.CompareAndSwapPointer(
		(*unsafe.Pointer)(unsafe.Pointer(desc.a2)),
		(unsafe.Pointer)(tgdptr),
		(unsafe.Pointer)(desc.o2),
	) {
		// TODO
		// . restore ( unable to restore case )
	}
	return false
}

// IsRDCSSDescriptor checks whether the given pointer
// `addr` is pointong to `RDCSSDescriptor`or not. According
// to original paper ( Section 6.2 ), `RDCSSDescriptor`
// pointers can be made distinct by non-zero low-order
// bits. A pointer is pointing to `RDCSSDescriptor` iff
// `0x1` is present.
func IsRDCSSDescriptor(addr unsafe.Pointer) bool {
	return HasTag(addr)
}

// - MARK: Atomics section.

// CASSliceSlot is a function that performs a CAS operation
// on a given slice slot by performing pointer arithmitic
// to find slot address. `addr` is a pointer to slice,
// `data` is a pointer to old value to be compared,
// `target` is a pointer to the new value,  `index` is
// the slot number and `ptrsize` is the slice value size.
// It returns true when succesfull.
func CASSliceSlot(addr unsafe.Pointer, data unsafe.Pointer, target unsafe.Pointer, index int, ptrsize uintptr) bool {
	var (
		tptr *unsafe.Pointer
		cptr unsafe.Pointer
	)
	tptr = (*unsafe.Pointer)(unsafe.Pointer(*(*uintptr)(addr) + (ptrsize * uintptr(index))))
	cptr = unsafe.Pointer(tptr)
	return atomic.CompareAndSwapPointer(
		(*unsafe.Pointer)(unsafe.Pointer(cptr)),
		(unsafe.Pointer)(unsafe.Pointer(target)),
		(unsafe.Pointer)(unsafe.Pointer(data)),
	)
}

// CASSliceSlotPtr is a function that performs a CAS operation
// on a given slice slot by performing pointer arithmitic
// to find slot pointer address. `addr` is a pointer to slice,
// `data` is a pointer to old value to be compared,
// `target` is a pointer to the new value,  `index` is
// the slot number and `ptrsize` is the slice value size.
// It returns true when succesfull.
func CASSliceSlotPtr(addr unsafe.Pointer, data unsafe.Pointer, target unsafe.Pointer, index int, ptrsize uintptr) bool {
	var (
		tptr *unsafe.Pointer
		cptr unsafe.Pointer
	)
	tptr = (*unsafe.Pointer)(unsafe.Pointer((uintptr)(addr) + (ptrsize * uintptr(index))))
	cptr = unsafe.Pointer(tptr)
	return atomic.CompareAndSwapPointer(
		(*unsafe.Pointer)(unsafe.Pointer(cptr)),
		(unsafe.Pointer)(unsafe.Pointer(target)),
		(unsafe.Pointer)(unsafe.Pointer(data)),
	)
}

// CASArraySlot is a function that performs a CAS operation
// on a given array slot by performing pointer arithmitic
// to find slot address. `addr` is a pointer to array,
// `data` is a pointer to old value to be compared,
// `target` is a pointer to the new value,  `index` is
// the slot number and `ptrsize` is the slice value size.
// It returns true when succesfull.
func CASArraySlot(addr unsafe.Pointer, data unsafe.Pointer, target unsafe.Pointer, index int, ptrsize uintptr) bool {
	var (
		tptr *unsafe.Pointer
		cptr unsafe.Pointer
	)
	tptr = (*unsafe.Pointer)(unsafe.Pointer((uintptr)(addr) + (ptrsize * uintptr(index))))
	cptr = unsafe.Pointer(tptr)
	return atomic.CompareAndSwapPointer((*unsafe.Pointer)(unsafe.Pointer(cptr)),
		(unsafe.Pointer)(unsafe.Pointer(target)),
		(unsafe.Pointer)(unsafe.Pointer(data)),
	)
}

// OffsetArraySlot takes a array pointer and returns
// slot address by adding `index` times `ptrsize` bytes
// to slice data pointer.
func OffsetArraySlot(addr unsafe.Pointer, index int, ptrsize uintptr) unsafe.Pointer {
	return unsafe.Pointer((*unsafe.Pointer)(unsafe.Pointer((uintptr)(addr) + (ptrsize * uintptr(index)))))
}

// OffsetSliceSlot takes a slice pointer and returns
// slot address by adding `index` times `ptrsize` bytes
// to slice data pointer.
func OffsetSliceSlot(addr unsafe.Pointer, index int, ptrsize uintptr) unsafe.Pointer {
	return unsafe.Pointer(*(*uintptr)(addr) + (ptrsize * uintptr(index)))
}

// SetSliceSlot is a wrapper function that writes `d`
// to the given slice slot iff its nil and returns
// true when succesfull.
func SetSliceSlot(addr unsafe.Pointer, index int, ptrsize uintptr, d unsafe.Pointer) bool {
	return CASSliceSlot(addr, d, nil, index, ptrsize)
}

// SetSliceSlotPtr is a wrapper function that writes `d`
// to the given slice slot opinter iff its nil and returns
// true when succesfull.
func SetSliceSlotPtr(addr unsafe.Pointer, index int, ptrsize uintptr, d unsafe.Pointer) bool {
	return CASSliceSlotPtr(addr, d, nil, index, ptrsize)
}

// SetSliceSlotI is a wrapper function that writes `d`
// to the given slice slot iff its nil and return
// true when succesfull. Note, it differs from
// `SetSliceSlot` because `d` is written as a pointer
// to `interface{}`.
func SetSliceSlotI(addr unsafe.Pointer, index int, ptrsize uintptr, d interface{}) bool {
	return CASSliceSlot(addr, unsafe.Pointer(&d), nil, index, ptrsize)
}

// SetArraySlot is a wrapper function that writes `d`
// to the given array slot iff its nil. It returns
// true when succesfull.
func SetArraySlot(addr unsafe.Pointer, index int, ptrsize uintptr, d unsafe.Pointer) bool {
	return CASArraySlot(addr, d, nil, index, ptrsize)
}

// LoadArraySlot takes a array pointer and loads
// slot address by adding `index` times `ptrsize` bytes
// to slice data pointer.
func LoadArraySlot(addr unsafe.Pointer, index int, ptrsize uintptr) unsafe.Pointer {
	var (
		tptr *unsafe.Pointer
	)
	tptr = (*unsafe.Pointer)(unsafe.Pointer((uintptr)(addr) + (ptrsize * uintptr(index))))
	return atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(tptr)))
}

// LoadSliceSlot takes a slice pointer and loads
// slot address by adding `index` times `ptrsize` bytes
// to slice data pointer.
func LoadSliceSlot(addr unsafe.Pointer, index int, ptrsize uintptr) unsafe.Pointer {
	var (
		bin *unsafe.Pointer
	)
	bin = (*unsafe.Pointer)(unsafe.Pointer(*(*uintptr)(addr) + (ptrsize * uintptr(index))))
	return atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(bin)))
}

// PopArraySlot is a wrapper function that pops
// `index` slot of array iff its nil. It returns
// a pointer and true when succesfull.
func PopArraySlot(addr unsafe.Pointer, index int, ptrsize uintptr) (unsafe.Pointer, bool) {
	var (
		slot unsafe.Pointer = LoadArraySlot(addr, index, ptrsize)
	)
	if !CASArraySlot(addr, nil, slot, index, ptrsize) {
		return nil, false
	}

	return slot, true
}

// PopSliceSlot is a wrapper function that pops
// `index` slot of slice iff its nil. It returns
// a pointer and true when succesfull.
func PopSliceSlot(addr unsafe.Pointer, index int, ptrsize uintptr) (unsafe.Pointer, bool) {
	var (
		slot unsafe.Pointer = LoadSliceSlot(addr, index, ptrsize)
	)
	if !CASSliceSlot(addr, nil, slot, index, ptrsize) {
		return nil, false
	}

	return slot, true
}

// CompareAndSwapPointerTag performs CAS operation
// and swaps `source` to `source` with new tag
// when comparision is successfull. It reutrns a
// pointer and boolean to to indicate its success.
func CompareAndSwapPointerTag(source unsafe.Pointer, oldtag uint, newtag uint) (unsafe.Pointer, bool) {
	if oldtag > cArchMAXTAG || newtag > cArchMAXTAG {
		panic(EPTRINVALT)
	}
	var (
		sraw   unsafe.Pointer = Untag(source)
		sptr   unsafe.Pointer
		target unsafe.Pointer
	)
	sptr, _ = TaggedPointer(sraw, oldtag)
	target, _ = TaggedPointer(sraw, newtag)
	if atomic.CompareAndSwapPointer(
		(*unsafe.Pointer)(unsafe.Pointer(&sptr)),
		(unsafe.Pointer)(source),
		(unsafe.Pointer)(target),
	) {
		return target, true
	}

	return nil, false
}

// - MARK: Pointer-Tagging section.

// GetTag returns the tag value from
// low-order bits.
func GetTag(ptr unsafe.Pointer) uint {
	return uint(uintptr(ptr) & uintptr(cArchMAXTAG))
}

// TaggedPointer is a function for tagging pointers.
// It attaches `tag` value to the pointer `ptr` iff
// `tag` <= `ArchMAXTAG` and returns the tagged pointer
// along with error set to `nil`. It panics when
// `tag` > `ArchMAXTAG`, I do too! It's like getting
// headshot by a champagne cork.
func TaggedPointer(ptr unsafe.Pointer, tag uint) (unsafe.Pointer, error) {
	if tag > cArchMAXTAG {
		// flip the table, not this time!
		panic(EPTRINVALT)
	}
	return unsafe.Pointer(uintptr(ptr) | uintptr(tag)), nil
}

// Untag is a function for untagging pointers. It
// returns a `unsafe.Pointer` with low-order bits
// set to 0.
func Untag(ptr unsafe.Pointer) unsafe.Pointer {
	return unsafe.Pointer(uintptr(ptr) & cArchPTRMASK)
}

// HasTag returns whether the given pointer `ptr`
// is tagged.
func HasTag(ptr unsafe.Pointer) bool {
	return GetTag(ptr)&cArchMAXTAG > 0
}
