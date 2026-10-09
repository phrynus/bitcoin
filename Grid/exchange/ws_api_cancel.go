package exchange

import (
	"context"
	"errors"
)

// CancelOrder 按参数撤销订单，需要 API 密钥签名。
// 参数为币安原始字段，优先使用 NewCancel 构造以避免字段遗漏。
func (w *WsApi) CancelOrder(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return call[*WsOrder](w, ctx, "order.cancel", params, true)
}

// CancelService 以链式调用构造撤单请求。symbol 必填，且必须指定 OrderID 或 ClientOrderID 之一，
// 完成构造后调用 Do 提交。
type CancelService struct {
	w *WsApi

	symbol        string
	orderID       *int64
	clientOrderID string
	recvWindow    int64
}

// NewCancel 创建撤单请求构造器，完成后调用 Do 提交。
func (w *WsApi) NewCancel() *CancelService {
	return &CancelService{w: w}
}

// Symbol 设置要撤单的交易对，必填。
func (s *CancelService) Symbol(symbol string) *CancelService {
	s.symbol = symbol
	return s
}

// OrderID 按交易所订单号指定要撤销的订单。
func (s *CancelService) OrderID(orderID int64) *CancelService {
	s.orderID = &orderID
	return s
}

// ClientOrderID 按客户端订单号指定要撤销的订单。
func (s *CancelService) ClientOrderID(id string) *CancelService {
	s.clientOrderID = id
	return s
}

// RecvWindow 设置请求有效时间窗口（毫秒），大于 0 时才会提交给币安。
func (s *CancelService) RecvWindow(ms int64) *CancelService {
	s.recvWindow = ms
	return s
}

// Do 校验参数并提交撤单请求。
// 缺少 symbol，或未指定 OrderID 与 ClientOrderID 时返回错误。
func (s *CancelService) Do(ctx context.Context) (*WsOrder, error) {
	params, err := s.Build()
	if err != nil {
		return nil, err
	}
	return s.w.CancelOrder(ctx, params)
}

// Build 将链式设置转换为币安的请求参数。
// 缺少 symbol，或未指定 OrderID 与 ClientOrderID 时返回错误。
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
