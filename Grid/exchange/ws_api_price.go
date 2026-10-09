package exchange

import (
	"context"
)

// WsPriceTicker 为交易对的最新成交价。
type WsPriceTicker struct {
	Symbol string `json:"symbol"` // 交易对
	Price  string `json:"price"`  // 最新成交价
	Time   int64  `json:"time"`   // 时间戳（毫秒）
}

// Price 查询单个交易对的最新成交价。
func (w *WsApi) Price(ctx context.Context, symbol string) (*WsPriceTicker, error) {
	return call[*WsPriceTicker](w, ctx, "ticker.price", map[string]any{"symbol": symbol}, false)
}

// Prices 查询全部交易对的最新成交价。
func (w *WsApi) Prices(ctx context.Context) ([]*WsPriceTicker, error) {
	return call[[]*WsPriceTicker](w, ctx, "ticker.price", nil, false)
}
