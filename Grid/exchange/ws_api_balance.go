package exchange

import (
	"context"
)

// WsBalance 是合约账户单个资产（如 USDT）的余额信息。
type WsBalance struct {
	AccountAlias       string `json:"accountAlias"`       // 账户别名
	Asset              string `json:"asset"`              // 资产名称，如 USDT
	Balance            string `json:"balance"`            // 钱包余额
	CrossWalletBalance string `json:"crossWalletBalance"` // 全仓钱包余额
	CrossUnPnl         string `json:"crossUnPnl"`         // 全仓未实现盈亏
	AvailableBalance   string `json:"availableBalance"`   // 可用余额
	MaxWithdrawAmount  string `json:"maxWithdrawAmount"`  // 最大可提金额
	MarginAvailable    bool   `json:"marginAvailable"`    // 是否可用作保证金
	UpdateTime         int64  `json:"updateTime"`         // 更新时间（毫秒）
}

// Balance 查询合约账户各资产余额，需要 API 密钥签名。
func (w *WsApi) Balance(ctx context.Context) ([]*WsBalance, error) {
	return call[[]*WsBalance](w, ctx, "account.balance", nil, true)
}
