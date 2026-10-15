package main

import (
	"context"
	"grid/exchange"

	"github.com/phrynus/plog"
)

// hpCoalesce 止盈挂单合并：同一网格立即执行，突发触发只补跑一次。
var hpCoalesce = NewCoalescer()

// CancelGridOrders 撤销网格当前挂出的买单与卖单，并清空数据库中的订单号。
// 订单号由调用方从数据库读出后传入，省去此处的重复查询。
// 交易所返回的「订单不存在/已成交」等业务错误只记录日志，不中断后续流程，保证整体订单增删不出错。
func CancelGridOrders(g Grid, isbid bool) {
	id := g.ID

	if isbid {
		plog.Debugf("[撤单] 网格 %d 撤销挂单 买=%q", id, g.BidID)
		if g.BidID != "" {
			if _, e := wa.NewCancel().Symbol(symbol).ClientOrderID(g.BidID).Do(context.Background()); e != nil && !orderGone(e) {
				plog.Warnf("[撤单] 撤销买单失败 网格 %d 订单 %s: %v", id, g.BidID, e)
			} else if e != nil {
				plog.Debugf("[撤单] 买单已不存在或已成交 网格 %d 订单 %s: %v", id, g.BidID, e)
			} else {
				plog.Debugf("[撤单] 已撤销买单 网格 %d 订单 %s", id, g.BidID)
			}
			if ud != nil {
				ud.Unsubscribe(g.BidID)
				plog.Debugf("[撤单] 已取消买单订阅 网格 %d 订单 %s", id, g.BidID)
			}
			if e := grids.Update(id, setBidID("")); e != nil {
				plog.Errorf("[撤单] 清空网格 %d 订单号失败: %v", id, e)
			}
		}
	} else {
		plog.Debugf("[撤单] 网格 %d 撤销挂单 卖=%q", id, g.AskID)
		if g.AskID != "" {
			if _, e := wa.NewCancel().Symbol(symbol).ClientOrderID(g.AskID).Do(context.Background()); e != nil && !orderGone(e) {
				plog.Warnf("[撤单] 撤销卖单失败 网格 %d 订单 %s: %v", id, g.AskID, e)
			} else if e != nil {
				plog.Debugf("[撤单] 卖单已不存在或已成交 网格 %d 订单 %s: %v", id, g.AskID, e)
			} else {
				plog.Debugf("[撤单] 已撤销卖单 网格 %d 订单 %s", id, g.AskID)
			}
			if ud != nil {
				ud.Unsubscribe(g.AskID)
				plog.Debugf("[撤单] 已取消卖单订阅 网格 %d 订单 %s", id, g.AskID)
			}
			if e := grids.Update(id, setAskID("")); e != nil {
				plog.Errorf("[撤单] 清空网格 %d 订单号失败: %v", id, e)
			}
		}
	}

	plog.Infof("[撤单] 网格 %d 撤销完成", id)
}

// 挂止盈（合并高频触发，无定时等待）
func HangingProfit(i int64, filledQty string) {
	g, ok := grids.Get(i)
	if !ok {
		plog.Errorf("[止盈] 查询网格 %d 失败: 网格不存在", i)
		return
	}

	// 防抖：仅当「已有止盈单」且成交量未增加时才跳过，避免重复改单。
	// 若止盈单缺失（下单失败/尚未挂出），即便成交量未变也必须补挂，
	// 否则残留的 bid_id 会被误判为「买单仍在挂」而让该格被永久跳过。
	if g.AskID != "" && exchange.ToFloat64(filledQty) <= exchange.ToFloat64(g.FilledQty) {
		plog.Debugf("[止盈] 网格 %d 已有等量止盈单(本次=%s <= 已记录=%s), 跳过止盈挂单", i, filledQty, g.FilledQty)
		return
	}

	plog.Debugf("[止盈] 网格 %d 触发止盈挂单 本次成交=%s 已记录=%s, 交由合并器串行化", i, filledQty, g.FilledQty)

	// 立即执行；执行期间的重复触发会合并为一次补跑
	hpCoalesce.Do(exchange.ToString(i), func() {
		doHangingProfit(i, filledQty)
	})
}

// EnsureHangingProfit 补挂止盈：把「已成交但缺失出场单」的格子重新挂上止盈单。
// 与成交回调共用合并器按网格串行化，执行时重新读取数据库：已有止盈单或无成交数量时跳过，
// 以此避免「读到 ask_id 为空 → 并发重复下只减仓卖单」导致币安返回 -2022。
// 与 HangingProfit 的区别：不做「本次成交量是否增加」的防抖——补挂时成交量已不再变化。
func EnsureHangingProfit(i int64) {
	hpCoalesce.Do(exchange.ToString(i), func() {
		g, ok := grids.Get(i)
		if !ok {
			plog.Errorf("[止盈] 查询网格 %d 失败: 网格不存在", i)
			return
		}
		if g.AskID != "" {
			plog.Debugf("[止盈] 网格 %d 已有止盈单(%s), 跳过补挂", i, g.AskID)
			return
		}
		if g.FilledQty == "" {
			plog.Debugf("[止盈] 网格 %d 无成交数量记录, 跳过补挂止盈", i)
			return
		}
		doHangingProfit(i, g.FilledQty)
	})
}

// 挂止盈
func doHangingProfit(i int64, filledQty string) {
	g, ok := grids.Get(i)
	if !ok {
		plog.Errorf("[止盈] 查询网格 %d 失败: 网格不存在", i)
		return
	}

	plog.Debugf("[止盈] 网格 %d 执行止盈挂单 止盈价=%s 数量=%s 已有卖单=%q", i, g.AskPrice, filledQty, g.AskID)

	// 已有止盈单且数量未增加：现存的止盈单已覆盖该持仓，再改一次属于空操作
	// （价格固定、数量不变），币安会直接返回 -5027。此处先跳过，避免无意义的改单请求。
	// 与 HangingProfit 的防抖一致，使合并器的补跑路径也能生效。
	if g.AskID != "" && exchange.ToFloat64(filledQty) <= exchange.ToFloat64(g.FilledQty) {
		plog.Debugf("[止盈] 网格 %d 已有等量止盈单(本次=%s <= 已记录=%s), 跳过改单", i, filledQty, g.FilledQty)
		return
	}

	// 先记录成交量（持仓事实），与止盈单能否挂成功解耦：
	// 即使止盈挂单失败，窗口变化时也能凭 filled_qty 识别为「持仓待止盈」并补挂，
	// 不会被残留的 bid_id 当成「买单仍在挂」而永久跳过。
	// 仅在数值确实变化时才写库，避免同一成交量的重复事件反复触发无意义的写事务。
	if g.FilledQty != filledQty {
		if e := grids.Update(i, setFilledQty(filledQty)); e != nil {
			plog.Errorf("[止盈] 网格 %d 记录成交数量失败: %v", i, e)
		}
	}

	svc := wa.NewOrder().
		Symbol(symbol).
		Side("SELL").
		PositionSide("LONG").
		// TimeInForce("GTX").
		Type("LIMIT").
		Quantity(filledQty).
		Price(g.AskPrice)

	var err error
	var d *exchange.WsOrder
	if g.AskID == "" {
		id := exchange.ToString(i) + exchange.GenID()
		plog.Debugf("[止盈] 网格 %d 新建止盈单 客户端订单号=%s", i, id)
		d, err = svc.NewClientOrderID(id).DoPlace(context.Background())
		if err == nil {
			if ud != nil {
				ud.Subscribe(id, func(status string, o exchange.WsOrderTradeUpdate) {
					plog.Debugf("[止盈] 网格 %d 止盈单事件 订单=%s 状态=%s 本次成交=%s 累计成交=%s",
						i, o.ClientOrderID, status, o.LastFilledQty, o.AccumulatedFilledQty)
					if status == "FILLED" {
						plog.Infof("[成交] 网格 %d 止盈成交 均价=%s 数量=%s 实现盈亏=%s",
							i, o.AveragePrice, o.AccumulatedFilledQty, o.RealizedPnL)
						RecordFill(FillLongOut, o.AccumulatedFilledQty, o.RealizedPnL)
						// 止盈成交：清空该格订单信息（含买单号），交回窗口重新挂买单
						plog.Debugf("[止盈] 网格 %d 止盈完成, 清空订单号并回退窗口", i)
						if e := grids.Update(i, setBidID(""), setAskID(""), setFilledQty("")); e != nil {
							plog.Errorf("[止盈] 网格 %d 清空订单号失败: %v", i, e)
						}
						if headOrder && headOrderID == i {
							plog.Infof("[头单] 网格 %d 头单止盈成交平仓, 头单结束", i)
							headOrder = false
						}
						ud.Unsubscribe(id)
						IntervalVariation(i - 1)
					}
				})
			}
			// filled_qty 已在上方写入，这里只补挂新的止盈单号，避免二次写库。
			if e := grids.Update(g.ID, setAskID(id)); e != nil {
				plog.Errorf("[止盈] 网格 %d 记录止盈单号失败: %v", i, e)
			}
		} else {
			// 下单失败时 d 为 nil；若在这里返回，可避免末尾 d.ClientOrderID 触发空指针 panic。
			plog.Errorf("[止盈] 网格 %d 止盈挂单失败: %v", i, err)
			return
		}
	} else {
		plog.Debugf("[止盈] 网格 %d 修改已有止盈单 订单=%s", i, g.AskID)
		d, err = svc.ClientOrderID(g.AskID).DoModify(context.Background())
		// filled_qty 已在函数开头写入，改单成功无需重复写库。
		// -5027「No need to modify the order」表示订单参数已与目标一致，属正常情况，视为已挂好。
		if err != nil {
			plog.Errorf("[止盈] 网格 %d 止盈挂单失败: %v", i, err)
			return
		}

	}

	if d == nil {
		plog.Warnf("[止盈] 网格 %d 挂单结果为空, 跳过成功日志", i)
		return
	}
	plog.Infof("[止盈] 网格 %d 止盈挂单成功 订单=%s 价格=%s 数量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)
}

// 挂下单
func HangTheOrder(i int64, price, qty string) {
	id := exchange.ToString(i) + exchange.GenID()
	plog.Debugf("[买单] 网格 %d 准备挂买单 价格=%s 数量=%q 客户端订单号=%s", i, price, qty, id)

	svc := wa.NewOrder().
		Symbol(symbol).
		Side("BUY").
		PositionSide("LONG").
		// TimeInForce("GTX").
		Type("LIMIT").
		Price(price).
		NewClientOrderID(id)
	if qty == "" {
		plog.Debugf("[买单] 网格 %d 未提供数量, 按名义价值 %dU 下单", i, usdt)
		svc.Usdt(exchange.ToString(usdt))
	} else {
		svc.Quantity(qty)
	}

	if ud != nil {
		ud.Subscribe(id, func(status string, o exchange.WsOrderTradeUpdate) {
			plog.Debugf("[买单] 网格 %d 买单事件 订单=%s 状态=%s 本次成交=%s 累计成交=%s",
				i, o.ClientOrderID, status, o.LastFilledQty, o.AccumulatedFilledQty)
			if status == "FILLED" {
				// 买单完全成交：交易所已无该挂单。清空买单号、以 filled_qty 记录持仓，
				// 避免「数据库有买单号、交易所却无此单」的幽灵挂单把该格当成「仍在挂单」而永久跳过。
				// 止盈由 HangingProfit 负责挂出，待止盈成交后再清空本格重新挂买单。
				plog.Infof("[成交] 网格 %d 买单完全成交 均价=%s 数量=%s",
					i, o.AveragePrice, o.AccumulatedFilledQty)
				RecordFill(FillLongIn, o.AccumulatedFilledQty, o.RealizedPnL)
				grids.Update(i, setFilledQty(o.AccumulatedFilledQty), setBidID(""))
				plog.Debugf("[买单] 网格 %d 买单成交, 窗口下移中心至 %d", i, i+1)
				ud.Unsubscribe(o.ClientOrderID)
				IntervalVariation(i + 1)
			}
			if status == "PARTIALLY_FILLED" || status == "FILLED" {
				HangingProfit(i, o.AccumulatedFilledQty)
			}
		})
	} else {
		plog.Warnf("[买单] 网格 %d 用户数据流未就绪, 跳过成交订阅", i)
	}

	// 记录买单号：网格已存在则只更新 BidID，避免重复下单时覆盖其他字段。
	if e := grids.Update(i, setBidID(id)); e != nil {
		if ud != nil {
			ud.Unsubscribe(id)
		}
		plog.Errorf("[买单] 网格 %d 记录买单号失败: %v", i, e)
		return
	}

	d, err := svc.DoPlace(context.Background())
	if err != nil {
		if ud != nil {
			ud.Unsubscribe(id)
		}
		// 下单失败回滚买单号，保持记录与实际一致
		plog.Debugf("[买单] 网格 %d 下单失败, 回滚已写入的买单号", i)
		grids.Update(i, setBidID(""))
		plog.Errorf("[买单] 网格 %d 买下单失败: %v", i, err)
		return
	}
	plog.Debugf("[买单] 网格 %d 交易所已接受买单 订单号=%s 成交价=%s 委托量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)

	// sellPrice := exchange.ToString(exchange.ToFloat64(price) * (1 + spacing/10000))
	// s, ok := exchange.Exc.GetSymbol(symbol)
	// if !ok {
	// 	plog.Warnf("[买单] 交易对 %s 不在交易对列表中, 止盈价按原值使用", symbol)
	// }
	// fp, _, err := exchange.FixOrderText(s, sellPrice, d.OrigQty)
	// if err != nil {
	// 	plog.Warnf("[买单] 网格 %d 计算止盈价失败(原始=%s): %v", i, sellPrice, err)
	// 	return
	// }

	plog.Infof("[买单] 网格 %d 买单已挂出 订单=%s 买价=%s 数量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)
}

// 市价下单
func MarketEnter(i int64, price, qty string) {

	id := exchange.ToString(i) + exchange.GenID()
	plog.Debugf("[买单] 网格 %d 准备挂买单 数量=%q 客户端订单号=%s", i, qty, id)

	svc := wa.NewOrder().
		Symbol(symbol).
		Side("BUY").
		PositionSide("LONG").
		// TimeInForce("GTX").
		Type("MARKET").
		Price(price).
		NewClientOrderID(id)
	if qty == "" {
		plog.Debugf("[买单] 网格 %d 未提供数量, 按名义价值 %dU 下单", i, usdt)
		svc.Usdt(exchange.ToString(usdt))
	} else {
		svc.Quantity(qty)
	}

	// if ud != nil {
	// 	ud.Subscribe(id, func(status string, o exchange.WsOrderTradeUpdate) {
	// 		plog.Debugf("[买单] 网格 %d 买单事件 订单=%s 状态=%s 本次成交=%s 累计成交=%s",
	// 			i, o.ClientOrderID, status, o.LastFilledQty, o.AccumulatedFilledQty)
	// 		if status == "FILLED" {
	// 			// 买单完全成交：保留买单号作为「已有挂单/持仓」的判断依据，
	// 			// 止盈由 HangingProfit 负责挂出，待止盈成交后再清空本格重新挂买单
	// 			plog.Infof("[成交] 网格 %d 买单完全成交 均价=%s 数量=%s",
	// 				i, o.AveragePrice, o.AccumulatedFilledQty)
	// 			plog.Debugf("[买单] 网格 %d 买单成交, 窗口下移中心至 %d", i, i+1)
	// 			ud.Unsubscribe(o.ClientOrderID)
	// 			IntervalVariation(i + 1)
	// 		}
	// 		if status == "PARTIALLY_FILLED" || status == "FILLED" {
	// 			HangingProfit(i, o.AccumulatedFilledQty)
	// 		}
	// 	})
	// } else {
	// 	plog.Warnf("[买单] 网格 %d 用户数据流未就绪, 跳过成交订阅", i)
	// }

	// 记录买单号：网格已存在则只更新 BidID，避免重复下单时覆盖其他字段。
	if e := grids.Update(i, setBidID(id)); e != nil {
		plog.Errorf("[买单] 网格 %d 记录买单号失败: %v", i, e)
		return
	}

	d, err := svc.DoPlace(context.Background())
	if err != nil {
		// if ud != nil {
		// 	ud.Unsubscribe(id)
		// }
		// 下单失败回滚买单号，保持记录与实际一致
		plog.Debugf("[买单] 网格 %d 下单失败, 回滚已写入的买单号", i)
		grids.Update(i, setBidID(""))
		plog.Errorf("[买单] 网格 %d 买下单失败: %v", i, err)
		return
	}
	headOrder = true
	headOrderID = i
	// 市价单即时成交，交易所不会再保留该挂单：以 filled_qty 记录持仓并清空 bid_id，
	// 避免「记录有买单号、交易所却无此单」的幽灵挂单把该格当成「仍在挂单」而永久跳过。
	grids.Update(i, setFilledQty(d.OrigQty), setBidID(""))
	plog.Infof("[头单] 网格 %d 头单买入建仓 订单=%s 委托量=%s, 后续中心及下方改用限价买单", i, d.ClientOrderID, d.OrigQty)
	// 市价头单即时成交，此处补记一笔成交统计（无实现盈亏）
	RecordFill(FillLongIn, d.OrigQty, "")
	HangingProfit(i, d.OrigQty)

	// 避免死锁
	go IntervalVariation(i + 1)

	plog.Debugf("[买单] 网格 %d 交易所已接受买单 订单号=%s 成交价=%s 委托量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)

	// sellPrice := exchange.ToString(exchange.ToFloat64(price) * (1 + spacing*2/10000))
	// s, ok := exchange.Exc.GetSymbol(symbol)
	// if !ok {
	// 	plog.Warnf("[买单] 交易对 %s 不在交易对列表中, 止盈价按原值使用", symbol)
	// }
	// fp, err := exchange.FixPriceText(s, sellPrice)
	// if err != nil {
	// 	plog.Warnf("[买单] 网格 %d 计算止盈价失败(原始=%s): %v", i, sellPrice, err)
	// 	return
	// }

	// plog.Infof("[买单] 网格 %d 买单已挂出 订单=%s 买价=%s 数量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)

}
