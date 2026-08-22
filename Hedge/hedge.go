// 对冲机器人核心：双向持仓（USDC 空 + USDT 多）管理与账户快照。
// 各策略模块（加仓 / 减仓 / 数量平衡 / 平仓）都基于本文件的 hedge 结构与 Snap() 数据工作。
package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"

	"main/exchange"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/phrynus/go-utils/plog"
)

// hedge 单个交易账户的对冲机器人实例。
type hedge struct {
	name string // 账户名

	ws     *exchange.WsApi    // WebSocket API（下单 / 盘口 / 账户查询）
	stream *exchange.UserData // 用户数据流（订单成交订阅）

	log *plog.Log

	reduceMu sync.Mutex // 保护 reducing 的并发访问
	reducing bool       // 是否正在独立减仓（期间主循环等待，避免并发下单）
}

// newHedge 创建对冲机器人实例并建立连接。
func newHedge(name, apiKey, secret, proxy string) *hedge {
	if proxy != "" {
		futures.SetWsProxyUrl(proxy)
	}

	h := &hedge{
		name: name,

		log: plog.Sub(name),

		ws:     exchange.NewWsApi(apiKey, secret, proxy),
		stream: exchange.NewUserData(newClient(apiKey, secret, proxy), proxy),
	}
	h.log.Infof("对冲机器人实例创建完成: 账户=%s 代理=%q", name, proxy)
	return h
}

// newClient 构建 binance 期货客户端（支持代理）。
func newClient(apiKey, secret, proxy string) *futures.Client {
	if proxy != "" {
		return futures.NewProxiedClient(apiKey, secret, proxy)
	}
	return futures.NewClient(apiKey, secret)
}

// Close 关闭机器人持有的所有连接。
func (h *hedge) Close() {
	h.log.Infof("关闭 WebSocket 与用户数据流连接")
	h.ws.Close()
	h.stream.Close()
}

// leg 单条腿（USDC 或 USDT 一侧）的持仓数据。
type leg struct {
	qty   float64 // 持仓数量
	value float64 // 名义价值
	pnl   float64 // 未实现盈亏
	side  string  // 持仓方向（LONG / SHORT）
}

// pos 一个币的对冲对持仓（USDC 腿 + USDT 腿）。
type pos struct {
	value float64 // 两条腿名义价值合计
	pnl   float64 // 两条腿未实现盈亏合计
	usdt  leg
	usdc  leg
}

// snap 账户快照：可用余额、保证金比率与全部持仓。
type snap struct {
	balance   float64         // 可用余额
	pnl       float64         // 未实现盈亏
	margin    float64         // 保证金比率（万分比，55 = 0.55%）
	positions map[string]*pos // 按基础币聚合的对冲对持仓
}

// Snap 拉取账户数据生成快照，供主循环判断加仓 / 减仓 / 清理。
func (h *hedge) Snap() (*snap, error) {
	acc, err := h.ws.Account(context.Background())
	if err != nil {
		h.log.Errorf("获取账户快照失败: %v", err)
		return nil, err
	}

	s := &snap{positions: make(map[string]*pos)}

	s.balance = absf(acc.AvailableBalance)
	s.pnl = tof(acc.TotalUnrealizedProfit)
	// 保证金比率 = 维持保证金 / 钱包余额 × 10000（万分比）
	if wb := absf(acc.TotalWalletBalance); wb > 0 {
		s.margin = absf(acc.TotalMaintMargin) / wb * 100
	}

	for _, pt := range acc.Positions {
		qty := absf(pt.PositionAmt)
		value := absf(pt.Notional)
		pnl := tof(pt.UnrealizedProfit)
		if qty == 0 && value == 0 {
			continue
		}

		// 以基础币为 key，把 USDC / USDT 两条腿聚合到同一个对冲对
		base := strings.TrimSuffix(strings.TrimSuffix(pt.Symbol, "USDC"), "USDT")
		p := s.positions[base]
		if p == nil {
			p = &pos{}
			s.positions[base] = p
		}
		p.value += value
		p.pnl += pnl

		if strings.HasSuffix(pt.Symbol, "USDC") {
			p.usdc.qty += qty
			p.usdc.value += value
			p.usdc.pnl += pnl
			p.usdc.side = pt.PositionSide
		} else {
			p.usdt.qty += qty
			p.usdt.value += value
			p.usdt.pnl += pnl
			p.usdt.side = pt.PositionSide
		}
	}

	h.log.Infof("快照完成: 可用余额=%.4f 未实现盈亏=%.4f 保证金率=%.2f%% 持仓对数=%d",
		s.balance, s.pnl, s.margin, len(s.positions))
	return s, nil
}

// tof 字符串转 float64（解析失败返回 0）。
func tof(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// absf 字符串转 float64 并取绝对值。
func absf(s string) float64 {
	return math.Abs(tof(s))
}

// ftos float64 转字符串（无尾零）。
func ftos(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// depthLevel 取某交易对盘口第二档价格（供挂限价单取价）。
//   - useAsk=true  取卖盘第二档 Asks[1][0]：开仓 SELL / 平仓 BUY
//   - useAsk=false 取买盘第二档 Bids[1][0]：开仓 BUY / 平仓 SELL
//
// 盘口只有一档时回退到最优档，避免越界。
func (h *hedge) depthLevel(symbol string, useAsk bool) (string, error) {
	d, err := h.ws.Depth(context.Background(), symbol, 5)
	if err != nil {
		return "", err
	}

	side := "买盘"
	var level [2]string
	if useAsk {
		side = "卖盘"
		if len(d.Asks) == 0 {
			return "", fmt.Errorf("%s 无卖盘", symbol)
		}
		level = d.Asks[0]
		if len(d.Asks) > 1 {
			level = d.Asks[1]
		}
	} else {
		if len(d.Bids) == 0 {
			return "", fmt.Errorf("%s 无买盘", symbol)
		}
		level = d.Bids[0]
		if len(d.Bids) > 1 {
			level = d.Bids[1]
		}
	}

	if level[0] == "" {
		return "", fmt.Errorf("%s 盘口%s为空", symbol, side)
	}
	return level[0], nil
}
