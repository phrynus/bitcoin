package exchange

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
)

// WsOrder 下单返回的订单信息(order.place)。
type WsOrder struct {
	OrderID                 int64  `json:"orderId"`                 // 订单 ID
	Symbol                  string `json:"symbol"`                  // 交易对
	Status                  string `json:"status"`                  // 订单状态(NEW/PARTIALLY_FILLED/FILLED/CANCELED/EXPIRED)
	ClientOrderID           string `json:"clientOrderId"`           // 客户端订单 ID
	ModifyID                int64  `json:"modifyId"`                // 用户自定义改单标识, 仅 order.modify 传入时返回
	Price                   string `json:"price"`                   // 订单价格
	OrigQty                 string `json:"origQty"`                 // 原始数量
	ExecutedQty             string `json:"executedQty"`             // 已成交量
	CumQty                  string `json:"cumQty"`                  // 累计成交量
	TimeInForce             string `json:"timeInForce"`             // 有效方式(GTC/IOC/FOK/GTX/GTD/RPI)
	Type                    string `json:"type"`                    // 订单类型(LIMIT/MARKET)
	ReduceOnly              bool   `json:"reduceOnly"`              // 是否只减仓
	ClosePosition           bool   `json:"closePosition"`           // 是否平仓
	Side                    string `json:"side"`                    // 买卖方向(BUY/SELL)
	PositionSide            string `json:"positionSide"`            // 持仓方向(BOTH/LONG/SHORT)
	StopPrice               string `json:"stopPrice"`               // 触发价
	WorkingType             string `json:"workingType"`             // 触发价格类型(MARK_PRICE/CONTRACT_PRICE)
	PriceProtect            bool   `json:"priceProtect"`            // 是否启用价格保护
	OrigType                string `json:"origType"`                // 原始订单类型
	PriceMatch              string `json:"priceMatch"`              // 价格匹配模式
	SelfTradePreventionMode string `json:"selfTradePreventionMode"` // 自成交保护模式
	GoodTillDate            int64  `json:"goodTillDate"`            // GTD 订单的自动取消时间(毫秒)
	UpdateTime              int64  `json:"updateTime"`              // 最后更新时间(毫秒)
}

// OrderPlace 下单(order.place, 签名接口)。
//
// params 需包含 symbol、side、type, 其余可选参数按需传入, 例如:
//
//	limit 单:  {"symbol":"BTCUSDT","side":"BUY","type":"LIMIT","timeInForce":"GTC","quantity":0.1,"price":43187.0}
//	市价单:   {"symbol":"BTCUSDT","side":"SELL","type":"MARKET","quantity":0.1}
//
// 常用可选参数: positionSide、reduceOnly、newClientOrderId、newOrderRespType、priceMatch、
// selfTradePreventionMode、goodTillDate、recvWindow。
// 说明: 签名、apiKey、timestamp 由 WsApi 自动附加; price/quantity 直接传 float64 即可,
// 精度由业务层自行保证(可参考 utils.go 的 ToString 或 strconv.FormatFloat)。
func (w *WsApi) OrderPlace(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return Call[*WsOrder](w, ctx, "order.place", params, true)
}

// OrderModify 修改订单(order.modify, 签名接口)。当前只支持限价(LIMIT)订单修改,
// 修改后会在撮合队列里重新排序。
//
// params 需包含 symbol、side、quantity、price, 且 orderId 与 origClientOrderId 必须至少传一个
// (若同时传入则以 orderId 为准), 例如:
//
//	{"symbol":"BTCUSDT","side":"BUY","quantity":0.11,"price":43769.1,"orderId":328971409}
//
// 常用可选参数: priceMatch、modifyId(自定义改单标识, 原样返回)、recvWindow。
// 注意: 同一订单最多可修改 10000 次; 订单已部分成交且新 quantity<=executedQty 时, 本次修改会导致订单被取消。
func (w *WsApi) OrderModify(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return Call[*WsOrder](w, ctx, "order.modify", params, true)
}

// 下单服务(链式), 参考 github.com/adshao/go-binance/v2/futures 的 OrderService 设计。
//
// 用法(参数直接填文本, 无需类型常量):
//
//	order, err := exc.WsApi.NewOrder().
//		Symbol("BTCUSDT").
//		Side("BUY").
//		PositionSide("LONG").
//		Type("LIMIT").
//		TimeInForce("GTC").
//		Quantity("0.001").
//		Price("60000").
//		OrderPlace(ctx)
//
// 按 USDT 金额下单(数量 = 金额 / 价格, 自动做精度修正):
//
//	order, err := exc.WsApi.NewOrder().
//		Symbol("BTCUSDT").
//		Side("BUY").
//		PositionSide("LONG").
//		Type("LIMIT").
//		Usdt("100"). // 下单 100 U, 必须配合 Price()
//		Price("60000").
//		OrderPlace(ctx)
//
// 修改订单(同一服务链式复用):
//
//	order, err := exc.WsApi.NewOrder().
//		Symbol("BTCUSDT").
//		Side("BUY").
//		Quantity("0.001").
//		Price("60500").
//		OrderID(328971409). // 或 OrigClientOrderID("..."), 至少传一个
//		OrderModify(ctx)
//
// 自动补充参数:
//   - timestamp / apiKey / signature 由 WsApi 签名时自动附加, 无需关心;
//   - newClientOrderId 未传时自动生成(满足 ^[\.A-Z:/a-z0-9_-]{1,36}$);
//   - 订单类型不写默认 MARKET; LIMIT 单 timeInForce 不写默认 GTC、selfTradePreventionMode 默认 NONE;
//   - 双向持仓(LONG/SHORT)自动剔除 reduceOnly(交易所规定不可传);
//   - MARKET 单自动剔除 price/priceMatch/timeInForce 等无效参数。

// OrderService 下单服务(双向持仓)。
type OrderService struct {
	w *WsApi

	symbol                  string
	side                    string
	positionSide            string
	orderType               string
	timeInForce             string
	quantity                string
	usdt                    string // 按 USDT 金额下单(数量 = usdt/价格), 与 quantity 二选一
	reduceOnly              *bool
	price                   string
	newClientOrderID        string
	newOrderRespType        string
	priceMatch              string
	selfTradePreventionMode string
	goodTillDate            int64
	recvWindow              int64
	orderID                 *int64
	origClientOrderID       string
	modifyID                int64
}

// NewOrder 创建下单服务。
func (w *WsApi) NewOrder() *OrderService {
	return &OrderService{w: w}
}

// fix 校验交易对是否在包级 exc.Symbols 列表中, 并修正价格/数量。
// exc 为 nil 时不修正(交换器未初始化)。
func (s *OrderService) fix() error {
	if exc == nil {
		return nil
	}
	si, ok := exc.GetSymbol(s.symbol)
	if !ok {
		return fmt.Errorf("交易对 %s 不在 Symbols 交易对列表中, 无法下单", s.symbol)
	}
	// 按 USDT 金额下单: 金额 >= MinNotional, 通过价格计算数量, 再做精度修正
	if s.usdt != "" {
		return s.fixUsdt(si)
	}
	// 有显式价格(限价单/改单): 价格+数量同时修正, 并校验名义价值
	if s.price != "" && s.priceMatch == "" && s.quantity != "" {
		fp, fq, err := FixOrderText(si, s.price, s.quantity)
		if err != nil {
			return err
		}
		s.price = fp
		s.quantity = fq
		return nil
	}
	// 无显式价格(MARKET 单 / priceMatch 单): 仅修正数量
	if s.quantity != "" {
		fq, err := FixQuantityText(si, s.quantity)
		if err != nil {
			return err
		}
		s.quantity = fq
	}
	return nil
}

// fixUsdt 按 USDT 金额计算数量并做价格/数量精度修正:
//   - 金额不得小于该交易对的最小名义价值(MinNotional);
//   - 必须显式设置价格(Price), 数量 = USDT金额 / 价格;
//   - 计算出的数量与价格再交给 FixOrderText 做步长/范围/名义价值修正。
func (s *OrderService) fixUsdt(si *SymbolInfo) error {
	if s.quantity != "" {
		return errors.New("Quantity() 与 Usdt() 只能二选一")
	}
	if s.price == "" {
		return errors.New("按 USDT 金额下单必须设置价格, 请用 Price()")
	}
	if s.priceMatch != "" {
		return errors.New("按 USDT 金额下单不能同时使用 PriceMatch()")
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
	// 金额不能小于最小名义价值
	if si.MinNotionalFilter != nil && si.MinNotionalFilter.Notional != "" {
		if notional := decOrZero(si.MinNotionalFilter.Notional); amount.LessThan(notional) {
			return fmt.Errorf("USDT 金额 %s 小于最小名义价值 %s", s.usdt, si.MinNotionalFilter.Notional)
		}
	}
	// 数量 = 金额 / 价格(精确十进制), 再交给 FixOrderText 做精度修正
	qty := amount.Div(price)
	fp, fq, err := FixOrderText(si, s.price, qty.String())
	if err != nil {
		return err
	}
	s.price = fp
	s.quantity = fq
	return nil
}

// Symbol 设置交易对
func (s *OrderService) Symbol(symbol string) *OrderService {
	s.symbol = symbol
	return s
}

// Side 设置买卖方向(BUY/SELL)
func (s *OrderService) Side(side string) *OrderService {
	s.side = side
	return s
}

// PositionSide 设置持仓方向(双向持仓必填: LONG/SHORT)
func (s *OrderService) PositionSide(positionSide string) *OrderService {
	s.positionSide = positionSide
	return s
}

// Type 设置订单类型(LIMIT/MARKET; 不写默认 MARKET)
func (s *OrderService) Type(orderType string) *OrderService {
	s.orderType = orderType
	return s
}

// TimeInForce 设置有效方式(GTC/IOC/FOK/GTX/GTD/RPI; LIMIT 可不写, 默认 GTC)
func (s *OrderService) TimeInForce(timeInForce string) *OrderService {
	s.timeInForce = timeInForce
	return s
}

// Quantity 设置数量
func (s *OrderService) Quantity(quantity string) *OrderService {
	s.quantity = quantity
	return s
}

// Usdt 按报价货币(USDT)金额下单: 数量 = USDT金额 / 价格。
// 与 Quantity() 二选一; 使用 Usdt() 时必须同时设置 Price()。
// 金额不得小于该交易对的最小名义价值(MinNotional), 计算出数量后会自动做价格/数量精度修正。
func (s *OrderService) Usdt(usdt string) *OrderService {
	s.usdt = usdt
	return s
}

// Price 设置价格(仅 LIMIT)
func (s *OrderService) Price(price string) *OrderService {
	s.price = price
	return s
}

// ReduceOnly 设置只减仓(仅单向持仓可用; 双向持仓下自动剔除)
func (s *OrderService) ReduceOnly(reduceOnly bool) *OrderService {
	s.reduceOnly = &reduceOnly
	return s
}

// NewClientOrderID 设置客户端订单 ID(未传则自动生成)
func (s *OrderService) NewClientOrderID(id string) *OrderService {
	s.newClientOrderID = id
	return s
}

// NewOrderResponseType 设置响应类型(ACK/RESULT)
func (s *OrderService) NewOrderResponseType(t string) *OrderService {
	s.newOrderRespType = t
	return s
}

// PriceMatch 设置价格匹配模式(仅 LIMIT, 不能与 Price 同时传)
func (s *OrderService) PriceMatch(m string) *OrderService {
	s.priceMatch = m
	return s
}

// SelfTradePreventionMode 设置自成交保护模式(默认 NONE)
func (s *OrderService) SelfTradePreventionMode(m string) *OrderService {
	s.selfTradePreventionMode = m
	return s
}

// GoodTillDate 设置 GTD 订单自动取消时间(配合 TimeInForce("GTD") 使用)
func (s *OrderService) GoodTillDate(ms int64) *OrderService {
	s.goodTillDate = ms
	return s
}

// RecvWindow 设置接收窗口(毫秒)
func (s *OrderService) RecvWindow(ms int64) *OrderService {
	s.recvWindow = ms
	return s
}

// OrderID 设置订单 ID(与 OrigClientOrderID 至少传一个; 同时传时以 orderId 为准)
func (s *OrderService) OrderID(orderID int64) *OrderService {
	s.orderID = &orderID
	return s
}

// OrigClientOrderID 设置原始客户端订单 ID(与 OrderID 至少传一个)
func (s *OrderService) OrigClientOrderID(id string) *OrderService {
	s.origClientOrderID = id
	return s
}

// ModifyID 设置自定义改单标识(交易所原样返回)
func (s *OrderService) ModifyID(modifyID int64) *OrderService {
	s.modifyID = modifyID
	return s
}

// DoPlace 生成参数并发送 order.place 请求。
func (s *OrderService) DoPlace(ctx context.Context) (*WsOrder, error) {
	params, err := s.Build()
	if err != nil {
		return nil, err
	}
	return s.w.OrderPlace(ctx, params)
}

// DoModify 生成改单参数并发送 order.modify 请求(当前仅支持 LIMIT)。
func (s *OrderService) DoModify(ctx context.Context) (*WsOrder, error) {
	params, err := s.BuildModify()
	if err != nil {
		return nil, err
	}
	return s.w.OrderModify(ctx, params)
}

// BuildModify 生成改单参数(order.modify)。
//
// 必填: symbol、side、quantity、price(或 priceMatch)、orderId 或 origClientOrderId(至少一个)。
// 注意: 同一订单最多修改 10000 次; 已部分成交且新 quantity<=executedQty 时订单会被取消。
// 会自动校验交易对(exc.Symbols)并修正价格/数量(含最小名义价值)。
func (s *OrderService) BuildModify() (map[string]any, error) {
	if s.symbol == "" {
		return nil, errors.New("缺少 symbol, 请用 Symbol()")
	}
	if s.side == "" {
		return nil, errors.New(`缺少 side, 请用 Side("BUY"/"SELL")`)
	}
	if s.quantity == "" && s.usdt == "" {
		return nil, errors.New("缺少 quantity, 请用 Quantity() 或 Usdt()")
	}
	if s.price == "" && s.priceMatch == "" {
		return nil, errors.New("改单缺少 price 或 priceMatch, 请用 Price()/PriceMatch()")
	}
	if s.orderID == nil && s.origClientOrderID == "" {
		return nil, errors.New("改单必须指定订单, 请用 OrderID() 或 OrigClientOrderID()")
	}

	// 交易对校验 + 价格/数量数据修正(基于包级 exc.Symbols)
	if err := s.fix(); err != nil {
		return nil, err
	}

	p := make(map[string]any, 6)
	p["symbol"] = s.symbol
	p["side"] = s.side
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
	if s.origClientOrderID != "" {
		p["origClientOrderId"] = s.origClientOrderID
	}
	if s.modifyID > 0 {
		p["modifyId"] = s.modifyID
	}
	if s.recvWindow > 0 {
		p["recvWindow"] = s.recvWindow
	}
	return p, nil
}

// Build 生成订单参数并自动补充/修正(返回 map, 便于调试或复用)。
// 会自动校验交易对(exc.Symbols)并修正价格/数量(含最小名义价值)。
func (s *OrderService) Build() (map[string]any, error) {
	// 必填校验
	if s.symbol == "" {
		return nil, errors.New("缺少 symbol, 请用 Symbol()")
	}
	if s.side == "" {
		return nil, errors.New(`缺少 side, 请用 Side("BUY"/"SELL")`)
	}
	if s.orderType == "" {
		s.orderType = "MARKET" // 默认市价单, Type() 可不写
	}
	if s.positionSide == "" {
		return nil, errors.New(`双向持仓模式必须设置持仓方向, 请用 PositionSide("LONG"/"SHORT")`)
	}

	// 交易对校验 + 价格/数量数据修正(基于包级 exc.Symbols)
	if err := s.fix(); err != nil {
		return nil, err
	}

	p := make(map[string]any, 8)
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
			return nil, errors.New("LIMIT 订单缺少 quantity, 请用 Quantity()")
		}
		p["quantity"] = s.quantity
		if s.price != "" {
			p["price"] = s.price
		} else if s.priceMatch == "" {
			return nil, errors.New("LIMIT 订单缺少 price 或 priceMatch")
		}
		// stp := s.selfTradePreventionMode
		// if stp == "" {
		// 	stp = "NONE"
		// }
		// p["selfTradePreventionMode"] = stp
	case "MARKET":
		if s.quantity == "" {
			return nil, errors.New("MARKET 订单缺少 quantity, 请用 Quantity()")
		}
		p["quantity"] = s.quantity
		// 市价单自动剔除 price/priceMatch/timeInForce 等无效参数(不写入即可)
	default:
		return nil, fmt.Errorf("不支持的订单类型: %v", s.orderType)
	}

	// 双向持仓(LONG/SHORT)下 reduceOnly 不可传, 自动剔除
	if s.positionSide != "BOTH" && s.reduceOnly != nil {
		// 忽略
	} else if s.reduceOnly != nil {
		p["reduceOnly"] = *s.reduceOnly
	}

	// 客户端订单 ID(未传自动生成)
	if s.newClientOrderID != "" {
		p["newClientOrderId"] = s.newClientOrderID
	} else {
		p["newClientOrderId"] = genClientOrderID()
	}

	if s.newOrderRespType != "" {
		p["newOrderRespType"] = s.newOrderRespType
	}
	if s.priceMatch != "" {
		p["priceMatch"] = s.priceMatch
	}
	if s.goodTillDate > 0 {
		p["goodTillDate"] = s.goodTillDate
	}
	if s.recvWindow > 0 {
		p["recvWindow"] = s.recvWindow
	}

	return p, nil
}

// genClientOrderID 生成唯一客户端订单 ID(字母数字 ≤36 位, 满足交易所格式要求)。
func genClientOrderID() string {
	return "g" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
