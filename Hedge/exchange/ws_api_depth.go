package exchange

import (
	"context"
)

type WsDepth struct {
	LastUpdateID int64       `json:"lastUpdateId"`
	E            int64       `json:"E"`
	T            int64       `json:"T"`
	Bids         [][2]string `json:"bids"`
	Asks         [][2]string `json:"asks"`
}

func (w *WsApi) Depth(ctx context.Context, symbol string, limit int) (*WsDepth, error) {
	params := map[string]any{"symbol": symbol}
	if limit > 0 {
		params["limit"] = limit
	}
	return call[*WsDepth](w, ctx, "depth", params, false)
}
