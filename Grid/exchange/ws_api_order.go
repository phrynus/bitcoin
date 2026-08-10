package exchange

import (
	"context"
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

// PlaceOrder 下单(order.place, 签名接口)。
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
func (w *WsApi) PlaceOrder(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return Call[*WsOrder](w, ctx, "order.place", params, true)
}

// ModifyOrder 修改订单(order.modify, 签名接口)。当前只支持限价(LIMIT)订单修改,
// 修改后会在撮合队列里重新排序。
//
// params 需包含 symbol、side、quantity、price, 且 orderId 与 origClientOrderId 必须至少传一个
// (若同时传入则以 orderId 为准), 例如:
//
//	{"symbol":"BTCUSDT","side":"BUY","quantity":0.11,"price":43769.1,"orderId":328971409}
//
// 常用可选参数: priceMatch、modifyId(自定义改单标识, 原样返回)、recvWindow。
// 注意: 同一订单最多可修改 10000 次; 订单已部分成交且新 quantity<=executedQty 时, 本次修改会导致订单被取消。
func (w *WsApi) ModifyOrder(ctx context.Context, params map[string]any) (*WsOrder, error) {
	return Call[*WsOrder](w, ctx, "order.modify", params, true)
}
