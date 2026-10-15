package main

import (
	"sync"

	"grid/exchange"

	"github.com/phrynus/plog"
)

// FillKind 成交类型：区分多单/空单与建仓/平仓方向，
// 用于把交易量拆分到 4 个桶、把已实现盈亏拆分到多空 2 个桶。
type FillKind int

const (
	FillLongIn   FillKind = iota // 多单买入成交（建仓）
	FillLongOut                  // 多单止盈卖出成交（平仓）
	FillShortIn                  // 空单开空卖出成交（建仓）
	FillShortOut                 // 空单回补买入成交（平仓）
)

// String 返回成交类型的中文名称，用于日志。
func (k FillKind) String() string {
	switch k {
	case FillLongIn:
		return "多单买入"
	case FillLongOut:
		return "多单止盈"
	case FillShortIn:
		return "空单开空"
	case FillShortOut:
		return "空单回补"
	}
	return "未知"
}

// 统计模块：累计本轮策略的运行成果。
// 成交回调来自用户数据流的不同 goroutine（多单/空单可能同时成交），
// 因此用互斥锁保护「写入（成交回调）」与「读取（日志汇总）」，避免并发累加产生数据竞争。
var (
	statMu sync.Mutex // 保护下列统计变量

	fillCount int64 // 成交次数：每笔订单完全成交计 1 次

	// 交易量：按方向拆为 4 类，累计各类成交数量（买入、卖出均计入各自方向）
	volLongIn   float64 // 多单买入量（建仓）
	volLongOut  float64 // 多单止盈卖出量（平仓）
	volShortIn  float64 // 空单开空卖出量（建仓）
	volShortOut float64 // 空单回补买入量（平仓）

	// 已实现盈亏：按多空拆为 2 类，累计交易所回报的 rp
	pnlLong  float64 // 多单已实现盈亏（止盈成交）
	pnlShort float64 // 空单已实现盈亏（回补成交）
)

// RecordFill 记录一笔成交。kind 为成交类型，qty 为本笔成交量，
// pnl 为本笔实现盈亏（无则为空串，按 0 计）。
// 多单（买入建仓、止盈平仓）与空单（开空建仓、回补平仓）的成交回调统一调用。
func RecordFill(kind FillKind, qty, pnl string) {
	q := exchange.ToFloat64(qty)
	p := exchange.ToFloat64(pnl)

	statMu.Lock()
	fillCount++
	switch kind {
	case FillLongIn:
		volLongIn += q
	case FillLongOut:
		volLongOut += q
		pnlLong += p
	case FillShortIn:
		volShortIn += q
	case FillShortOut:
		volShortOut += q
		pnlShort += p
	}
	n := fillCount
	li, lo, si, so := volLongIn, volLongOut, volShortIn, volShortOut
	pl, ps := pnlLong, pnlShort
	statMu.Unlock()

	plog.Infof("[统计] 类型=%s 成交次数=%d 交易量[多单买入=%s 多单止盈=%s 空单开空=%s 空单回补=%s] 盈亏[多单=%s 空单=%s]",
		kind, n, exchange.ToString(li), exchange.ToString(lo),
		exchange.ToString(si), exchange.ToString(so),
		exchange.ToString(pl), exchange.ToString(ps))
}

// ResetStats 归零统计，用于清空数据表、重新开始一轮策略时调用。
func ResetStats() {
	statMu.Lock()
	defer statMu.Unlock()
	fillCount = 0
	volLongIn, volLongOut, volShortIn, volShortOut = 0, 0, 0, 0
	pnlLong, pnlShort = 0, 0
	plog.Debugf("[统计] 统计已归零")
}

// LogStats 输出当前累计统计，用于退出等需要汇总的场景。
func LogStats() {
	statMu.Lock()
	n := fillCount
	li, lo, si, so := volLongIn, volLongOut, volShortIn, volShortOut
	pl, ps := pnlLong, pnlShort
	statMu.Unlock()

	plog.Infof("[统计] 累计 成交次数=%d 交易量[多单买入=%s 多单止盈=%s 空单开空=%s 空单回补=%s] 盈亏[多单=%s 空单=%s]",
		n, exchange.ToString(li), exchange.ToString(lo),
		exchange.ToString(si), exchange.ToString(so),
		exchange.ToString(pl), exchange.ToString(ps))
}
