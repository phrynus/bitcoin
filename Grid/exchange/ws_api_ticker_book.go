package exchange

import (
	"context"
)

// WsBookTicker 交易对最优挂单(ticker.book)。
// 注意: 零售价格改进(RPI)订单在响应消息中不可见且被排除在外。
type WsBookTicker struct {
	Symbol   string `json:"symbol"`   // 交易对
	BidPrice string `json:"bidPrice"` // 最优买价
	BidQty   string `json:"bidQty"`   // 最优买价数量
	AskPrice string `json:"askPrice"` // 最优卖价
	AskQty   string `json:"askQty"`   // 最优卖价数量
	Time     int64  `json:"time"`     // 数据时间(毫秒)
}

// BookTicker 查询指定交易对的最优挂单。
func (w *WsApi) BookTicker(ctx context.Context, symbol string) (*WsBookTicker, error) {
	return Call[*WsBookTicker](w, ctx, "ticker.book", map[string]any{"symbol": symbol}, false)
}

// BookTickers 查询所有交易对的最优挂单。
func (w *WsApi) BookTickers(ctx context.Context) ([]*WsBookTicker, error) {
	return Call[[]*WsBookTicker](w, ctx, "ticker.book", nil, false)
}
