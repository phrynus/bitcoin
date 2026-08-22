package exchange

import (
	"context"
)

type WsBookTicker struct {
	Symbol   string `json:"symbol"`
	BidPrice string `json:"bidPrice"`
	BidQty   string `json:"bidQty"`
	AskPrice string `json:"askPrice"`
	AskQty   string `json:"askQty"`
	Time     int64  `json:"time"`
}

func (w *WsApi) Book(ctx context.Context, symbol string) (*WsBookTicker, error) {
	return call[*WsBookTicker](w, ctx, "ticker.book", map[string]any{"symbol": symbol}, false)
}

func (w *WsApi) Books(ctx context.Context) ([]*WsBookTicker, error) {
	return call[[]*WsBookTicker](w, ctx, "ticker.book", nil, false)
}
