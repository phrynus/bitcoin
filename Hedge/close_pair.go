// 平异常对冲对：包括反向对冲对（USDC 多 + USDT 空）与规划外的正向对冲对。
// 实际下单在 closePair（正向）/ closeInverted（反向）。
package main

import (
	"context"

	"main/exchange"

	"github.com/adshao/go-binance/v2/futures"
)

// CloseIrregular 平异常对冲对，分两类：
//   - 反向对冲对（USDC 多 + USDT 空）：与开仓方向相反，一律平掉 → closeInverted
//   - 正向对冲对（USDC 空 + USDT 多）但不在规划（plans）内：规划外持仓，平掉 → closePair
func (h *hedge) CloseIrregular(positions map[string]*pos) {

	// 规划内的币（其正向对冲对保留）
	kept := make(map[string]bool, len(cfg.Plans))
	for _, p := range cfg.Plans {
		kept[p.Base] = true
	}

	for base, p := range positions {
		switch {
		case p.usdc.qty != 0 && p.usdc.side == "LONG" && p.usdt.qty != 0 && p.usdt.side == "SHORT":
			// 反向对冲对：一律平掉
			h.log.Warnf("平异常: %s 为反向对冲对(USDC多+USDT空)，平掉", base)
			h.closeInverted(base, p)
		case p.usdc.qty != 0 && p.usdc.side == "SHORT" && p.usdt.qty != 0 && p.usdt.side == "LONG":
			// 规划内的正向对冲对保留
			if kept[base] {
				continue
			}
			// 规划外正向对冲对：平掉
			h.log.Warnf("平异常: %s 为规划外正向对冲对，平掉", base)
			h.closePair(base, p)
		}
	}
}

// closePair 平正向对冲对（规划外）：
//  1. 取卖盘第二档价，BUY 限价平 USDC 空单（名义价值超过阈值时按 StepUsdt 分批，否则全平）
//  2. 订阅成交回调：空单成交后，市价 SELL 同步平掉等量 USDT 多单
//  3. 超时未成交则撤销挂单；下单失败则取消订阅
func (h *hedge) closePair(base string, p *pos) {
	usdc := base + "USDC"
	usdt := base + "USDT"
	lp := p.usdc
	closeAll := lp.value <= cfg.Close.StepNotional

	// 平 USDC 空单 = 买回
	svc := h.ws.NewOrder().
		Symbol(usdc).
		Side("BUY").
		PositionSide("SHORT").
		Type("LIMIT")
	if lp.value > cfg.Close.StepNotional {
		svc = svc.Usdt(cfg.Close.StepUsdt)
	} else {
		svc = svc.Quantity(ftos(lp.qty))
	}

	// 取卖盘第二档价作为限价（平仓 BUY → Ask，与开仓相反）
	price, err := h.depthLevel(usdc, true)
	if err != nil {
		h.log.Errorf("平异常: 获取 %s 盘口失败: %v", usdc, err)
		return
	}

	// 订阅成交事件：空单成交后同步平 USDT 多单
	id := exchange.GenID()
	done := h.stream.Subscribe(id, 2, func(o futures.WsOrderTradeUpdate) {
		filled := tof(o.AccumulatedFilledQty)

		// 同步平等量 USDT 多单（全平时平掉全部多单）
		qty := filled
		if closeAll || filled >= p.usdt.qty {
			qty = p.usdt.qty
		}
		h.log.Infof("平异常: %s 空单已成交 %.8f，同步平 %s 多单 %.8f", usdc, filled, usdt, qty)
		if _, err := h.ws.NewOrder().
			Symbol(usdt).
			Side("SELL").
			PositionSide(p.usdt.side).
			Type("MARKET").
			Quantity(ftos(qty)).
			ClientOrderID(exchange.GenID()).
			DoPlace(context.Background()); err != nil {
			h.log.Errorf("平异常: 同步平 %s 多单失败: %v", usdt, err)
		}
	}, cfg.Close.Timeout, func() {
		// 超时未成交：撤销挂单
		h.log.Warnf("平异常: %s 平空单 %s 在 %s 内未成交，撤销挂单", usdc, id, cfg.Close.Timeout)
		h.ws.NewCancel().
			Symbol(usdc).
			ClientOrderID(id).
			Do(context.Background())
	})

	// 挂 BUY 限价单
	h.log.Infof("平异常: 挂 BUY 限价单平 %s 空单(全平=%v) 限价=%s id=%s", usdc, closeAll, price, id)
	if _, err := svc.Price(price).ClientOrderID(id).DoPlace(context.Background()); err != nil {
		h.log.Errorf("平异常: 挂 %s 平仓单失败: %v", usdc, err)
		h.stream.Unsubscribe(id, 2)
		return
	}

	// 阻塞等待空单成交（或超时撤销），确保平对完成后再返回
	<-done
}

// closeInverted 平反向对冲对（USDC 多单 + USDT 空单，与开仓方向相反）：
//  1. 取买盘第二档价，SELL 限价平 USDC 多单（名义价值超过阈值时按 StepUsdt 分批，否则全平）
//  2. 订阅成交回调：多单成交后，市价 BUY 同步平掉等量 USDT 空单
//  3. 超时未成交则撤销挂单；下单失败则取消订阅
func (h *hedge) closeInverted(base string, p *pos) {
	usdc := base + "USDC"
	usdt := base + "USDT"
	lp := p.usdc
	closeAll := lp.value <= cfg.Close.StepNotional

	// 只处理 USDC 多单
	if lp.side != "LONG" {
		return
	}

	// 平 USDC 多单 = 卖出
	svc := h.ws.NewOrder().
		Symbol(usdc).
		Side("SELL").
		PositionSide("LONG").
		Type("LIMIT")
	if lp.value > cfg.Close.StepNotional {
		svc = svc.Usdt(cfg.Close.StepUsdt)
	} else {
		svc = svc.Quantity(ftos(lp.qty))
	}

	// 取买盘第二档价作为限价（平仓 SELL → Bid，与开仓相反）
	price, err := h.depthLevel(usdc, false)
	if err != nil {
		h.log.Errorf("平异常: 获取 %s 盘口失败: %v", usdc, err)
		return
	}

	// 订阅成交事件：多单成交后同步平 USDT 空单
	id := exchange.GenID()
	done := h.stream.Subscribe(id, 2, func(o futures.WsOrderTradeUpdate) {
		filled := tof(o.AccumulatedFilledQty)

		// USDT 空单用买回平仓
		if p.usdt.side != "SHORT" {
			h.log.Warnf("平异常: %s 的 USDT 腿非空单(side=%s)，跳过同步平仓", usdt, p.usdt.side)
			return
		}

		// 同步平等量 USDT 空单（全平时平掉全部空单）
		qty := filled
		if closeAll || filled >= p.usdt.qty {
			qty = p.usdt.qty
		}
		h.log.Infof("平异常: %s 多单已成交 %.8f，同步平 %s 空单 %.8f", usdc, filled, usdt, qty)
		if _, err := h.ws.NewOrder().
			Symbol(usdt).
			Side("BUY").
			PositionSide(p.usdt.side).
			Type("MARKET").
			Quantity(ftos(qty)).
			ClientOrderID(exchange.GenID()).
			DoPlace(context.Background()); err != nil {
			h.log.Errorf("平异常: 同步平 %s 空单失败: %v", usdt, err)
		}
	}, cfg.Close.Timeout, func() {
		// 超时未成交：撤销挂单
		h.log.Warnf("平异常: %s 平多单 %s 在 %s 内未成交，撤销挂单", usdc, id, cfg.Close.Timeout)
		h.ws.NewCancel().
			Symbol(usdc).
			ClientOrderID(id).
			Do(context.Background())
	})

	// 挂 SELL 限价单
	h.log.Infof("平异常: 挂 SELL 限价单平 %s 多单(全平=%v) 限价=%s id=%s", usdc, closeAll, price, id)
	if _, err := svc.Price(price).ClientOrderID(id).DoPlace(context.Background()); err != nil {
		h.log.Errorf("平异常: 挂 %s 平仓单失败: %v", usdc, err)
		h.stream.Unsubscribe(id, 2)
		return
	}

	// 阻塞等待多单成交（或超时撤销），确保平对完成后再返回
	<-done
}
