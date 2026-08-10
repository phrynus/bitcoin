package exchange

import (
	"context"
)

// WsAccountInfo 账户信息V2(v2/account.status)。
// 单资产/多资产模式下字段含义略有不同, 汇总字段仅对 USDT 资产有意义。
type WsAccountInfo struct {
	TotalInitialMargin          string               `json:"totalInitialMargin"`          // 当前标记价格下所需总起始保证金(逐仓持仓下无意义, 仅 USDT)
	TotalMaintMargin            string               `json:"totalMaintMargin"`            // 全仓持仓维持保证金总额(USD 计价)
	TotalWalletBalance          string               `json:"totalWalletBalance"`          // 账户总钱包余额(仅 USDT)
	TotalUnrealizedProfit       string               `json:"totalUnrealizedProfit"`       // 未实现盈亏总额(仅 USDT)
	TotalMarginBalance          string               `json:"totalMarginBalance"`          // 保证金余额总额(仅 USDT)
	TotalPositionInitialMargin  string               `json:"totalPositionInitialMargin"`  // 当前标记价格下持仓所需起始保证金(仅 USDT)
	TotalOpenOrderInitialMargin string               `json:"totalOpenOrderInitialMargin"` // 当前标记价格下挂单所需起始保证金(仅 USDT)
	TotalCrossWalletBalance     string               `json:"totalCrossWalletBalance"`     // 全仓钱包余额(仅 USDT)
	TotalCrossUnPnl             string               `json:"totalCrossUnPnl"`             // 全仓持仓未实现盈亏(仅 USDT)
	AvailableBalance            string               `json:"availableBalance"`            // 可用余额(仅 USDT)
	MaxWithdrawAmount           string               `json:"maxWithdrawAmount"`           // 最大可转出金额(仅 USDT)
	Assets                      []*WsAccountAsset    `json:"assets"`                      // 报价资产列表(USDT/USDC/BTC)
	Positions                   []*WsAccountPosition `json:"positions"`                   // 有持仓或有挂单的所有交易对持仓
}

// WsAccountAsset 账户资产。
type WsAccountAsset struct {
	Asset                  string `json:"asset"`                  // 资产名称
	WalletBalance          string `json:"walletBalance"`          // 钱包余额
	UnrealizedProfit       string `json:"unrealizedProfit"`       // 未实现盈亏
	MarginBalance          string `json:"marginBalance"`          // 保证金余额
	MaintMargin            string `json:"maintMargin"`            // 所需维持保证金
	InitialMargin          string `json:"initialMargin"`          // 当前标记价格下所需总起始保证金
	PositionInitialMargin  string `json:"positionInitialMargin"`  // 当前标记价格下持仓所需起始保证金
	OpenOrderInitialMargin string `json:"openOrderInitialMargin"` // 当前标记价格下挂单所需起始保证金
	CrossWalletBalance     string `json:"crossWalletBalance"`     // 全仓钱包余额
	CrossUnPnl             string `json:"crossUnPnl"`             // 全仓持仓未实现盈亏
	AvailableBalance       string `json:"availableBalance"`       // 可用余额(仅 USDT)
	MaxWithdrawAmount      string `json:"maxWithdrawAmount"`      // 最大可转出金额(仅 USDT)
	MarginAvailable        bool   `json:"marginAvailable"`        // 该资产是否可在多资产模式下作为保证金
	UpdateTime             int64  `json:"updateTime"`             // 最后更新时间
}

// WsAccountPosition 账户持仓(有持仓或有挂单的交易对)。
type WsAccountPosition struct {
	Symbol           string `json:"symbol"`           // 交易对
	PositionSide     string `json:"positionSide"`     // 持仓方向
	PositionAmt      string `json:"positionAmt"`      // 持仓数量
	UnrealizedProfit string `json:"unrealizedProfit"` // 未实现盈亏
	IsolatedMargin   string `json:"isolatedMargin"`   // 逐仓保证金
	Notional         string `json:"notional"`         // 名义价值
	IsolatedWallet   string `json:"isolatedWallet"`   // 逐仓钱包余额
	InitialMargin    string `json:"initialMargin"`    // 当前标记价格下所需总起始保证金
	MaintMargin      string `json:"maintMargin"`      // 所需维持保证金
	UpdateTime       int64  `json:"updateTime"`       // 最后更新时间
}

// AccountStatus 查询账户信息V2(资产汇总 + 资产列表 + 持仓)。
func (w *WsApi) AccountStatus(ctx context.Context) (*WsAccountInfo, error) {
	return Call[*WsAccountInfo](w, ctx, "v2/account.status", nil, true)
}
