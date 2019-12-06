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
