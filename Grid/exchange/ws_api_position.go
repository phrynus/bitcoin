package exchange

import (
	"context"
)

// WsPositionRisk 持仓风险信息V2(v2/account.position)。
// 仅返回有仓位或挂单的交易对。
type WsPositionRisk struct {
	Symbol                string `json:"symbol"`                // 交易对
	PositionSide          string `json:"positionSide"`          // 持仓方向(BOTH/LONG/SHORT)
	PositionAmt           string `json:"positionAmt"`           // 持仓数量
	EntryPrice            string `json:"entryPrice"`            // 开仓均价
	BreakEvenPrice        string `json:"breakEvenPrice"`        // 盈亏平衡价
	MarkPrice             string `json:"markPrice"`             // 标记价格
	UnRealizedProfit      string `json:"unRealizedProfit"`      // 未实现盈亏
	LiquidationPrice      string `json:"liquidationPrice"`      // 强平价格
	IsolatedMargin        string `json:"isolatedMargin"`        // 逐仓保证金
	Notional              string `json:"notional"`              // 名义价值
	MarginAsset           string `json:"marginAsset"`           // 保证金资产
	IsolatedWallet        string `json:"isolatedWallet"`        // 逐仓钱包余额
	InitialMargin         string `json:"initialMargin"`         // 起始保证金
	MaintMargin           string `json:"maintMargin"`           // 维持保证金
	PositionInitialMargin string `json:"positionInitialMargin"` // 持仓起始保证金
	OpenOrderInitialMargin string `json:"openOrderInitialMargin"` // 挂单起始保证金
	ADL                   int64  `json:"adl"`                   // 自动减仓排名
	BidNotional           string `json:"bidNotional"`           // 买单名义价值
	AskNotional           string `json:"askNotional"`           // 卖单名义价值
	UpdateTime            int64  `json:"updateTime"`            // 更新时间(毫秒)
}

// PositionRisk 查询持仓风险V2(v2/account.position, 签名接口)。
// 仅返回有仓位或挂单的交易对; symbol 传空字符串时查询全部交易对。
// 建议结合用户数据流 ACCOUNT_UPDATE 一起使用, 以满足时效性和准确性需求。
func (w *WsApi) PositionRisk(ctx context.Context, symbol string) ([]*WsPositionRisk, error) {
	var params map[string]any
	if symbol != "" {
		params = map[string]any{"symbol": symbol}
	}
	return Call[[]*WsPositionRisk](w, ctx, "v2/account.position", params, true)
}
