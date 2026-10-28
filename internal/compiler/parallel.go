package compiler

import (
	"runtime"
	"sync"
)

// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
//
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// 排查看 Span/诊断，如 `local x=1` 错圈整句。
// …（略，详见实现；排查先看 Span/诊断）
// 死锁：故每项独立 recover，工头不死；障碍二是 fn 写串槽：契约要求只写自家槽。
// 候选：errgroup vs 手写；选手写，语义贴 fail-fast。不变量：n<=0 直接返；
// 单工退化串行。排查现场：数据串扰先查 fn 是否写邻槽，panic 丢失先查 firstPanic。
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
				// 排查看 Span/诊断，如 `local x=1` 错圈整句。
				// 排查看 Span/诊断，如 `local x=1` 错圈整句。
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
