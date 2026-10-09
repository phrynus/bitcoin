package exchange

import (
	"context"
)

// WsAccountInfo 为合约账户的整体资产与持仓概况。
type WsAccountInfo struct {
	TotalInitialMargin          string               `json:"totalInitialMargin"`          // 全仓初始保证金
	TotalMaintMargin            string               `json:"totalMaintMargin"`            // 全仓维持保证金
	TotalWalletBalance          string               `json:"totalWalletBalance"`          // 钱包总余额
	TotalUnrealizedProfit       string               `json:"totalUnrealizedProfit"`       // 未实现总盈亏
	TotalMarginBalance          string               `json:"totalMarginBalance"`          // 保证金总余额
	TotalPositionInitialMargin  string               `json:"totalPositionInitialMargin"`  // 持仓初始保证金
	TotalOpenOrderInitialMargin string               `json:"totalOpenOrderInitialMargin"` // 挂单初始保证金
	TotalCrossWalletBalance     string               `json:"totalCrossWalletBalance"`     // 全仓钱包余额
	TotalCrossUnPnl             string               `json:"totalCrossUnPnl"`             // 全仓未实现盈亏
	AvailableBalance            string               `json:"availableBalance"`            // 可用余额
	MaxWithdrawAmount           string               `json:"maxWithdrawAmount"`           // 最大可提金额
	Assets                      []*WsAccountAsset    `json:"assets"`                      // 各资产余额
	Positions                   []*WsAccountPosition `json:"positions"`                   // 各持仓概况
}

// WsAccountAsset 为账户中单个资产的余额与保证金信息。
type WsAccountAsset struct {
	Asset                  string `json:"asset"`                  // 资产名称
	WalletBalance          string `json:"walletBalance"`          // 钱包余额
	UnrealizedProfit       string `json:"unrealizedProfit"`       // 未实现盈亏
	MarginBalance          string `json:"marginBalance"`          // 保证金余额
	MaintMargin            string `json:"maintMargin"`            // 维持保证金
	InitialMargin          string `json:"initialMargin"`          // 初始保证金
	PositionInitialMargin  string `json:"positionInitialMargin"`  // 持仓初始保证金
	OpenOrderInitialMargin string `json:"openOrderInitialMargin"` // 挂单初始保证金
	CrossWalletBalance     string `json:"crossWalletBalance"`     // 全仓钱包余额
	CrossUnPnl             string `json:"crossUnPnl"`             // 全仓未实现盈亏
	AvailableBalance       string `json:"availableBalance"`       // 可用余额
	MaxWithdrawAmount      string `json:"maxWithdrawAmount"`      // 最大可提金额
	MarginAvailable        bool   `json:"marginAvailable"`        // 是否可用作保证金
	UpdateTime             int64  `json:"updateTime"`             // 更新时间（毫秒）
}

// WsAccountPosition 为账户持仓概况中的一个持仓。
type WsAccountPosition struct {
	Symbol           string `json:"symbol"`           // 交易对
	PositionSide     string `json:"positionSide"`     // 持仓方向（BOTH/LONG/SHORT）
	PositionAmt      string `json:"positionAmt"`      // 持仓数量，带符号
	UnrealizedProfit string `json:"unrealizedProfit"` // 未实现盈亏
	IsolatedMargin   string `json:"isolatedMargin"`   // 逐仓保证金
	Notional         string `json:"notional"`         // 名义价值
	IsolatedWallet   string `json:"isolatedWallet"`   // 逐仓钱包余额
	InitialMargin    string `json:"initialMargin"`    // 初始保证金
	MaintMargin      string `json:"maintMargin"`      // 维持保证金
	UpdateTime       int64  `json:"updateTime"`       // 更新时间（毫秒）
}

// Account 查询合约账户整体资产与持仓概况，需要 API 密钥签名。
func (w *WsApi) Account(ctx context.Context) (*WsAccountInfo, error) {
	return call[*WsAccountInfo](w, ctx, "v2/account.status", nil, true)
}
