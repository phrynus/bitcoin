package exchange

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// WsOrder 为币安订单对象，同时用于下单、改单与撤单的响应。
type WsOrder struct {
	OrderID                 int64  `json:"orderId"`                 // 订单号
	Symbol                  string `json:"symbol"`                  // 交易对
	Status                  string `json:"status"`                  // 订单状态，如 NEW、PARTIALLY_FILLED、FILLED、CANCELED
	ClientOrderID           string `json:"clientOrderId"`           // 客户端订单号
	ModifyID                int64  `json:"modifyId"`                // 改单标识
	Price                   string `json:"price"`                   // 委托价格
	OrigQty                 string `json:"origQty"`                 // 原始委托数量
	ExecutedQty             string `json:"executedQty"`             // 已成交数量
	CumQty                  string `json:"cumQty"`                  // 累计成交数量
	TimeInForce             string `json:"timeInForce"`             // 有效期策略
	Type                    string `json:"type"`                    // 订单类型
	ReduceOnly              bool   `json:"reduceOnly"`              // 是否只减仓
	ClosePosition           bool   `json:"closePosition"`           // 是否全平仓
	Side                    string `json:"side"`                    // 买卖方向
	PositionSide            string `json:"positionSide"`            // 持仓方向
	StopPrice               string `json:"stopPrice"`               // 触发价
	WorkingType             string `json:"workingType"`             // 触发价类型（MARK_PRICE/CONTRACT_PRICE）
	PriceProtect            bool   `json:"priceProtect"`            // 是否启用触发价保护
	ActivatePrice           string `json:"activatePrice"`           // 跟踪委托激活价
	PriceRate               string `json:"priceRate"`               // 跟踪委托回调比例
	OrigType                string `json:"origType"`                // 原始订单类型
	PriceMatch              string `json:"priceMatch"`              // 价格匹配模式
	SelfTradePreventionMode string `json:"selfTradePreventionMode"` // 自成交预防模式
	GoodTillDate            int64  `json:"goodTillDate"`            // GTD 失效时间戳（毫秒）
	UpdateTime              int64  `json:"updateTime"`              // 更新时间（毫秒）
}

// PlaceOrder 按币安原始参数下单，需要签名。优先使用 NewOrder 构造参数。
func (w *WsApi) PlaceOrder(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return call[*WsOrder](w, ctx, "order.place", params, true)
}

// ModifyOrder 按币安原始参数修改未成交订单，需要签名。优先使用 NewOrder 构造参数。
func (w *WsApi) ModifyOrder(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return call[*WsOrder](w, ctx, "order.modify", params, true)
}

// OrderService 以链式调用构造下单或改单请求：设置完订单参数后调用 Do，
// 未指定 OrderID/ClientOrderID 时执行下单，否则执行改单。
// 若 Exchange 已初始化，提交前会自动按交易对规则修正价格与数量。
type OrderService struct {
	w *WsApi

	symbol           string
	side             string
	positionSide     string
	quantity         string
	usdt             string
	price            string
	priceMatch       string
	clientOrderID    string
	newClientOrderID string
	recvWindow       int64

	orderType               string
	timeInForce             string
	reduceOnly              *bool
	newOrderRespType        string
	selfTradePreventionMode string
	goodTillDate            int64

	orderID  *int64
	modifyID int64
}

// NewOrder 创建下单/改单请求构造器，完成后调用 Do 提交。
func (w *WsApi) NewOrder() *OrderService {
	return &OrderService{w: w}
}

func (s *OrderService) normalize() error {

	if s.priceMatch == "NONE" {
		s.priceMatch = ""
	}

	if s.price != "" && s.priceMatch != "" {
		return errors.New("Price() 与 PriceMatch() 只能二选一")
	}

	if s.positionSide == "" {
		s.positionSide = "BOTH"
	}

	if exc == nil {
		return nil
	}
	si, ok := exc.getSymbol(s.symbol)
	if !ok {
		return fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中", s.symbol)
	}

	if s.usdt != "" {
		return s.fixUsdt(si)
	}

	if s.price != "" && s.quantity != "" {
		fp, fq, err := fixOrderText(si, s.price, s.quantity)
		if err != nil {
			return err
		}
		s.price = fp
		s.quantity = fq
		return nil
	}

	if s.quantity != "" {
		fq, err := fixQuantityText(si, s.quantity)
		if err != nil {
			return err
		}
		s.quantity = fq
	}
	return nil
}

func (s *OrderService) fixUsdt(si *SymbolInfo) error {
	if s.quantity != "" {
		return errors.New("Quantity() 与 Usdt() 只能二选一")
	}
	if s.price == "" {
		return errors.New("按 USDT 金额下单必须设置价格, 请用 Price()")
	}
	amount, err := decimal.NewFromString(s.usdt)
	if err != nil {
		return fmt.Errorf("USDT 金额解析失败 %q: %v", s.usdt, err)
	}
	price, err := decimal.NewFromString(s.price)
	if err != nil {
		return fmt.Errorf("价格解析失败 %q: %v", s.price, err)
	}
	if !price.IsPositive() {
		return fmt.Errorf("价格 %s 无效, 必须大于 0", s.price)
	}

	if si.MinNotionalFilter != nil && si.MinNotionalFilter.Notional != "" {
		if notional := decOrZero(si.MinNotionalFilter.Notional); amount.LessThan(notional) {
			return fmt.Errorf("USDT 金额 %s 小于最小名义价值 %s", s.usdt, si.MinNotionalFilter.Notional)
		}
	}

	qty := amount.Div(price)
	fp, fq, err := fixOrderText(si, s.price, qty.String())
	if err != nil {
		return err
	}
	s.price = fp
	s.quantity = fq
	return nil
}

// Symbol 设置交易对，必填。
func (s *OrderService) Symbol(symbol string) *OrderService {
	s.symbol = symbol
	return s
}

// Side 设置买卖方向，取 "BUY" 或 "SELL"，必填。
func (s *OrderService) Side(side string) *OrderService {
	s.side = side
	return s
}

// PositionSide 设置持仓方向（BOTH/LONG/SHORT），未设置时默认为 BOTH。
func (s *OrderService) PositionSide(positionSide string) *OrderService {
	s.positionSide = positionSide
	return s
}

// Type 设置订单类型（如 LIMIT、MARKET），未设置时默认为 MARKET。
func (s *OrderService) Type(orderType string) *OrderService {
	s.orderType = orderType
	return s
}

// TimeInForce 设置有效期策略（GTC/IOC/FOK/GTD），LIMIT 订单未设置时默认为 GTC。
func (s *OrderService) TimeInForce(timeInForce string) *OrderService {
	s.timeInForce = timeInForce
	return s
}

// Quantity 设置下单数量，与 Usdt 二选一。
func (s *OrderService) Quantity(quantity string) *OrderService {
	s.quantity = quantity
	return s
}

// Usdt 按名义价值（USDT 金额）下单，与 Quantity 二选一，且必须同时设置 Price。
// 金额会除以价格换算为数量，并按交易对规则修正。
func (s *OrderService) Usdt(usdt string) *OrderService {
	s.usdt = usdt
	return s
}

// Price 设置委托价格，与 PriceMatch 二选一。
func (s *OrderService) Price(price string) *OrderService {
	s.price = price
	return s
}

// ReduceOnly 设置只减仓。仅在 PositionSide 为 BOTH 时生效。
func (s *OrderService) ReduceOnly(reduceOnly bool) *OrderService {
	s.reduceOnly = &reduceOnly
	return s
}

// RespType 设置响应类型（ACK/RESULT/FULL），影响返回的订单字段完整度。
func (s *OrderService) RespType(t string) *OrderService {
	s.newOrderRespType = t
	return s
}

// PriceMatch 设置价格匹配模式（如 OPPONENT、QUEUE），与 Price 二选一；传入 "NONE" 表示不用。
func (s *OrderService) PriceMatch(m string) *OrderService {
	s.priceMatch = m
	return s
}

// SelfTradePreventionMode 设置自成交预防模式，仅在有效期策略为 GTC/IOC/GTD 时提交。
func (s *OrderService) SelfTradePreventionMode(m string) *OrderService {
	s.selfTradePreventionMode = m
	return s
}

// GoodTillDate 设置 GTD 订单的失效时间戳（毫秒），大于 0 时才会提交。
func (s *OrderService) GoodTillDate(ms int64) *OrderService {
	s.goodTillDate = ms
	return s
}

// RecvWindow 设置请求有效时间窗口（毫秒），大于 0 时才会提交。
func (s *OrderService) RecvWindow(ms int64) *OrderService {
	s.recvWindow = ms
	return s
}

// OrderID 指定要修改的订单（交易所订单号），设置后 Do 会执行改单。
func (s *OrderService) OrderID(orderID int64) *OrderService {
	s.orderID = &orderID
	return s
}

// ClientOrderID 指定要修改的订单（目标订单的客户端订单号），设置后 Do 会执行改单。
// 下单时若要自定义客户端订单号，请用 NewClientOrderID。
func (s *OrderService) ClientOrderID(id string) *OrderService {
	s.clientOrderID = id
	return s
}

// NewClientOrderID 设置下单时使用的客户端订单号；未设置时 Build 会用 GenID 自动生成。
func (s *OrderService) NewClientOrderID(id string) *OrderService {
	s.newClientOrderID = id
	return s
}

// ModifyID 设置改单标识，仅作回显，大于 0 时才会提交。
func (s *OrderService) ModifyID(modifyID int64) *OrderService {
	s.modifyID = modifyID
	return s
}

// Do 提交请求：未指定 OrderID/ClientOrderID 时下单，否则改单。
func (s *OrderService) Do(ctx context.Context) (*WsOrder, error) {
	if s.orderID != nil || s.clientOrderID != "" {
		return s.DoModify(ctx)
	}
	return s.DoPlace(ctx)
}

// DoPlace 仅执行下单。参数不完整或不符合交易对规则时返回错误。
func (s *OrderService) DoPlace(ctx context.Context) (*WsOrder, error) {
	params, err := s.Build()
	if err != nil {
		return nil, err
	}
	return s.w.PlaceOrder(ctx, params)
}

// DoModify 仅执行改单。缺少订单标识、数量或价格信息时返回错误。
func (s *OrderService) DoModify(ctx context.Context) (*WsOrder, error) {
	params, err := s.BuildModify()
	if err != nil {
		return nil, err
	}
	return s.w.ModifyOrder(ctx, params)
}

// BuildModify 将链式设置转换为币安的改单请求参数。
// 缺少 symbol/side/订单标识，或修正后仍缺少 quantity 与 price/priceMatch 时返回错误。
func (s *OrderService) BuildModify() (map[string]any, error) {
	if s.symbol == "" {
		return nil, errors.New("缺少 symbol, 请用 Symbol()")
	}
	if s.side == "" {
		return nil, errors.New(`缺少 side, 请用 Side("BUY"/"SELL")`)
	}
	if s.orderID == nil && s.clientOrderID == "" {
		return nil, errors.New("改单必须指定订单, 请用 OrderID() 或 ClientOrderID()")
	}

	if err := s.normalize(); err != nil {
		return nil, err
	}

	if s.quantity == "" {
		return nil, errors.New("改单缺少 quantity, 请用 Quantity() 或 Usdt()")
	}
	if s.price == "" && s.priceMatch == "" {
		return nil, errors.New("改单缺少 price 或 priceMatch, 请用 Price()/PriceMatch()")
	}

	p := make(map[string]any, 8)
	p["symbol"] = s.symbol
	p["side"] = s.side
	p["positionSide"] = s.positionSide
	p["quantity"] = s.quantity
	if s.price != "" {
		p["price"] = s.price
	}
	if s.priceMatch != "" {
		p["priceMatch"] = s.priceMatch
	}
	if s.orderID != nil {
		p["orderId"] = *s.orderID
	}
	if s.clientOrderID != "" {
		p["origClientOrderId"] = s.clientOrderID
	}
	if s.modifyID > 0 {
		p["modifyId"] = s.modifyID
	}
	if s.recvWindow > 0 {
		p["recvWindow"] = s.recvWindow
	}
	return p, nil
}

// Build 将链式设置转换为币安的下单请求参数，未指定客户端订单号时会自动用 GenID 生成。
// 缺少 symbol/side，订单类型不支持，或缺少必需的价格/数量时返回错误。
func (s *OrderService) Build() (map[string]any, error) {

	if s.symbol == "" {
		return nil, errors.New("缺少 symbol, 请用 Symbol()")
	}
	if s.side == "" {
		return nil, errors.New(`缺少 side, 请用 Side("BUY"/"SELL")`)
	}
	if s.orderType == "" {
		s.orderType = "MARKET"
	}

	if err := s.normalize(); err != nil {
		return nil, err
	}

	p := make(map[string]any, 10)
	p["symbol"] = s.symbol
	p["side"] = s.side
	p["type"] = s.orderType
	p["positionSide"] = s.positionSide

	switch s.orderType {
	case "LIMIT":
		tif := s.timeInForce
		if tif == "" {
			tif = "GTC"
		}
		p["timeInForce"] = tif
		if s.quantity == "" {
			return nil, errors.New("LIMIT 订单缺少 quantity, 请用 Quantity() 或 Usdt()")
		}
		p["quantity"] = s.quantity
		switch {
		case s.price != "":
			p["price"] = s.price
		case s.priceMatch != "":
			p["priceMatch"] = s.priceMatch
		default:
			return nil, errors.New("LIMIT 订单缺少 price 或 priceMatch, 请用 Price()/PriceMatch()")
		}
	case "MARKET":
		if s.quantity == "" {
			return nil, errors.New("MARKET 订单缺少 quantity, 请用 Quantity() 或 Usdt()")
		}
		p["quantity"] = s.quantity

	default:
		return nil, fmt.Errorf("不支持的订单类型: %s", s.orderType)
	}

	if s.reduceOnly != nil && s.positionSide == "BOTH" {
		p["reduceOnly"] = *s.reduceOnly
	}

	if s.newClientOrderID != "" {
		p["newClientOrderId"] = s.newClientOrderID
	} else {
		p["newClientOrderId"] = GenID()
	}

	if s.newOrderRespType != "" {
		p["newOrderRespType"] = s.newOrderRespType
	}

	if s.selfTradePreventionMode != "" {
		tif := s.timeInForce
		if tif == "" && s.orderType == "LIMIT" {
			tif = "GTC"
		}
		switch tif {
		case "GTC", "IOC", "GTD":
			p["selfTradePreventionMode"] = s.selfTradePreventionMode
		}
	}
	if s.goodTillDate > 0 {
		p["goodTillDate"] = s.goodTillDate
	}
	if s.recvWindow > 0 {
		p["recvWindow"] = s.recvWindow
	}

	return p, nil
}
