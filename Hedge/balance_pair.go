// 数量平衡：当同一币种 USDC / USDT 两条腿数量不一致时，
// 减掉数量多的一边，使双向持仓重新平衡。实际下单在 balancePair。
package main

import "context"

// Balance 平衡对冲对：遍历所有持仓，对数量不一致的正向对冲对（USDC 空 + USDT 多）
// 异步调用 balancePair 减多的一边。
func (h *hedge) Balance(positions map[string]*pos) {
	for base, p := range positions {

		// 只处理正向对冲对：USDC 空单 + USDT 多单
		if p.usdc.qty == 0 || p.usdc.side != "SHORT" || p.usdt.qty == 0 || p.usdt.side != "LONG" {
			continue
		}
		diff := p.usdc.qty - p.usdt.qty
		if diff < 0 {
			diff = -diff
		}
		// 数量不一致超过浮点容差 → 异步平衡
		if diff > 1e-8 {
			go h.balancePair(base, p)
		}
	}
}

// balancePair 平衡单个对冲对：减掉数量多的那条腿，让两边数量一致。
func (h *hedge) balancePair(base string, p *pos) {
	usdcQty := p.usdc.qty
	usdtQty := p.usdt.qty
	if usdcQty == 0 || usdtQty == 0 {
		return
	}

	diff := usdcQty - usdtQty
	if diff < 0 {
		diff = -diff
	}
	if diff <= 1e-8 {
		return
	}

	// 找出数量多的一边
	var symbol string
	var lp leg
	if usdcQty > usdtQty {
		symbol = base + "USDC"
		lp = p.usdc
	} else {
		symbol = base + "USDT"
		lp = p.usdt
	}

	// 平仓方向：空单买回、多单卖出
	var side string
	switch lp.side {
	case "SHORT":
		side = "BUY"
	case "LONG":
		side = "SELL"
	default:
		return
	}

	// 市价平掉多余数量
	h.log.Infof("平衡: %s USDC=%.8f USDT=%.8f 差额=%.8f 市价 %s 平多余", base, usdcQty, usdtQty, diff, side)
	if _, err := h.ws.NewOrder().
		Symbol(symbol).
		Side(side).
		PositionSide(lp.side).
		Type("MARKET").
		Quantity(ftos(diff)).
		DoPlace(context.Background()); err != nil {
		h.log.Errorf("平衡: %s 平仓失败: %v", symbol, err)
		return
	}
}
