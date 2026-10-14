package main

import (
	"sync"

	"github.com/phrynus/plog"
)

// coalesceState 记录单个 key 的执行状态。
type coalesceState struct {
	running bool   // 是否正在执行
	pending bool   // 执行期间是否又有新的触发
	fn      func() // 最近一次调用的参数（最新值）
}

// Coalescer 按键合并高频调用（single-flight + 最新值覆盖）：
//   - 空闲时调用立即执行，不引入任何定时等待；
//   - 执行期间到达的重复触发被折叠，并在当前执行结束后立刻以最新参数补跑一次。
//
// 相比固定窗口防抖（debounce），热点路径没有人为延迟，突发流量最多合并成一次补跑，
// 且同一 key 的调用天然串行，不会并发冲击下游（例如交易所接口）。
// 所有方法并发安全。
type Coalescer struct {
	mu     sync.Mutex
	states map[string]*coalesceState
}

// NewCoalescer 创建一个合并器。
func NewCoalescer() *Coalescer {
	return &Coalescer{states: make(map[string]*coalesceState)}
}

// Do 触发一次 key 的执行。fn 会立即开始执行；若该 key 已有执行在跑，
// 则只记录最新一次的 fn，等当前执行结束后补跑一次。
func (c *Coalescer) Do(key string, fn func()) {
	c.mu.Lock()
	st, ok := c.states[key]
	if !ok {
		st = &coalesceState{}
		c.states[key] = st
	}
	st.fn = fn // 始终覆盖为最新参数
	if st.running {
		st.pending = true
		c.mu.Unlock()
		plog.Debugf("[止盈] 合并器键=%s 正在执行, 本次触发折叠为补跑", key)
		return
	}
	st.running = true
	c.mu.Unlock()

	plog.Debugf("[止盈] 合并器键=%s 空闲, 立即执行", key)
	go c.loop(key, st)
}

// loop 串行执行同一 key 的任务，并在期间有新触发时补跑最新一次。
func (c *Coalescer) loop(key string, st *coalesceState) {
	for {
		c.mu.Lock()
		fn := st.fn
		st.pending = false
		c.mu.Unlock()

		fn()

		c.mu.Lock()
		if st.pending {
			c.mu.Unlock()
			plog.Debugf("[止盈] 合并器键=%s 执行结束, 存在新触发, 以最新参数补跑一次", key)
			continue
		}
		st.running = false
		delete(c.states, key)
		c.mu.Unlock()
		plog.Debugf("[止盈] 合并器键=%s 执行结束, 无新触发, 退出", key)
		return
	}
}

// Len 返回当前正在执行或待补跑的 key 数量。
func (c *Coalescer) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.states)
}
