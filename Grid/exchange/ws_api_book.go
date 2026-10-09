package exchange

import (
	"context"
)

// WsBookTicker 为交易对的最优挂单行情（买一/卖一及其数量）。
type WsBookTicker struct {
	Symbol   string `json:"symbol"`   // 交易对
	BidPrice string `json:"bidPrice"` // 买一价
	BidQty   string `json:"bidQty"`   // 买一量
	AskPrice string `json:"askPrice"` // 卖一价
	AskQty   string `json:"askQty"`   // 卖一量
	Time     int64  `json:"time"`     // 时间戳（毫秒）
}

// Book 查询单个交易对的最优挂单行情；symbol 为空时币安会返回错误。
func (w *WsApi) Book(ctx context.Context, symbol string) (*WsBookTicker, error) {
	return call[*WsBookTicker](w, ctx, "ticker.book", map[string]any{"symbol": symbol}, false)
}

// Books 查询全部交易对的最优挂单行情。
func (w *WsApi) Books(ctx context.Context) ([]*WsBookTicker, error) {
	return call[[]*WsBookTicker](w, ctx, "ticker.book", nil, false)
}
