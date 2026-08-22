package exchange

import (
	"context"
)

// WsDepth 订单簿深度信息(depth)。
// 注意: 零点价订单在响应消息中不可见且被排除在外。
type WsDepth struct {
	LastUpdateID int64       `json:"lastUpdateId"` // 订单簿最后更新 ID
	E            int64       `json:"E"`            // 消息输出时间(毫秒)
	T            int64       `json:"T"`            // 成交时间(毫秒)
	Bids         [][2]string `json:"bids"`         // 买单列表, 每项为 [价格, 数量]
	Asks         [][2]string `json:"asks"`         // 卖单列表, 每项为 [价格, 数量]
}

// Depth 查询订单簿深度信息。
// limit 有效值: [5, 10, 20, 50, 100, 500, 1000], 传 0 时使用交易所默认值(500)。
// 如需持续监控订单簿更新, 请配合 <symbol>@depth 或 <symbol>@depth<levels> 市场流维护本地订单簿。
func (w *WsApi) Depth(ctx context.Context, symbol string, limit int) (*WsDepth, error) {
	params := map[string]any{"symbol": symbol}
	if limit > 0 {
		params["limit"] = limit
	}
	return Call[*WsDepth](w, ctx, "depth", params, false)
}
