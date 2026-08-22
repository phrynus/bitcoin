// 加仓：当保证金比率低于基准点时，为规划中持仓价值最小的币新开一对对冲
// （USDC 空单 + USDT 多单），用满单次加仓金额。
package main

import (
	"context"

	"main/exchange"

	"github.com/adshao/go-binance/v2/futures"
)

// AddPair 加仓：在规划标的中挑持仓价值最小的币（未持仓按 0 计），
// 满足持仓未达上限、余额充足后开一对新对冲。实际下单在 addPair。
func (h *hedge) AddPair(s *snap) {
	if s == nil {
		return
	}

	// 挑选规划中当前持仓价值最小的币
	var pick *Plan
	minValue := 0.0
	first := true
	for i := range cfg.Plans {
		p := &cfg.Plans[i]
		v := 0.0
		if pd := s.positions[p.Base]; pd != nil {
			v = pd.value
		}
		if first || v < minValue {
			first = false
			minValue = v
			pick = p
		}
	}
	if pick == nil {
		h.log.Warnf("加仓: 规划列表为空，无法加仓")
		return
	}

	// 持仓价值已达上限（cap × cap_ratio）→ 不加仓
	capValue := pick.Cap * cfg.Add.CapRatio
	if minValue >= capValue {
		h.log.Infof("加仓: %s 持仓价值 %.4f 已达上限 %.4f，跳过加仓", pick.Base, minValue, capValue)
		return
	}

	

	h.log.Infof("加仓: 选择持仓价值最小的 %s(当前 %.4f)，开一对新对冲(金额=%s USDT)",
		pick.Base, minValue, ftos(pick.Usdt))
	h.addPair(pick)
}

// addPair 执行开一对对冲：
//  1. 取 USDC 卖盘第二档价，SELL 限价开 USDC 空单（金额 = plan.Usdt）
//  2. 订阅成交回调，等空单全部成交后，市价 BUY 开等量 USDT 多单（保持双向平衡）
//  3. 超时未成交则撤销挂单；下单失败则取消订阅
func (h *hedge) addPair(plan *Plan) {
	usdc := plan.Base + "USDC"
	usdt := plan.Base + "USDT"

	// 取卖盘第二档价作为限价（开仓 SELL → Ask）
	price, err := h.depthLevel(usdc, true)
	if err != nil {
		h.log.Errorf("加仓: 获取 %s 盘口失败: %v", usdc, err)
		return
	}

	// 订阅成交事件：空单成交后同步开多单
	id := exchange.GenID()
	done := h.stream.Subscribe(id, 2, func(o futures.WsOrderTradeUpdate) {
		filled := tof(o.AccumulatedFilledQty)
		if filled <= 0 {
			return
		}

		h.log.Infof("加仓: %s 空单已成交 %.8f，同步市价开 %s 多单", usdc, filled, usdt)
		// 同步市价开等量 USDT 多单，保证双向持仓数量平衡
		if _, err := h.ws.NewOrder().
			Symbol(usdt).
			Side("BUY").
			PositionSide("LONG").
			Type("MARKET").
			Quantity(ftos(filled)).
			ClientOrderID(exchange.GenID()).
			DoPlace(context.Background()); err != nil {
			h.log.Errorf("加仓: 同步开 %s 多单失败: %v", usdt, err)
		}
	}, cfg.Add.Timeout, func() {
		// 超时未成交：撤销挂单
		h.log.Warnf("加仓: %s 空单 %s 在 %s 内未成交，撤销挂单", usdc, id, cfg.Add.Timeout)
		h.ws.NewCancel().
			Symbol(usdc).
			ClientOrderID(id).
			Do(context.Background())
	})

	// 挂 SELL 限价空单
	h.log.Infof("加仓: 挂 SELL 限价空单 %s 金额=%s USDT 限价=%s id=%s", usdc, ftos(plan.Usdt), price, id)
	if _, err := h.ws.NewOrder().
		Symbol(usdc).
		Side("SELL").
		PositionSide("SHORT").
		Type("LIMIT").
		Usdt(ftos(plan.Usdt)).
		Price(price).
		ClientOrderID(id).
		DoPlace(context.Background()); err != nil {
		h.log.Errorf("加仓: 挂 %s 空单失败: %v", usdc, err)
		h.stream.Unsubscribe(id, 2)
		return
	}

	// 阻塞等待空单成交（或超时撤销），确保开对完成后再返回
	<-done
}
