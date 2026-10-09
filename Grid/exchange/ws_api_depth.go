package exchange

import (
	"context"
)

// WsDepth 为交易对的市场深度快照，Bids/Asks 每项为 [价格, 数量] 字符串对。
type WsDepth struct {
	LastUpdateID int64       `json:"lastUpdateId"` // 快照对应的更新 id
	E            int64       `json:"E"`            // 事件时间（毫秒）
	T            int64       `json:"T"`            // 撮合时间（毫秒）
	Bids         [][2]string `json:"bids"`         // 买单档位，每项为 [价格, 数量]，按价格降序
	Asks         [][2]string `json:"asks"`         // 卖单档位，每项为 [价格, 数量]，按价格升序
}

// Depth 查询交易对的市场深度快照；limit > 0 时限制返回的档位数。
func (w *WsApi) Depth(ctx context.Context, symbol string, limit int) (*WsDepth, error) {
	params := map[string]any{"symbol": symbol}
	if limit > 0 {
		params["limit"] = limit
	}
	return call[*WsDepth](w, ctx, "depth", params, false)
}
