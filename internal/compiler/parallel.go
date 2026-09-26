package compiler

import (
	"runtime"
	"sync"
)

// Bounded worker pool for file-level pipeline stages (parse, emit
// compute). Results are assembled by index, so output order is always
// deterministic regardless of worker count or scheduling.
//
// Panic semantics mirror sequential code: a worker panic is re-raised in
// the CALLING goroutine after all workers settle, so watch-mode safeCycle
// still converts a single bad cycle into a logged warning instead of a
// crash, and one-shot commands still fail loudly. Only the first panic is
// kept (same as sequential fail-fast). Workers always drain the channel so
// a panic can never deadlock the sender; fn must write only its own slot
// (all current users: parse, emit compute).
func parallelDo(n int, fn func(i int)) {
	if n <= 0 {
		return
	}
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > n {
		workers = n
	}
	if workers == 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstPanic any
	record := func(r any) {
		mu.Lock()
		if firstPanic == nil {
			firstPanic = r
		}
		mu.Unlock()
	}
	ch := make(chan int)
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := range ch {
				// Per-item recover: a panicking item must never kill the
				// worker, or (with all workers dead) deadlock the sender.
				func() {
					defer func() {
						if r := recover(); r != nil {
							record(r)
						}
					}()
					fn(i)
				}()
			}
		}()
	}
	for i := 0; i < n; i++ {
		ch <- i
	}
	close(ch)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if firstPanic != nil {
		panic(firstPanic)
	}
}
