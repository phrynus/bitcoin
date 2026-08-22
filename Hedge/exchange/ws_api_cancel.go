package exchange

import (
	"context"
	"errors"
)

//

//
//	{"symbol":"BTCUSDT","orderId":283194212}
//	{"symbol":"BTCUSDT","origClientOrderId":"myOrder1"}
//

func (w *WsApi) CancelOrder(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return call[*WsOrder](w, ctx, "order.cancel", params, true)
}

//

//

//

//
//	order, err := wsApi.NewCancel().
//		Symbol("BTCUSDT").OrderID(283194212).
//		Do(ctx)
//

//
//	order, err := wsApi.NewCancel().
//		Symbol("BTCUSDT").ClientOrderID("myOrder1").
//		Do(ctx)
//

type CancelService struct {
	w *WsApi

	symbol        string
	orderID       *int64
	clientOrderID string
	recvWindow    int64
}

func (w *WsApi) NewCancel() *CancelService {
	return &CancelService{w: w}
}

func (s *CancelService) Symbol(symbol string) *CancelService {
	s.symbol = symbol
	return s
}

func (s *CancelService) OrderID(orderID int64) *CancelService {
	s.orderID = &orderID
	return s
}

func (s *CancelService) ClientOrderID(id string) *CancelService {
	s.clientOrderID = id
	return s
}

func (s *CancelService) RecvWindow(ms int64) *CancelService {
	s.recvWindow = ms
	return s
}

func (s *CancelService) Do(ctx context.Context) (*WsOrder, error) {
	params, err := s.Build()
	if err != nil {
		return nil, err
	}
	return s.w.CancelOrder(ctx, params)
}

//

func (s *CancelService) Build() (map[string]any, error) {
	if s.symbol == "" {
		return nil, errors.New("缺少 symbol, 请用 Symbol()")
	}
	if s.orderID == nil && s.clientOrderID == "" {
		return nil, errors.New("撤销订单必须指定订单, 请用 OrderID() 或 ClientOrderID()")
	}

	p := make(map[string]any, 3)
	p["symbol"] = s.symbol
	if s.orderID != nil {
		p["orderId"] = *s.orderID
	}
	if s.clientOrderID != "" {
		p["origClientOrderId"] = s.clientOrderID
	}
	if s.recvWindow > 0 {
		p["recvWindow"] = s.recvWindow
	}
	return p, nil
}
