package main

import (
	"context"
	"main/exchange"

	"github.com/phrynus/plog"
)

// 空单模块：与多单（order.go）完全镜像、独立管理。
// 多单在 BidPrice 挂买单建仓、在 AskPrice 挂卖单止盈；
// 空单则在 AskPrice 挂卖单开空、在 BidPrice 挂买单回补止盈（平空）。
// 订单号分别存放在 EmptyAskID（开空卖单）与 EmptyBidID（回补买单），
// 成交数量存放在 EmptyFilledQty，与多单的 BidID/AskID/FilledQty 互不干扰，
// 两者可同时在同一张网格上运行（对冲模式下 PositionSide 分别为 LONG / SHORT）。
// 日志标签：[空单] 开空 [空止盈] 回补 [空撤单] 撤单 [空成交] 成交 [空头单] 头单。

// epCoalesce 空单止盈挂单合并：同一网格立即执行，突发触发只补跑一次。
var epCoalesce = NewCoalescer()

// EmptyCancelGridOrders 撤销空单网格当前挂出的开空卖单与回补买单，并清空数据库中的订单号。
// isask 为 true 撤销开空卖单（EmptyAskID），否则撤销回补买单（EmptyBidID）。
// 交易所返回的「订单不存在/已成交」等业务错误只记录日志，不中断后续流程，保证整体订单增删不出错。
func EmptyCancelGridOrders(g Grid, isask bool) {
	id := g.ID

	if isask {
		plog.Debugf("[空撤单] 网格 %d 撤销挂单 卖=%q", id, g.EmptyAskID)
		if g.EmptyAskID != "" {
			if _, e := wa.NewCancel().Symbol(symbol).ClientOrderID(g.EmptyAskID).Do(context.Background()); e != nil && !orderGone(e) {
				plog.Warnf("[空撤单] 撤销开空卖单失败 网格 %d 订单 %s: %v", id, g.EmptyAskID, e)
			} else if e != nil {
				plog.Debugf("[空撤单] 开空卖单已不存在或已成交 网格 %d 订单 %s: %v", id, g.EmptyAskID, e)
			} else {
				plog.Debugf("[空撤单] 已撤销开空卖单 网格 %d 订单 %s", id, g.EmptyAskID)
			}
			if ud != nil {
				ud.Unsubscribe(g.EmptyAskID)
				plog.Debugf("[空撤单] 已取消开空卖单订阅 网格 %d 订单 %s", id, g.EmptyAskID)
			}
			if e := grids.Update(id, setEmptyAskID("")); e != nil {
				plog.Errorf("[空撤单] 清空网格 %d 空单卖单号失败: %v", id, e)
			}
		}
	} else {
		plog.Debugf("[空撤单] 网格 %d 撤销挂单 买=%q", id, g.EmptyBidID)
		if g.EmptyBidID != "" {
			if _, e := wa.NewCancel().Symbol(symbol).ClientOrderID(g.EmptyBidID).Do(context.Background()); e != nil && !orderGone(e) {
				plog.Warnf("[空撤单] 撤销回补买单失败 网格 %d 订单 %s: %v", id, g.EmptyBidID, e)
			} else if e != nil {
				plog.Debugf("[空撤单] 回补买单已不存在或已成交 网格 %d 订单 %s: %v", id, g.EmptyBidID, e)
			} else {
				plog.Debugf("[空撤单] 已撤销回补买单 网格 %d 订单 %s", id, g.EmptyBidID)
			}
			if ud != nil {
				ud.Unsubscribe(g.EmptyBidID)
				plog.Debugf("[空撤单] 已取消回补买单订阅 网格 %d 订单 %s", id, g.EmptyBidID)
			}
			if e := grids.Update(id, setEmptyBidID("")); e != nil {
				plog.Errorf("[空撤单] 清空网格 %d 空单买单号失败: %v", id, e)
			}
		}
	}

	plog.Infof("[空撤单] 网格 %d 撤销完成", id)
}

// EmptyHangingProfit 挂空单止盈（回补）：开空成交后按成交数量在 BidPrice 挂买单平空。
// 合并高频触发，无定时等待。
func EmptyHangingProfit(i int64, filledQty string) {
	g, ok := grids.Get(i)
	if !ok {
		plog.Errorf("[空止盈] 查询网格 %d 失败: 网格不存在", i)
		return
	}

	// 防抖：仅当「已有回补单」且成交量未增加时才跳过，避免重复改单。
	// 若回补单缺失（下单失败/尚未挂出），即便成交量未变也必须补挂，
	// 否则残留的 empty_bid_id 会被误判为「已有出场单」而让该格被永久跳过。
	if g.EmptyBidID != "" && exchange.ToFloat64(filledQty) <= exchange.ToFloat64(g.EmptyFilledQty) {
		plog.Debugf("[空止盈] 网格 %d 已有等量回补单(本次=%s <= 已记录=%s), 跳过止盈挂单", i, filledQty, g.EmptyFilledQty)
		return
	}

	plog.Debugf("[空止盈] 网格 %d 触发止盈挂单 本次成交=%s 已记录=%s, 交由合并器串行化", i, filledQty, g.EmptyFilledQty)

	// 立即执行；执行期间的重复触发会合并为一次补跑
	epCoalesce.Do(exchange.ToString(i), func() {
		doEmptyHangingProfit(i, filledQty)
	})
}

// EmptyEnsureHangingProfit 补挂空单止盈：把「已开空但缺失回补单」的格子重新挂上买单。
// 与成交回调共用合并器按网格串行化，执行时重新读取数据库：已有回补单或无成交数量时跳过，
// 以此避免并发重复下回补买单导致币安返回 -2022。
func EmptyEnsureHangingProfit(i int64) {
	epCoalesce.Do(exchange.ToString(i), func() {
		g, ok := grids.Get(i)
		if !ok {
			plog.Errorf("[空止盈] 查询网格 %d 失败: 网格不存在", i)
			return
		}
		if g.EmptyBidID != "" {
			plog.Debugf("[空止盈] 网格 %d 已有回补单(%s), 跳过补挂", i, g.EmptyBidID)
			return
		}
		if g.EmptyFilledQty == "" {
			plog.Debugf("[空止盈] 网格 %d 无成交数量记录, 跳过补挂止盈", i)
			return
		}
		doEmptyHangingProfit(i, g.EmptyFilledQty)
	})
}

// emptyHeadExitPrice 计算空单「市价头单」的回补（止盈）价：取下方相邻格子的买价。
// 市价卖单按买一成交（≈本格 BidPrice），若仍用本格 BidPrice 回补：
//  1. 建仓价与平仓价相同，一格利润也没有；
//  2. 价格紧贴市价，会被币安 POST_ONLY(GTX) 判为「会立即吃单」而整单拒掉（-5022）。
//
// 因此头单止盈价下移一格：既保证一格利润，也保证挂单是 maker。
func emptyHeadExitPrice(g Grid) string {
	// 正常情况下下方相邻格子已由窗口补全，直接取它的买价（与网格阶梯完全对齐）。
	if next, ok := grids.Get(g.ID + 1); ok && next.BidPrice != "" {
		return next.BidPrice
	}

	// 下方相邻格子缺失时按本格间距推算，并按最小变动修正，避免出现非法价格。
	s, ok := exchange.Exc.GetSymbol(symbol)
	if !ok {
		plog.Warnf("[空止盈] 交易对 %s 不在交易对列表中, 头单止盈价按原值使用", symbol)
	}
	raw := exchange.ToString(exchange.ToFloat64(g.BidPrice) - exchange.ToFloat64(g.Space))
	fp, err := exchange.FixPriceText(s, raw)
	if err != nil {
		plog.Warnf("[空止盈] 网格 %d 计算头单止盈价失败(原始=%s): %v, 退回本格买价", g.ID, raw, err)
		return g.BidPrice
	}
	return fp
}

// doEmptyHangingProfit 挂空单止盈（挂 BUY 平空）。
// 普通格子回补到本格 BidPrice：限价开空卖单在 AskPrice 成交，回补到 BidPrice 恰好赚一格；
// 市价头单成交价≈本格买一，必须下移一格（见 emptyHeadExitPrice）。
func doEmptyHangingProfit(i int64, filledQty string) {
	g, ok := grids.Get(i)
	if !ok {
		plog.Errorf("[空止盈] 查询网格 %d 失败: 网格不存在", i)
		return
	}

	exitPrice := g.BidPrice
	if emptyHeadOrder && emptyHeadOrderID == g.ID {
		exitPrice = emptyHeadExitPrice(g)
	}

	plog.Debugf("[空止盈] 网格 %d 执行止盈挂单 止盈价=%s 数量=%s 已有回补单=%q", i, exitPrice, filledQty, g.EmptyBidID)

	// 已有回补单且数量未增加：现存回补单已覆盖该持仓，再改一次属于空操作
	// （价格固定、数量不变），币安会直接返回 -5027。此处先跳过，避免无意义的改单请求。
	if g.EmptyBidID != "" && exchange.ToFloat64(filledQty) <= exchange.ToFloat64(g.EmptyFilledQty) {
		plog.Debugf("[空止盈] 网格 %d 已有等量回补单(本次=%s <= 已记录=%s), 跳过改单", i, filledQty, g.EmptyFilledQty)
		return
	}

	// 先记录成交量（持仓事实），与回补单能否挂成功解耦：
	// 即使回补挂单失败，窗口变化时也能凭 empty_filled_qty 识别为「持仓待止盈」并补挂。
	// 仅在数值确实变化时才写库，避免同一成交量的重复事件反复触发无意义的写事务。
	if g.EmptyFilledQty != filledQty {
		if e := grids.Update(i, setEmptyFilledQty(filledQty)); e != nil {
			plog.Errorf("[空止盈] 网格 %d 记录成交数量失败: %v", i, e)
		}
	}

	svc := wa.NewOrder().
		Symbol(symbol).
		Side("BUY").
		PositionSide("SHORT").
		TimeInForce("GTX").
		Type("LIMIT").
		Quantity(filledQty).
		Price(exitPrice)

	var err error
	var d *exchange.WsOrder
	if g.EmptyBidID == "" {
		id := exchange.ToString(i) + exchange.GenID()
		plog.Debugf("[空止盈] 网格 %d 新建回补单 客户端订单号=%s", i, id)
		d, err = svc.NewClientOrderID(id).DoPlace(context.Background())
		if err == nil {
			if ud != nil {
				ud.Subscribe(id, func(status string, o exchange.WsOrderTradeUpdate) {
					plog.Debugf("[空止盈] 网格 %d 回补单事件 订单=%s 状态=%s 本次成交=%s 累计成交=%s",
						i, o.ClientOrderID, status, o.LastFilledQty, o.AccumulatedFilledQty)
					if status == "FILLED" {
						plog.Infof("[空成交] 网格 %d 空单止盈成交 均价=%s 数量=%s 实现盈亏=%s",
							i, o.AveragePrice, o.AccumulatedFilledQty, o.RealizedPnL)
						RecordFill(FillShortOut, o.AccumulatedFilledQty, o.RealizedPnL)
						// 空单止盈成交：清空该格订单信息（含开空卖单号），交回窗口重新挂开空单
						plog.Debugf("[空止盈] 网格 %d 止盈完成, 清空订单号并回退窗口", i)
						if e := grids.Update(i, setEmptyBidID(""), setEmptyAskID(""), setEmptyFilledQty("")); e != nil {
							plog.Errorf("[空止盈] 网格 %d 清空订单号失败: %v", i, e)
						}
						if emptyHeadOrder && emptyHeadOrderID == i {
							plog.Infof("[空头单] 网格 %d 头单止盈成交平仓, 头单结束", i)
							emptyHeadOrder = false
						}
						ud.Unsubscribe(id)
						// 空单止盈是「买回」，价格回落 → 窗口下移（编号增大），与多单方向相反
						EmptyIntervalVariation(i + 1)
					}
				})
			}
			// empty_filled_qty 已在上方写入，这里只补挂新的回补单号，避免二次写库。
			if e := grids.Update(g.ID, setEmptyBidID(id)); e != nil {
				plog.Errorf("[空止盈] 网格 %d 记录回补单号失败: %v", i, e)
			}
		} else {
			// 下单失败时 d 为 nil；若在这里返回，可避免末尾 d.ClientOrderID 触发空指针 panic。
			plog.Errorf("[空止盈] 网格 %d 回补挂单失败: %v", i, err)
			return
		}
	} else {
		plog.Debugf("[空止盈] 网格 %d 修改已有回补单 订单=%s", i, g.EmptyBidID)
		d, err = svc.ClientOrderID(g.EmptyBidID).DoModify(context.Background())
		// -5027「No need to modify the order」表示订单参数已与目标一致，属正常情况，视为已挂好。
		if err != nil {
			plog.Errorf("[空止盈] 网格 %d 止盈挂单失败: %v", i, err)
			return
		}
	}

	if d == nil {
		plog.Warnf("[空止盈] 网格 %d 挂单结果为空, 跳过成功日志", i)
		return
	}
	plog.Infof("[空止盈] 网格 %d 止盈挂单成功 订单=%s 价格=%s 数量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)
}

// EmptyHangTheOrder 开空：在 AskPrice 挂 SELL LIMIT 卖单建仓（价格更高处开空）。
func EmptyHangTheOrder(i int64, price, qty string) {
	id := exchange.ToString(i) + exchange.GenID()
	plog.Debugf("[空单] 网格 %d 准备挂开空卖单 价格=%s 数量=%q 客户端订单号=%s", i, price, qty, id)

	svc := wa.NewOrder().
		Symbol(symbol).
		Side("SELL").
		PositionSide("SHORT").
		// TimeInForce("GTX").
		Type("LIMIT").
		Price(price).
		NewClientOrderID(id)
	if qty == "" {
		plog.Debugf("[空单] 网格 %d 未提供数量, 按名义价值 %dU 下单", i, usdt)
		svc.Usdt(exchange.ToString(usdt))
	} else {
		svc.Quantity(qty)
	}

	if ud != nil {
		ud.Subscribe(id, func(status string, o exchange.WsOrderTradeUpdate) {
			plog.Debugf("[空单] 网格 %d 开空卖单事件 订单=%s 状态=%s 本次成交=%s 累计成交=%s",
				i, o.ClientOrderID, status, o.LastFilledQty, o.AccumulatedFilledQty)
			if status == "FILLED" {
				// 开空卖单完全成交：交易所已无该挂单。清空卖单号、以 empty_filled_qty 记录持仓，
				// 避免「数据库有卖单号、交易所却无此单」的幽灵挂单把该格当成「仍在挂单」而永久跳过。
				// 止盈由 EmptyHangingProfit 负责挂出，待回补成交后再清空本格重新挂开空单。
				plog.Infof("[空成交] 网格 %d 开空卖单完全成交 均价=%s 数量=%s",
					i, o.AveragePrice, o.AccumulatedFilledQty)
				RecordFill(FillShortIn, o.AccumulatedFilledQty, o.RealizedPnL)
				grids.Update(i, setEmptyFilledQty(o.AccumulatedFilledQty), setEmptyAskID(""))
				plog.Debugf("[空单] 网格 %d 开空成交, 窗口上移中心至 %d", i, i-1)
				ud.Unsubscribe(o.ClientOrderID)
				EmptyIntervalVariation(i - 1)
			}
			if status == "PARTIALLY_FILLED" || status == "FILLED" {
				EmptyHangingProfit(i, o.AccumulatedFilledQty)
			}
		})
	} else {
		plog.Warnf("[空单] 网格 %d 用户数据流未就绪, 跳过成交订阅", i)
	}

	// 记录开空卖单号：网格已存在则只更新 EmptyAskID，避免重复下单时覆盖其他字段。
	if e := grids.Update(i, setEmptyAskID(id)); e != nil {
		if ud != nil {
			ud.Unsubscribe(id)
		}
		plog.Errorf("[空单] 网格 %d 记录开空卖单号失败: %v", i, e)
		return
	}

	d, err := svc.DoPlace(context.Background())
	if err != nil {
		if ud != nil {
			ud.Unsubscribe(id)
		}
		// 下单失败回滚卖单号，保持记录与实际一致
		plog.Debugf("[空单] 网格 %d 下单失败, 回滚已写入的开空卖单号", i)
		grids.Update(i, setEmptyAskID(""))
		plog.Errorf("[空单] 网格 %d 开空下单失败: %v", i, err)
		return
	}
	plog.Debugf("[空单] 网格 %d 交易所已接受开空卖单 订单号=%s 成交价=%s 委托量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)
	plog.Infof("[空单] 网格 %d 开空卖单已挂出 订单=%s 卖价=%s 数量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)
}

// EmptyMarketEnter 开空：市价 SELL 建仓（头单），随后挂回补止盈。
func EmptyMarketEnter(i int64, price, qty string) {
	id := exchange.ToString(i) + exchange.GenID()
	plog.Debugf("[空单] 网格 %d 准备市价开空 数量=%q 客户端订单号=%s", i, qty, id)

	svc := wa.NewOrder().
		Symbol(symbol).
		Side("SELL").
		PositionSide("SHORT").
		Type("MARKET").
		Price(price).
		NewClientOrderID(id)
	if qty == "" {
		plog.Debugf("[空单] 网格 %d 未提供数量, 按名义价值 %dU 下单", i, usdt)
		svc.Usdt(exchange.ToString(usdt))
	} else {
		svc.Quantity(qty)
	}

	// 记录开空卖单号（网格已存在则只更新），失败即返回，避免出现「交易所已下单、内存无记录」。
	if e := grids.Update(i, setEmptyAskID(id)); e != nil {
		plog.Errorf("[空单] 网格 %d 记录开空卖单号失败: %v", i, e)
		return
	}

	d, err := svc.DoPlace(context.Background())
	if err != nil {
		// 下单失败回滚卖单号，保持记录与实际一致
		plog.Debugf("[空单] 网格 %d 下单失败, 回滚已写入的开空卖单号", i)
		grids.Update(i, setEmptyAskID(""))
		plog.Errorf("[空单] 网格 %d 开空下单失败: %v", i, err)
		return
	}
	emptyHeadOrder = true
	emptyHeadOrderID = i
	// 市价单即时成交，交易所不会再保留该挂单：以 empty_filled_qty 记录持仓并清空 empty_ask_id，
	// 避免「记录有卖单号、交易所却无此单」的幽灵挂单把该格当成「仍在挂单」而永久跳过。
	grids.Update(i, setEmptyFilledQty(d.OrigQty), setEmptyAskID(""))
	plog.Infof("[空头单] 网格 %d 头单卖出建仓 订单=%s 委托量=%s, 后续中心及上方改用限价开空", i, d.ClientOrderID, d.OrigQty)
	// 市价头单即时成交，此处补记一笔成交统计（无实现盈亏）
	RecordFill(FillShortIn, d.OrigQty, "")
	// 头单成交价≈本格买一，止盈价由 emptyHeadExitPrice 下移一格挂出，保证有利润且不会被 POST_ONLY 拒单
	EmptyHangingProfit(i, d.OrigQty)

	// 开空后价格上行，窗口上移（编号减小），避免死锁故异步
	go EmptyIntervalVariation(i - 1)

	plog.Debugf("[空单] 网格 %d 交易所已接受开空卖单 订单号=%s 成交价=%s 委托量=%s", i, d.ClientOrderID, d.Price, d.OrigQty)
}
