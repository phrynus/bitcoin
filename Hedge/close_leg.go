// 平单边持仓：当对冲对某一条腿已清空（数量为 0）而另一条腿仍有持仓时，
// 对冲已失效，市价平掉剩余的一条腿。实际下单在 closeLeg。
package main

import "context"

// CloseOneLeg 遍历所有持仓，识别单边持仓（USDC / USDT 仅一边有量）并异步平掉。
func (h *hedge) CloseOneLeg(positions map[string]*pos) {
	for base, p := range positions {

		// 两条腿都为 0 或都不为 0 → 不是单边，跳过
		if (p.usdc.qty != 0) == (p.usdt.qty != 0) {
			continue
		}
		h.log.Warnf("平单边: %s 对冲失效(USDC=%.8f USDT=%.8f)，平掉残余单边", base, p.usdc.qty, p.usdt.qty)
		// 只剩 USDC 腿
		if p.usdc.qty != 0 {
			go h.closeLeg(base+"USDC", p.usdc)
			continue
		}
		// 只剩 USDT 腿
		go h.closeLeg(base+"USDT", p.usdt)
	}
}

// closeLeg 市价平掉一条腿：
//   - 平仓方向由持仓方向推导：SHORT → BUY 买回，LONG → SELL 卖出；方向未知则跳过
//   - 名义价值超过步进阈值时，按盘口价把 StepUsdt 换算成数量分批平，否则全量平
func (h *hedge) closeLeg(symbol string, lp leg) {

	// 由持仓方向推导平仓方向
	var side string
	switch lp.side {
	case "SHORT":
		side = "BUY"
	case "LONG":
		side = "SELL"
	default:
		return
	}

	svc := h.ws.NewOrder().
		Symbol(symbol).
		Side(side).
		PositionSide(lp.side).
		Type("MARKET")
	if lp.value > cfg.Close.StepNotional {
		// 名义价值较大：按盘口反向价把 StepUsdt 换算成数量分批平
		// 平仓 SHORT→BUY 取卖一价 Ask，LONG→SELL 取买一价 Bid（与开仓相反）
		book, err := h.ws.Book(context.Background(), symbol)
		if err != nil {
			h.log.Errorf("平单边: 获取 %s 盘口失败: %v", symbol, err)
			return
		}
		price := book.BidPrice
		if side == "BUY" {
			price = book.AskPrice
		}
		svc = svc.Quantity(ftos(tof(cfg.Close.StepUsdt) / tof(price)))
	} else {
		// 名义价值较小：全量平
		svc = svc.Quantity(ftos(lp.qty))
	}

	h.log.Infof("平单边: 市价 %s 平 %s(全平=%v) 持仓=%.8f 名义价值=%.4f",
		side, symbol, lp.value <= cfg.Close.StepNotional, lp.qty, lp.value)
	if _, err := svc.DoPlace(context.Background()); err != nil {
		h.log.Errorf("平单边: %s 平仓失败: %v", symbol, err)
		return
	}
}
