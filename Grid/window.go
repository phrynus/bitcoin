package main

import (
	"errors"
	"main/exchange"
	"sync"

	"github.com/phrynus/plog"
)

// 窗口模块：负责网格窗口的补全、查询与移动，并按窗口位置分派挂单。
// 窗口状态（持有网格记录、头单标记）与串行化锁都集中在此文件维护。

var (
	// intervalMu 串行化区间变化：保证「比对 → 撤单 → 挂单」整段逻辑不被打断，
	// 避免多个成交回调并发触发时订单增删交叉执行。
	intervalMu sync.Mutex

	// orderRecord 记录本轮窗口的网格编号，用于下次窗口变化时比对出
	// 「已不在窗口」的多余网格并撤销其挂单。
	orderRecord []int64
	headOrder   bool  // 头单是否存在
	headOrderID int64 // 头单所在网格编号
)

// 区间变化：窗口移动后按「先比对 → 再撤单 → 后挂单」的顺序处理，保证订单增删不出错。
func IntervalVariation(i int64) {
	// 串行化，避免成交回调并发触发时比对/挂单交叉执行
	intervalMu.Lock()
	defer intervalMu.Unlock()

	plog.Debugf("[窗口] ===== 窗口变化开始 新中心网格=%d =====", i)

	gs := CompleteGrid(i)

	// 当前窗口的网格编号
	cur := make([]int64, 0, len(gs))
	exist := make(map[int64]bool, len(gs))
	for _, g := range gs {
		cur = append(cur, g.ID)
		exist[g.ID] = true
	}
	plog.Debugf("[窗口] 当前窗口网格=%v 上一轮持有=%v", cur, orderRecord)

	// 1) 比对：上一轮持有、但已不在当前窗口的网格 → 撤销其「未成交的挂单」
	for _, id := range orderRecord {
		if exist[id] {
			continue
		}

		g, ok := grids.Get(id)
		if !ok {
			plog.Errorf("[窗口] 查询移出窗口的网格 %d 失败: 网格不存在", id)
			continue
		}

		plog.Debugf("[窗口] 网格 %d 已移出窗口, 需撤销其挂单", id)
		if g.FilledQty != "" {
			// 已成交（有持仓）：止盈单是该持仓的出场单，撤掉会让持仓裸奔，必须保留。
			// 该格仍由止盈成交回调负责收尾（成交后清空并回退窗口）。
			// plog.Debugf("[窗口] 网格 %d 已持仓(成交量=%s), 保留止盈单 %q", id, g.FilledQty, g.AskID)
			// continue
		}
		// 未成交：撤掉等待成交的买单
		CancelGridOrders(g, i < g.ID)
	}

	// 2) 挂单：窗口内尚无买单/止盈的格子才挂（以数据库为准，避免重复挂单）
	for _, g := range gs {
		PlaceGridOrder(i, g)
	}

	// 3) 更新持有记录为本轮窗口的 6 个网格
	orderRecord = cur
	plog.Infof("[窗口] ===== 窗口变化完成 头部ID=%d =====", headOrderID)
	plog.Infof("[窗口] ===== 窗口变化完成 持有网格=%v =====", orderRecord)
}

// 按窗口位置挂单。判定以数据库状态为准，且必须区分「买单还在挂」与「买单已成交（有持仓）」：
// 买单一旦完全成交，BidID 会被清空、并以 FilledQty 作为持仓标记（见 HangTheOrder/MarketEnter），
// 因此 FilledQty 非空即视为持仓；BidID 非空（且 FilledQty 为空）才是真正的活跃买单。
//  1. 已有止盈单：持仓已有出场单，跳过。
//  2. 有持仓但无出场单：补挂止盈（此时不能再被 BidID 挡住）。
//  3. 无持仓但有买单号：买单仍挂在交易所，等成交即可。
//  4. 无持仓无挂单：中心及下方（价格不高于市价）挂限价买单；
//     上方格子（编号小于中心）买价高于市价，限价单会立即成交，空仓时不挂单。
//
// 注意：不要在「上方格子」上用市价单建仓——那是按市价买入却只赚一个格子的差价，必亏。
func PlaceGridOrder(i int64, g Grid) {
	if g.AskID != "" {
		plog.Debugf("[买单] 网格 %d 已有止盈单(卖=%s), 跳过挂单", g.ID, g.AskID)
		return
	}
	if g.FilledQty != "" {
		plog.Debugf("[止盈] 网格 %d 有持仓(成交量=%s)但无出场单, 补挂止盈", g.ID, g.FilledQty)
		EnsureHangingProfit(g.ID)
		return
	}
	if g.BidID != "" {
		plog.Debugf("[买单] 网格 %d 已有挂单(买=%s), 跳过挂单", g.ID, g.BidID)
		return
	}
	if g.ID >= i {
		if headOrder {
			plog.Debugf("[买单] 网格 %d 位于中心及下方(编号>=%d), 准备挂买单 价格=%s", g.ID, i, g.BidPrice)
			HangTheOrder(g.ID, g.BidPrice, g.Qty)
			return
		}
		plog.Debugf("[买单] 网格 %d 位于中心及下方(编号>=%d), 头单不存在, 改用市价单进场", g.ID, i)
		MarketEnter(g.ID, g.BidPrice, g.Qty)
		return
	}
	plog.Debugf("[买单] 网格 %d 位于市价上方(编号<%d), 空仓不挂单", g.ID, i)
}

// orderGone 判断撤单错误是否表示订单已不存在/已成交：属正常情况，无需告警。
func orderGone(e error) bool {
	var werr *exchange.WsError
	if errors.As(e, &werr) {
		return werr.Code == -2011 || werr.Code == -2013
	}
	return false
}
