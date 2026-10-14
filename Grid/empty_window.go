package main

import (
	"sync"

	"github.com/phrynus/plog"
)

// 空单窗口模块：与多单窗口（window.go）完全镜像、独立维护状态与串行化锁。
// 注意方向相反：多单在买入成交（价格下探）时窗口下移（编号增大），止盈成交（价格回升）时上移；
// 空单在开空成交（价格上探）时窗口上移（编号减小），回补成交（价格回落）时下移。
// 网格定义（BidPrice/AskPrice/Qty）与多单共享同一张网格表，仅订单与状态记录相互独立。

var (
	// emptyIntervalMu 串行化空单区间变化：保证「比对 → 撤单 → 挂单」整段逻辑不被打断，
	// 避免多个成交回调并发触发时订单增删交叉执行。
	emptyIntervalMu sync.Mutex

	// emptyOrderRecord 记录本轮空单窗口的网格编号，用于下次窗口变化时比对出
	// 「已不在窗口」的多余网格并撤销其挂单。
	emptyOrderRecord []int64
	emptyHeadOrder   bool  // 空单头单是否存在
	emptyHeadOrderID int64 // 空单头单所在网格编号
)

// EmptyIntervalVariation 空单区间变化：窗口移动后按「先比对 → 再撤单 → 后挂单」的顺序处理，保证订单增删不出错。
func EmptyIntervalVariation(i int64) {
	// 串行化，避免成交回调并发触发时比对/挂单交叉执行
	emptyIntervalMu.Lock()
	defer emptyIntervalMu.Unlock()

	plog.Debugf("[空窗口] ===== 空单窗口变化开始 新中心网格=%d =====", i)

	gs := CompleteGrid(i)

	// 当前窗口的网格编号
	cur := make([]int64, 0, len(gs))
	exist := make(map[int64]bool, len(gs))
	for _, g := range gs {
		cur = append(cur, g.ID)
		exist[g.ID] = true
	}
	plog.Debugf("[空窗口] 当前窗口网格=%v 上一轮持有=%v", cur, emptyOrderRecord)

	// 1) 比对：上一轮持有、但已不在当前窗口的网格 → 撤销其「未成交的挂单」
	for _, id := range emptyOrderRecord {
		if exist[id] {
			continue
		}

		g, ok := grids.Get(id)
		if !ok {
			plog.Errorf("[空窗口] 查询移出窗口的网格 %d 失败: 网格不存在", id)
			continue
		}

		plog.Debugf("[空窗口] 网格 %d 已移出窗口, 需撤销其挂单", id)
		// 网格高于新中心（编号更小）说明它从上方移出 → 撤销等待成交的开空卖单；
		// 否则自下方移出 → 撤销回补买单。判据与多单窗口镜像（多单为 i < g.ID）。
		EmptyCancelGridOrders(g, g.ID < i)
	}

	// 2) 挂单：窗口内尚无挂单/止盈的格子才挂（以数据库为准，避免重复挂单）。
	// 由下往上倒序遍历，使头单落在「中心格」而非最上方格子：
	// 空单入场格在中心及上方，而 gs 按编号升序返回，升序遍历会先碰到最上方格子（与多单相反）。
	for n := len(gs) - 1; n >= 0; n-- {
		EmptyPlaceGridOrder(i, gs[n])
	}

	// 3) 更新持有记录为本轮窗口的网格
	emptyOrderRecord = cur
	plog.Infof("[空窗口] ===== 空单窗口变化完成 头部ID=%d =====", emptyHeadOrderID)
	plog.Infof("[空窗口] ===== 空单窗口变化完成 持有网格=%v =====", emptyOrderRecord)
}

// EmptyPlaceGridOrder 按窗口位置挂空单。判定以数据库状态为准，且必须区分「开空卖单还在挂」与「已开空（有持仓）」：
// 开空卖单一旦完全成交，EmptyAskID 会被清空、并以 EmptyFilledQty 作为持仓标记（见 EmptyHangTheOrder/EmptyMarketEnter），
// 因此 EmptyFilledQty 非空即视为持仓；EmptyAskID 非空（且 EmptyFilledQty 为空）才是真正的活跃开空卖单。
//  1. 已有回补单：持仓已有出场单，跳过。
//  2. 有持仓但无出场单：补挂回补（此时不能再被 EmptyAskID 挡住）。
//  3. 无持仓但有开空卖单号：开空卖单仍挂在交易所，等成交即可。
//  4. 无持仓无挂单：中心及上方（价格不低于市价、编号不大于中心）挂限价开空卖单；
//     下方格子（编号大于中心）卖价低于市价，限价单会立即成交，空仓时不挂单。
//
// 注意：不要在「下方格子」上用市价单开空——那是按市价卖出却只赚一个格子的差价，必亏。
func EmptyPlaceGridOrder(i int64, g Grid) {
	if g.EmptyBidID != "" {
		plog.Debugf("[空单] 网格 %d 已有回补单(买=%s), 跳过挂单", g.ID, g.EmptyBidID)
		return
	}
	if g.EmptyFilledQty != "" {
		plog.Debugf("[空止盈] 网格 %d 有持仓(成交量=%s)但无出场单, 补挂止盈", g.ID, g.EmptyFilledQty)
		EmptyEnsureHangingProfit(g.ID)
		return
	}
	if g.EmptyAskID != "" {
		plog.Debugf("[空单] 网格 %d 已有开空卖单(卖=%s), 跳过挂单", g.ID, g.EmptyAskID)
		return
	}
	if g.ID <= i {
		if emptyHeadOrder {
			plog.Debugf("[空单] 网格 %d 位于中心及上方(编号<=%d), 准备挂开空卖单 价格=%s", g.ID, i, g.AskPrice)
			EmptyHangTheOrder(g.ID, g.AskPrice, g.Qty)
			return
		}
		plog.Debugf("[空单] 网格 %d 位于中心及上方(编号<=%d), 头单不存在, 改用市价单进场", g.ID, i)
		EmptyMarketEnter(g.ID, g.AskPrice, g.Qty)
		return
	}
	plog.Debugf("[空单] 网格 %d 位于市价下方(编号>%d), 空仓不挂单", g.ID, i)
}
