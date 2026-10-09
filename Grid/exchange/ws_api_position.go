package exchange

import (
	"context"
)

// WsPositionRisk 为单个合约持仓的风险与保证金信息。
type WsPositionRisk struct {
	Symbol                 string `json:"symbol"`                 // 交易对
	PositionSide           string `json:"positionSide"`           // 持仓方向（BOTH/LONG/SHORT）
	PositionAmt            string `json:"positionAmt"`            // 持仓数量，带符号
	EntryPrice             string `json:"entryPrice"`             // 开仓均价
	BreakEvenPrice         string `json:"breakEvenPrice"`         // 盈亏平衡价
	MarkPrice              string `json:"markPrice"`              // 标记价
	UnRealizedProfit       string `json:"unRealizedProfit"`       // 未实现盈亏
	LiquidationPrice       string `json:"liquidationPrice"`       // 预估强平价
	IsolatedMargin         string `json:"isolatedMargin"`         // 逐仓保证金
	Notional               string `json:"notional"`               // 名义价值
	MarginAsset            string `json:"marginAsset"`            // 保证金资产
	IsolatedWallet         string `json:"isolatedWallet"`         // 逐仓钱包余额
	InitialMargin          string `json:"initialMargin"`          // 初始保证金
	MaintMargin            string `json:"maintMargin"`            // 维持保证金
	PositionInitialMargin  string `json:"positionInitialMargin"`  // 持仓初始保证金
	OpenOrderInitialMargin string `json:"openOrderInitialMargin"` // 挂单初始保证金
	ADL                    int64  `json:"adl"`                    // 自动减仓等级（1-4）
	BidNotional            string `json:"bidNotional"`            // 买单名义价值
	AskNotional            string `json:"askNotional"`            // 卖单名义价值
	UpdateTime             int64  `json:"updateTime"`             // 更新时间（毫秒）
}

// Position 查询持仓风险信息；symbol 为空时返回全部持仓，需要 API 密钥签名。
func (w *WsApi) Position(ctx context.Context, symbol string) ([]*WsPositionRisk, error) {
	var params map[string]any
	if symbol != "" {
		params = map[string]any{"symbol": symbol}
	}
	return call[[]*WsPositionRisk](w, ctx, "v2/account.position", params, true)
}
