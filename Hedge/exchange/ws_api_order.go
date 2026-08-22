package exchange

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

type WsOrder struct {
	OrderID                 int64  `json:"orderId"`
	Symbol                  string `json:"symbol"`
	Status                  string `json:"status"`
	ClientOrderID           string `json:"clientOrderId"`
	ModifyID                int64  `json:"modifyId"`
	Price                   string `json:"price"`
	OrigQty                 string `json:"origQty"`
	ExecutedQty             string `json:"executedQty"`
	CumQty                  string `json:"cumQty"`
	TimeInForce             string `json:"timeInForce"`
	Type                    string `json:"type"`
	ReduceOnly              bool   `json:"reduceOnly"`
	ClosePosition           bool   `json:"closePosition"`
	Side                    string `json:"side"`
	PositionSide            string `json:"positionSide"`
	StopPrice               string `json:"stopPrice"`
	WorkingType             string `json:"workingType"`
	PriceProtect            bool   `json:"priceProtect"`
	ActivatePrice           string `json:"activatePrice"`
	PriceRate               string `json:"priceRate"`
	OrigType                string `json:"origType"`
	PriceMatch              string `json:"priceMatch"`
	SelfTradePreventionMode string `json:"selfTradePreventionMode"`
	GoodTillDate            int64  `json:"goodTillDate"`
	UpdateTime              int64  `json:"updateTime"`
}

//

//
//	limit 单:  {"symbol":"BTCUSDT","side":"BUY","type":"LIMIT","timeInForce":"GTC","quantity":0.1,"price":43187.0}
//	市价单:   {"symbol":"BTCUSDT","side":"SELL","type":"MARKET","quantity":0.1}
//

func (w *WsApi) PlaceOrder(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return call[*WsOrder](w, ctx, "order.place", params, true)
}

//

//
//	{"symbol":"BTCUSDT","side":"BUY","quantity":0.11,"price":43769.1,"orderId":328971409}
//

func (w *WsApi) ModifyOrder(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return call[*WsOrder](w, ctx, "order.modify", params, true)
}

//

//

//

//
//	order, err := wsApi.NewOrder().
//		Symbol("BTCUSDT").Side("BUY").PositionSide("LONG").
//		Type("LIMIT").TimeInForce("GTC").
//		Quantity("0.001").Price("60000").
//		Do(ctx)
//

//
//	order, err := wsApi.NewOrder().
//		Symbol("BTCUSDT").Side("BUY").PositionSide("LONG").
//		Type("LIMIT").Usdt("100").
//		Price("60000").
//		Do(ctx)
//

//
//	order, err := wsApi.NewOrder().
//		Symbol("BTCUSDT").Side("BUY").PositionSide("LONG").
//		Quantity("0.001").Price("60500").
//		OrderID(328971409).
//		Do(ctx)
//

type OrderService struct {
	w *WsApi

	symbol        string
	side          string
	positionSide  string
	quantity      string
	usdt          string
	price         string
	priceMatch    string
	clientOrderID string
	recvWindow    int64

	orderType               string
	timeInForce             string
	reduceOnly              *bool
	newOrderRespType        string
	selfTradePreventionMode string
	goodTillDate            int64

	orderID  *int64
	modifyID int64
}

func (w *WsApi) NewOrder() *OrderService {
	return &OrderService{w: w}
}

//

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

func (s *OrderService) Symbol(symbol string) *OrderService {
	s.symbol = symbol
	return s
}

func (s *OrderService) Side(side string) *OrderService {
	s.side = side
	return s
}

func (s *OrderService) PositionSide(positionSide string) *OrderService {
	s.positionSide = positionSide
	return s
}

func (s *OrderService) Type(orderType string) *OrderService {
	s.orderType = orderType
	return s
}

func (s *OrderService) TimeInForce(timeInForce string) *OrderService {
	s.timeInForce = timeInForce
	return s
}

func (s *OrderService) Quantity(quantity string) *OrderService {
	s.quantity = quantity
	return s
}

func (s *OrderService) Usdt(usdt string) *OrderService {
	s.usdt = usdt
	return s
}

func (s *OrderService) Price(price string) *OrderService {
	s.price = price
	return s
}

func (s *OrderService) ReduceOnly(reduceOnly bool) *OrderService {
	s.reduceOnly = &reduceOnly
	return s
}

func (s *OrderService) RespType(t string) *OrderService {
	s.newOrderRespType = t
	return s
}

func (s *OrderService) PriceMatch(m string) *OrderService {
	s.priceMatch = m
	return s
}

func (s *OrderService) SelfTradePreventionMode(m string) *OrderService {
	s.selfTradePreventionMode = m
	return s
}

func (s *OrderService) GoodTillDate(ms int64) *OrderService {
	s.goodTillDate = ms
	return s
}

func (s *OrderService) RecvWindow(ms int64) *OrderService {
	s.recvWindow = ms
	return s
}

func (s *OrderService) OrderID(orderID int64) *OrderService {
	s.orderID = &orderID
	return s
}

func (s *OrderService) ClientOrderID(id string) *OrderService {
	s.clientOrderID = id
	return s
}

func (s *OrderService) ModifyID(modifyID int64) *OrderService {
	s.modifyID = modifyID
	return s
}

func (s *OrderService) Do(ctx context.Context) (*WsOrder, error) {
	if s.orderID != nil || s.clientOrderID != "" {
		return s.DoModify(ctx)
	}
	return s.DoPlace(ctx)
}

func (s *OrderService) DoPlace(ctx context.Context) (*WsOrder, error) {
	params, err := s.Build()
	if err != nil {
		return nil, err
	}
	return s.w.PlaceOrder(ctx, params)
}

func (s *OrderService) DoModify(ctx context.Context) (*WsOrder, error) {
	params, err := s.BuildModify()
	if err != nil {
		return nil, err
	}
	return s.w.ModifyOrder(ctx, params)
}

//

//

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

//

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

	if s.clientOrderID != "" {
		p["newClientOrderId"] = s.clientOrderID
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
