// 减仓：当保证金比率高于基准点时，选取 usdc 做空且未实现盈亏最大的币，
// 分批平掉部分仓位以释放保证金。实际下单在 reducePair。
package main

import (
	"context"
)

// Reduce 减仓：在所有 usdc 做空的持仓中找未实现盈亏最大者，异步执行减仓。
func (h *hedge) Reduce(positions map[string]*pos) {

	// 找未实现盈亏最大的 usdc 空单持仓
	var pick string
	var maxPnl float64
	found := false
	for base, p := range positions {
		if p.usdc.side != "SHORT" || p.usdc.qty == 0 {
			continue
		}
		if !found || p.pnl > maxPnl {
			found = true
			pick = base
			maxPnl = p.pnl
		}
	}
	if !found {
		h.log.Infof("减仓: 无 USDC 空单持仓可减")
		return
	}

	p := positions[pick]

	h.log.Infof("减仓: 选 %s 减仓 pnl=%.4f usdc=%.8f usdt=%.8f", pick, maxPnl, p.usdc.qty, p.usdt.qty)
	// 异步执行减仓，避免阻塞主循环
	go h.reducePair(pick, p)
}

// reducePair 执行减仓（针对单个对冲对）：
//  1. 市价 BUY 平掉部分 USDC 空单（名义价值超过阈值时按 StepUsdt 换算数量，否则全平）
//  2. 从下单返回的成交量得到实际减仓数量
//  3. 市价 SELL 同步减掉等量 USDT 多单，保持双向持仓平衡
func (h *hedge) reducePair(base string, p *pos) {
	usdc := base + "USDC"
	usdt := base + "USDT"
	lp := p.usdc

	// 只处理 usdc 空单
	if p.usdc.side != "SHORT" || p.usdc.qty <= 0 {
		return
	}

	svc := h.ws.NewOrder().
		Symbol(usdc).
		Side("BUY").
		PositionSide("SHORT").
		Type("MARKET")
	if lp.value > cfg.Reduce.StepNotional {
		// 持仓名义价值较大：按盘口反向价（卖一价 Ask）换算减仓数量（平仓 BUY）
		book, err := h.ws.Book(context.Background(), usdc)
		if err != nil {
			h.log.Errorf("减仓: 获取 %s 盘口失败: %v", usdc, err)
			return
		}
		svc = svc.Quantity(ftos(tof(cfg.Reduce.StepUsdt) / tof(book.AskPrice)))
	} else {
		// 名义价值较小：一次全平
		svc = svc.Quantity(ftos(lp.qty))
	}

	// 市价下单并返回成交结果（RESULT 含成交量）
	h.log.Infof("减仓: 市价 BUY 减 %s 空单(全平=%v) 持仓=%.8f 名义价值=%.4f",
		usdc, lp.value <= cfg.Reduce.StepNotional, lp.qty, lp.value)
	order, err := svc.RespType("RESULT").DoPlace(context.Background())
	if err != nil {
		h.log.Errorf("减仓: %s 空单减仓失败: %v", usdc, err)
		return
	}

	filled := tof(order.OrigQty)
	if filled <= 0 {
		h.log.Warnf("减仓: %s 空单成交量为 0，跳过同步减多单", usdc)
		return
	}

	// 同步减掉等量 USDT 多单
	if p.usdt.side != "LONG" || p.usdt.qty <= 0 {
		h.log.Warnf("减仓: %s 无 USDT 多单可同步减(side=%s qty=%.8f)，跳过", usdt, p.usdt.side, p.usdt.qty)
		return
	}
	qty := filled
	if qty > p.usdt.qty {
		qty = p.usdt.qty // 最多减到 USDT 多单数量，避免反手
	}

	h.log.Infof("减仓: %s 空单实际减仓 %.8f，同步 SELL 减 %s 多单 %.8f", usdc, filled, usdt, qty)
	if _, err := h.ws.NewOrder().
		Symbol(usdt).
		Side("SELL").
		PositionSide(p.usdt.side).
		Type("MARKET").
		Quantity(ftos(qty)).
		DoPlace(context.Background()); err != nil {
		h.log.Errorf("减仓: 同步减 %s 多单失败: %v", usdt, err)
	}
}
