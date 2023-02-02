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
	"fmt"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// - MARK: Test-structs section.

type tstnode struct {
	uid   string
	value int
}

// - MARK: Test section.

func TestRingConcurrent(t *testing.T) {
	const size uint64 = 1024
	var (
		r       *Ring           = NewRing(size - 7)
		wg      *sync.WaitGroup = &sync.WaitGroup{}
		startCh chan struct{}
	)
	if r.size != size {
		t.Fatalf("assertion failed, r.size(%d)!=size(%d).", r.size, size)
	}
	startCh = make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(wg *sync.WaitGroup, start <-chan struct{}, index int) {
			tmp := make([]interface{}, 0)
			t := 0
			time.Sleep(time.Millisecond * 190)
			<-start
		L:
			for i := 0; i < 200; i++ {
				if i == 50 {
					time.Sleep(time.Millisecond * 5)
				}
				if t == 2 {
					goto DONE
				}
				if r.Push(&tstnode{value: i * index}) {
					tmp = append(tmp, i)
				}
				// runtime.Gosched()
			}
			t++
			goto L
		DONE:
			fmt.Printf("PUSH[GOROUTINE %2.2d,\tLEN %d]:\n%v\n\n ", index, len(tmp), tmp)
			wg.Done()
		}(wg, startCh, i+1)
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(wg *sync.WaitGroup, start <-chan struct{}, index int) {
			tmp := make([]interface{}, 0)
			time.Sleep(time.Millisecond * 200)
			<-start
			for i := 0; i < 100; i++ {
				if i == 20 {
					time.Sleep(time.Millisecond * 5)
				}
				if a, b := r.Pop(); b {
					if x, ok := (a).(*tstnode); !ok || a == nil {
						continue
					} else {
						tmp = append(tmp, x)
					}
				}
				// runtime.Gosched()
			}
			fmt.Printf("POP[GOROUTINE %2.2d,\tLEN %d]:\n%v\n\n", index, len(tmp), tmp)
			wg.Done()
		}(wg, startCh, i)
	}
	for i := 0; i < 4; i++ {
		startCh <- struct{}{}
	}
	wg.Wait()
	fmt.Printf("RING-NODES:\n%v\n", r.nodes)
}

func TestRingSerial(t *testing.T) {
	const rcap = 8
	var (
		lfq *Ring = NewRing(rcap - 3)
	)
	// lfq.size rounds to nearest pow2
	// therefor `lfq.size==rcap`
	if lfq.size != rcap {
		t.Fatal("assertion failed, expected equal.")
	}
	for i := 0; i < rcap; i++ {
		uid := fmt.Sprintf("user_%d", i)
		if !lfq.Push(&tstnode{uid: uid}) {
			t.Fatal("inconsistent state, unable to push.")
		}
	}
	fmt.Printf("(push)RING: %+v\n", lfq)
	// pushing into full ring returns false
	if lfq.Push(&tstnode{uid: "invalid"}) {
		t.Fatal("inconsistent state.")
	}
	if (lfq.count != lfq.maxrdi) && (lfq.wri != 0 && lfq.count == 8) {
		t.Fatal("asesrtion failed.")
	}
	for i := 0; i < rcap; i++ {
		uid := fmt.Sprintf("user_%d", i)
		val, ok := lfq.Pop()
		if !ok {
			t.Fatal("inconsistent state, unable to pop item.")
		}
		item, ok := val.(*tstnode)
		if !ok || item == nil {
			t.Fatal("inconsistent state, invalid value returned.")
		}
		if item.uid != uid {
			t.Fatal("assertion failed, order violation.")
		}
	}
	for i := 0; i < rcap; i++ {
		if lfq.nodes[i] != nil {
			t.Fatal("assertion failed, expected all slots to be nil.")
		}
	}
	// popping from empty ring returns nil, false
	if _, ok := lfq.Pop(); ok {
		t.Fatal("inconsistent state, returned value from empty ring.")
	}
	if (lfq.wri != lfq.rdi) && lfq.count != 0 {
		t.Fatal("assertion failed.")
	}
	fmt.Printf("(pop)RING: %+v\n", lfq)
}

type Sample struct {
	value int
}

type SliceIFTest struct {
	nodes []interface{}
	sid   string
}

type SliceTest struct {
	nodes []unsafe.Pointer
	sid   string
	rdi   uint64
}

type ArrayTest struct {
	nodes [16]unsafe.Pointer
	sid   string
}

type tststore struct {
	counter uint64
	nodes   *SliceTest
}

type ststaligned struct {
	nodes                  []unsafe.Pointer // storage with capacity `size`, pow2
	wri, rdi, maxrdi, size uint64           // write, read, max-read and size (mask) indexes
	count                  uint64           // occupancy counter
	addrdata               uintptr
}

// - MARK: Test-structs section.

type tstsample struct {
	value int
}

type tstslice struct {
	nodes []unsafe.Pointer
	sid   string
}

func init() {
	const _archfmt = "========================\n%-6s%8d\n%-6s%12d\n%-6s%11d\n========================\n"
	fmt.Printf(_archfmt,
		"Address size", cArchADDRSIZE,
		"Tag size", cArchMAXTAG,
		"Word size", cArchWORDSIZE)
}

func TestTag(t *testing.T) {
	const sval = 8
	var (
		s    *Sample = &Sample{value: sval}
		sptr unsafe.Pointer
		tag  uint
		err  error
	)
	sptr, err = TaggedPointer(unsafe.Pointer(s), 1)
	if err != nil {
		t.Fatal("assertion failed, expected==nil.")
	}
	s = (*Sample)(sptr)
	tag = GetTag(sptr)
	fmt.Println("Tag: ", tag)
	sptr = Untag(unsafe.Pointer(s))
	s = (*Sample)(sptr)
	if s == nil || s.value != sval {
		t.Fatal("assertion failed, expected s.value==sval.")
	}
	tag = GetTag(sptr)
	if tag != 0 {
		t.Fatal("assertion failed, expected tag==0.")
	}
}

func TestAtomicSliceWithInterface(t *testing.T) {
	var (
		s    *Sample      = &Sample{value: 8}
		a    *SliceIFTest = &SliceIFTest{nodes: make([]interface{}, 16)}
		sptr unsafe.Pointer
		sval *Sample
		ok   bool
	)
	if !SetSliceSlotI(unsafe.Pointer(&a.nodes), 0, sizeINTERFACE, unsafe.Pointer(s)) {
		t.Fatal("inconsistent state, cannot set slot.")
	}
	if a.nodes[0] == nil {
		t.Fatal("inconsistent state, value is not inserted.")
	} else {
		fmt.Println(a.nodes[2], " is the head value", a.nodes[1])
	}
	fmt.Println("nodes(iface):", a.nodes)
	sptr, ok = PopSliceSlot(unsafe.Pointer(&a.nodes), 0, sizeINTERFACE)
	if !ok {
		t.Fatal("inconsistent state, cannot pop slot.")
	}
	sval = *(**Sample)(sptr)
	if sval == nil {
		t.Fatal("inconsistent state.")
	} else if sval.value != 8 {
		t.Fatal("assertion failed.")
	}
	fmt.Println("Pointer(S) has Value(", sval.value, ")")
}

func TestAtomicSlice(t *testing.T) {
	var (
		s    *Sample    = &Sample{value: 8}
		a    *SliceTest = &SliceTest{nodes: make([]unsafe.Pointer, 16)}
		sptr unsafe.Pointer
		sval *Sample
		ok   bool
	)
	if !SetSliceSlot(unsafe.Pointer(&a.nodes), 0, ArchPTRSIZE, unsafe.Pointer(s)) {
		t.Fatal("inconsistent state, cannot set slot.")
	}
	if a.nodes[0] == nil {
		t.Fatal("inconsistent state, value is not inserted.")
	}
	fmt.Printf("nodes: %#x\n", a.nodes)
	sptr, ok = PopSliceSlot(unsafe.Pointer(&a.nodes), 0, ArchPTRSIZE)
	if !ok {
		t.Fatal("inconsistent state, cannot pop slot.")
	}
	fmt.Printf("nodes: %#x\n", a.nodes)
	sval = (*Sample)(sptr)
	if sval == nil {
		t.Fatal("inconsistent state.")
	} else if sval.value != 8 {
		t.Fatal("assertion failed.")
	}
	fmt.Println("Pointer(S) has Value(", sval.value, ")")
}

func TestCASTag(t *testing.T) {
	var (
		s    *Sample = &Sample{value: 8}
		sptr unsafe.Pointer
		ok   bool
		err  error
	)
	sptr, err = TaggedPointer(unsafe.Pointer(s), 1)
	if err != nil {
		t.Fatal("inconsistent state, expected err==nil.")
	}
	fmt.Println("sptr: ", sptr)
	if sptr, ok = CompareAndSwapPointerTag(sptr, 1, 2); sptr == nil || !ok {
		t.Fatal("inconsistent state, expected !nil, true.", sptr, ok)
	}
	fmt.Println("sptr: swap1->2 ", sptr)
	if tag := GetTag(sptr); tag != 2 {
		t.Fatal("assertion failed, expected 2. got ", tag)
	}
}

func TestRDCSS(t *testing.T) {
	var (
		expected = &tststore{
			counter: 16,
			nodes:   &SliceTest{make([]unsafe.Pointer, 0), "replaced", 0},
		}
		c = &tststore{
			counter: 16,
			nodes:   &SliceTest{make([]unsafe.Pointer, 0), "original", 0},
		}
		crdiptr unsafe.Pointer
	)
	crdiptr = unsafe.Pointer(&c.counter)
	if !RDCSS(
		(*unsafe.Pointer)(crdiptr),
		(unsafe.Pointer)(unsafe.Pointer(uintptr(expected.counter))),
		(*unsafe.Pointer)(unsafe.Pointer(&c.nodes)),
		unsafe.Pointer(c.nodes),
		unsafe.Pointer(expected.nodes),
	) {
		t.Fatal("Expected RDCSS to succeed")
	}
	if c.nodes == nil {
		t.Fatal("inconsistent state.")
	}
	if c.nodes.sid != "replaced" {
		t.Fatal("inconsistent state.", c.nodes)
	}
}

func TestRDCSS2(t *testing.T) {
	var (
		item  = &Sample{16}
		sl    [8]unsafe.Pointer
		slptr *[8]unsafe.Pointer
		n     *Sample
	)
	slptr = &sl
	if !SetArraySlot(unsafe.Pointer(slptr), 4, unsafe.Sizeof(slptr), unsafe.Pointer(item)) {
		t.Fatal("inconsistent state.")
	}
	nsptr, ok := PopArraySlot(unsafe.Pointer(slptr), 4, unsafe.Sizeof(slptr))
	if !ok {
		t.Fatal("inconsistent state.")
	}
	n = (*Sample)(nsptr)
	if n == nil {
		t.Fatal("inconsistent state.")
	} else if n.value != 16 {
		t.Fatal("inconsistent state.")
	}
}

func TestRDCSS2x(t *testing.T) {
	var (
		s       *tstsample     = &tstsample{value: 64}
		n       *tstsample     = &tstsample{value: 128}
		ring    *ststaligned   = &ststaligned{nodes: make([]unsafe.Pointer, 8)}
		slotptr unsafe.Pointer = unsafe.Pointer(OffsetSliceSlot(unsafe.Pointer(&ring.nodes), 1, ArchPTRSIZE))
		rdiPtr  unsafe.Pointer = unsafe.Pointer(&ring.rdi)
		vptr    **tstsample    // value pointer
	)
	if !SetSliceSlot(unsafe.Pointer(&ring.nodes), 1, ArchPTRSIZE, unsafe.Pointer(&s)) {
		t.Fatal("inconsistent state, can't write to slice/slot.")
	}
	if ring.nodes[1] == nil {
		t.Fatal("assertion failed, expected non-nil.")
	}
	vptr = (**tstsample)(ring.nodes[1])
	if vptr == nil {
		t.Fatal("assertion failed, expected non-nil.")
	} else if *vptr == nil {
		t.Fatal("assertion failed, expected non-nil.")
	}
	if (*vptr) != s {
		t.Fatal("assertion failed, expected equal as 's' is written to slot 1.")
	}
	if (((*vptr).value != s.value) && s.value == 64) || s.value != 64 {
		t.Fatal("assertion failed, invalid pointers.")
	}
	fmt.Printf("nodes: %#x\n", ring.nodes)
	if !RDCSS(
		(*unsafe.Pointer)(unsafe.Pointer(&rdiPtr)),
		(unsafe.Pointer)(unsafe.Pointer(rdiPtr)),
		(*unsafe.Pointer)(unsafe.Pointer(slotptr)),
		(unsafe.Pointer)(unsafe.Pointer(&s)),
		(unsafe.Pointer)(unsafe.Pointer(n)),
	) {
		t.Fatal("inconsistent state, RDCSS failure.")
	}
	if s == nil {
		t.Fatal("assertion failed, fatal condition from incorrect pointer mutation.")
	} else if s.value != 64 {
		t.Fatal("assertion failed, invalid pointer.")
	}
	fmt.Printf("nodes: %#x\n", ring.nodes)
	if ring.nodes[1] == nil {
		t.Fatal("assertion failed, expected non-nil for occupied slot.")
	}
	if s == nil {
		t.Fatal("assertion failed, fatal condition from incorrect pointer mutation.")
	}
	vptr = (**tstsample)(slotptr)
	if vptr == nil {
		t.Fatal("assertion failed, expected non-nil pointer.")
	} else if *vptr == nil {
		t.Fatal("assertion failed, expected non-nil pointer.")
	}
	if (*vptr) != n {
		t.Fatal("assertion failed, expected equal as 's' is swapped with 'n'.")
	}
	if (((*vptr).value != n.value) && n.value == 128) || n.value != 128 {
		t.Fatal("assertion failed, invalid pointers.")
	}
	fmt.Println("slotptr:", *(**tstsample)(slotptr), "addr:", slotptr, &n, ring.nodes, ring.nodes[1])
}
