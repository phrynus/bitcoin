package main

import (
	"context"
)

// ReduceOverCap 超上限减仓：遍历规划内的币，对持仓价值超过上限的正向对冲对异步减仓。
// 减仓金额使用平仓规则 cfg.Close（与保证金率减仓的 cfg.Reduce 区分）。
func (h *hedge) ReduceOverCap(positions map[string]*pos) {
	for i := range cfg.Plans {
		plan := &cfg.Plans[i]
		p := positions[plan.Base]
		if p == nil {
			continue
		}
		// 触发上限 = cap × over.ratio（独立参数，与加仓的 cap_ratio 区分）
		capValue := plan.Cap * cfg.Over.Ratio
		if p.value <= capValue {
			continue
		}
		h.log.Warnf("超上限减仓: %s 持仓价值 %.4f 超过上限 %.4f(×%.2f)，触发减仓", plan.Base, p.value, capValue, cfg.Over.Ratio)
		go h.reduceOverPair(plan.Base, p)
	}
}

// reduceOverPair 执行超上限减仓（针对单个对冲对）：
//  1. 市价 BUY 平掉 USDC 空单，数量 = over.step_usdt 按盘口 Ask 价换算（固定金额，不判断大小）
//  2. 从下单返回的成交量得到实际减仓数量
//  3. 市价 SELL 同步减掉等量 USDT 多单，保持双向持仓平衡
func (h *hedge) reduceOverPair(base string, p *pos) {
	usdc := base + "USDC"
	usdt := base + "USDT"
	lp := p.usdc

	// 只处理 usdc 空单
	if p.usdc.side != "SHORT" || p.usdc.qty <= 0 {
		return
	}

	// 按盘口反向价（卖一价 Ask）把单次固定金额换算成数量（平仓 BUY），不判断持仓大小
	book, err := h.ws.Book(context.Background(), usdc)
	if err != nil {
		h.log.Errorf("超上限减仓: 获取 %s 盘口失败: %v", usdc, err)
		return
	}

	// 市价下单并返回成交结果（RESULT 含成交量）
	h.log.Infof("超上限减仓: 市价 BUY 减 %s 空单 金额=%s USDT 持仓=%.8f 名义价值=%.4f",
		usdc, cfg.Over.StepUsdt, lp.qty, lp.value)
	order, err := h.ws.NewOrder().
		Symbol(usdc).
		Side("BUY").
		PositionSide("SHORT").
		Type("MARKET").
		Quantity(ftos(tof(cfg.Over.StepUsdt) / tof(book.AskPrice))).
		RespType("RESULT").
		DoPlace(context.Background())
	if err != nil {
		h.log.Errorf("超上限减仓: %s 空单减仓失败: %v", usdc, err)
		return
	}

	filled := tof(order.OrigQty)
	if filled <= 0 {
		h.log.Warnf("超上限减仓: %s 空单成交量为 0，跳过同步减多单", usdc)
		return
	}

	// 同步减掉等量 USDT 多单
	if p.usdt.side != "LONG" || p.usdt.qty <= 0 {
		h.log.Warnf("超上限减仓: %s 无 USDT 多单可同步减(side=%s qty=%.8f)，跳过", usdt, p.usdt.side, p.usdt.qty)
		return
	}
	qty := filled
	if qty > p.usdt.qty {
		qty = p.usdt.qty // 最多减到 USDT 多单数量，避免反手
	}

	h.log.Infof("超上限减仓: %s 空单实际减仓 %.8f，同步 SELL 减 %s 多单 %.8f", usdc, filled, usdt, qty)
	if _, err := h.ws.NewOrder().
		Symbol(usdt).
		Side("SELL").
		PositionSide(p.usdt.side).
		Type("MARKET").
		Quantity(ftos(qty)).
		DoPlace(context.Background()); err != nil {
		h.log.Errorf("超上限减仓: 同步减 %s 多单失败: %v", usdt, err)
	}
}
