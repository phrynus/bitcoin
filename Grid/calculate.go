package main

import (
	"main/exchange"

	"github.com/phrynus/plog"
)

// 计算格子
func CalculateGrid(i int64, price string) {
	plog.Debugf("[网格] 计算中心网格 编号=%d 基准价=%s", i, price)

	bidPrice := exchange.ToFloat64(price)
	space := bidPrice * (spacing / 10000)
	bidPrice = bidPrice - space/2
	askPrice := bidPrice + space + miniChange
	s, ok := exchange.Exc.GetSymbol(symbol)
	if !ok {
		plog.Warnf("[网格] 交易对 %s 不在交易对列表中, 将按原值使用", symbol)
	}
	plog.Debugf("[网格] 原始买价=%s 间距=%g 最小变动=%g 原始止盈价=%s",
		exchange.ToString(bidPrice), space, miniChange, exchange.ToString(askPrice))

	bfp, fq, err := exchange.FixOrderText(s, exchange.ToString(bidPrice), exchange.ToString(usdt/bidPrice))
	if err != nil {
		plog.Errorf("[网格] 修正中心网格 %d 价格/数量失败: %v", i, err)
		return
	}
	afp, err := exchange.FixPriceText(s, exchange.ToString(askPrice))
	if err != nil {
		plog.Errorf("[网格] 修正中心网格 %d 止盈价失败: %v", i, err)
		return
	}

	err = grids.Create(Grid{
		ID:       i,
		BidPrice: bfp,
		AskPrice: afp,
		Qty:      fq,
		Space:    exchange.ToString(space),
	})
	if err != nil {
		plog.Errorf("[网格] 创建中心网格 %d 失败: %v", i, err)
		return
	}

	plog.Infof("[网格] 中心网格 %d 成型 买价=%s 止盈=%s 数量=%s 间距=%s",
		i, bfp, afp, fq, exchange.ToString(space))
}

// 计算上一格子
func CalculateUpGrid(i int64) {
	plog.Debugf("[网格] 计算上方网格 依据编号=%d 新编号=%d", i, i-1)

	g, ok := grids.Get(i)
	if !ok {
		plog.Errorf("[网格] 查询依据网格 %d 失败: 网格不存在", i)
		return
	}
	bidPrice := exchange.ToFloat64(g.AskPrice) + miniChange
	space := bidPrice * (spacing / 10000)
	askPrice := bidPrice + space + miniChange
	s, ok := exchange.Exc.GetSymbol(symbol)
	if !ok {
		plog.Warnf("[网格] 交易对 %s 不在交易对列表中, 将按原值使用", symbol)
	}
	plog.Debugf("[网格] 依据网格 %d 止盈价=%s 推导买价=%s 间距=%g",
		i, g.AskPrice, exchange.ToString(bidPrice), space)

	bfp, fq, err := exchange.FixOrderText(s, exchange.ToString(bidPrice), exchange.ToString(usdt/bidPrice))
	if err != nil {
		plog.Errorf("[网格] 修正上方网格 %d 价格/数量失败: %v", i-1, err)
		return
	}
	afp, err := exchange.FixPriceText(s, exchange.ToString(askPrice))
	if err != nil {
		plog.Errorf("[网格] 修正上方网格 %d 止盈价失败: %v", i-1, err)
		return
	}

	err = grids.Create(Grid{
		ID:       i - 1,
		BidPrice: bfp,
		AskPrice: afp,
		Qty:      fq,
		Space:    exchange.ToString(space),
	})
	if err != nil {
		plog.Errorf("[网格] 创建上方网格 %d 失败: %v", i-1, err)
		return
	}

	plog.Debugf("[网格] 上方网格 %d 成型 买价=%s 止盈=%s 数量=%s", i-1, bfp, afp, fq)
}

// 计算下一格子
func CalculateDownGrid(i int64) {
	plog.Debugf("[网格] 计算下方网格 依据编号=%d 新编号=%d", i, i+1)

	g, ok := grids.Get(i)
	if !ok {
		plog.Errorf("[网格] 查询依据网格 %d 失败: 网格不存在", i)
		return
	}
	askPrice := exchange.ToFloat64(g.BidPrice) - miniChange
	space := askPrice * (spacing / 10000)
	bidPrice := askPrice - space - miniChange
	s, ok := exchange.Exc.GetSymbol(symbol)
	if !ok {
		plog.Warnf("[网格] 交易对 %s 不在交易对列表中, 将按原值使用", symbol)
	}
	plog.Debugf("[网格] 依据网格 %d 买价=%s 推导止盈价=%s 买价=%s 间距=%g",
		i, g.BidPrice, exchange.ToString(askPrice), exchange.ToString(bidPrice), space)

	bfp, fq, err := exchange.FixOrderText(s, exchange.ToString(bidPrice), exchange.ToString(usdt/bidPrice))
	if err != nil {
		plog.Errorf("[网格] 修正下方网格 %d 价格/数量失败: %v", i+1, err)
		return
	}
	afp, err := exchange.FixPriceText(s, exchange.ToString(askPrice))
	if err != nil {
		plog.Errorf("[网格] 修正下方网格 %d 止盈价失败: %v", i+1, err)
		return
	}

	err = grids.Create(Grid{
		ID:       i + 1,
		BidPrice: bfp,
		AskPrice: afp,
		Qty:      fq,
		Space:    exchange.ToString(space),
	})
	if err != nil {
		plog.Errorf("[网格] 创建下方网格 %d 失败: %v", i+1, err)
		return
	}

	plog.Debugf("[网格] 下方网格 %d 成型 买价=%s 止盈=%s 数量=%s", i+1, bfp, afp, fq)
}

// 补全网格
func CompleteGrid(i int64) []Grid {
	plog.Debugf("[网格] 查询窗口网格 中心=%d 期望=%d格", i, gridWindow)
	gs := QueryGrids(i)

	if int64(len(gs)) < gridWindow {
		plog.Debugf("[网格] 窗口网格不足 现有=%d格 缺口=%d格, 开始补全", len(gs), gridWindow-int64(len(gs)))

		// 已存在的编号；缺口编号更小（价格更高）属上方，更大（价格更低）属下方
		exist := make(map[int64]bool, len(gs))
		for _, g := range gs {
			exist[g.ID] = true
		}

		// 下方缺口：由靠近中心的相邻格子逐格向下计算（i+1 → i+gridDown）
		for n := int64(1); n <= gridDown+10; n++ {
			id := i + n
			if !exist[id] && exist[id-1] {
				plog.Debugf("[网格] 补全下方缺口 网格=%d (依据相邻网格=%d)", id, id-1)
				CalculateDownGrid(id - 1)
				exist[id] = true
			}
		}

		// 上方缺口：由靠近中心的相邻格子逐格向上计算（i-1 → i-gridUp）
		for n := int64(1); n <= gridUp+10; n++ {
			id := i - n
			if !exist[id] && exist[id+1] {
				plog.Debugf("[网格] 补全上方缺口 网格=%d (依据相邻网格=%d)", id, id+1)
				CalculateUpGrid(id + 1)
				exist[id] = true
			}
		}

		// 扩容后重新获取
		gs = QueryGrids(i)
	} else {
		plog.Debugf("[网格] 窗口网格已完整, 无需补全")
	}

	plog.Debugf("[网格] 窗口网格就绪 中心=%d 数量=%d", i, len(gs))
	return gs
}

// 查询网格
func QueryGrids(id int64) []Grid {
	ids := make([]int64, 0, gridWindow)
	for n := -gridUp; n <= gridDown; n++ {
		ids = append(ids, id+n)
	}

	gs := grids.List(ids)
	plog.Debugf("[网格] 查询网格 中心=%d 编号=%v 命中=%d", id, ids, len(gs))
	return gs
}
