package exchange

import (
	"context"
)

type WsPriceTicker struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
	Time   int64  `json:"time"`
}

func (w *WsApi) Price(ctx context.Context, symbol string) (*WsPriceTicker, error) {
	return call[*WsPriceTicker](w, ctx, "ticker.price", map[string]any{"symbol": symbol}, false)
}

func (w *WsApi) Prices(ctx context.Context) ([]*WsPriceTicker, error) {
	return call[[]*WsPriceTicker](w, ctx, "ticker.price", nil, false)
}
