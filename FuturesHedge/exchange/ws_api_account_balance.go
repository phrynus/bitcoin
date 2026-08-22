package exchange

import (
	"context"
)

// WsBalance 合约账户余额。
type WsBalance struct {
	AccountAlias       string `json:"accountAlias"`       // 唯一账户标识
	Asset              string `json:"asset"`              // 资产名称
	Balance            string `json:"balance"`            // 钱包余额
	CrossWalletBalance string `json:"crossWalletBalance"` // 全仓钱包余额
	CrossUnPnl         string `json:"crossUnPnl"`         // 全仓持仓未实现盈亏
	AvailableBalance   string `json:"availableBalance"`   // 可用余额
	MaxWithdrawAmount  string `json:"maxWithdrawAmount"`  // 最大可转出金额
	MarginAvailable    bool   `json:"marginAvailable"`    // 该资产是否可在多资产模式下作为保证金
	UpdateTime         int64  `json:"updateTime"`         // 最后更新时间
}

// Balance 查询账户余额V2(全部资产)。
func (w *WsApi) Balance(ctx context.Context) ([]*WsBalance, error) {
	return Call[[]*WsBalance](w, ctx, "account.balance", nil, true)
}
