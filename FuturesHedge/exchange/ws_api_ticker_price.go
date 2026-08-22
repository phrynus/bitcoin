package exchange

import (
	"context"
)

// WsPriceTicker 交易对最新价格(ticker.price)。
type WsPriceTicker struct {
	Symbol string `json:"symbol"` // 交易对
	Price  string `json:"price"`  // 最新价格
	Time   int64  `json:"time"`   // 数据时间(毫秒)
}

// PriceTicker 查询指定交易对的最新价格。
func (w *WsApi) PriceTicker(ctx context.Context, symbol string) (*WsPriceTicker, error) {
	return Call[*WsPriceTicker](w, ctx, "ticker.price", map[string]any{"symbol": symbol}, false)
}

// PriceTickers 查询所有交易对的最新价格。
func (w *WsApi) PriceTickers(ctx context.Context) ([]*WsPriceTicker, error) {
	return Call[[]*WsPriceTicker](w, ctx, "ticker.price", nil, false)
}
