package exchange

import (
	"strconv"
	"sync"
	"time"
)

// WsUserDataEvent 为用户数据流推送的事件。
// 仅 ORDER_TRADE_UPDATE 的载荷会被解析到 OrderTradeUpdate；
// 其余事件（如 listenKeyExpired）只有 Event/Time 字段有值。
type WsUserDataEvent struct {
	Event            string             `json:"e"` // 事件类型，如 ORDER_TRADE_UPDATE、listenKeyExpired
	Time             int64              `json:"E"` // 事件时间（毫秒）
	TransactionTime  int64              `json:"T"` // 撮合时间（毫秒）
	OrderTradeUpdate WsOrderTradeUpdate `json:"o"` // 订单/成交更新载荷，仅 ORDER_TRADE_UPDATE 事件有效
}

// WsOrderTradeUpdate 为 ORDER_TRADE_UPDATE 事件中 o 字段的内容，即订单状态变化与成交明细。
type WsOrderTradeUpdate struct {
	Symbol               string `json:"s"`   // 交易对
	ClientOrderID        string `json:"c"`   // 客户端订单号
	Side                 string `json:"S"`   // 买卖方向
	Type                 string `json:"o"`   // 订单类型
	TimeInForce          string `json:"f"`   // 有效期策略
	OriginalQty          string `json:"q"`   // 原始委托数量
	OriginalPrice        string `json:"p"`   // 原始委托价格
	AveragePrice         string `json:"ap"`  // 平均成交价
	StopPrice            string `json:"sp"`  // 触发价
	ExecutionType        string `json:"x"`   // 执行类型
	Status               string `json:"X"`   // 订单状态，如 NEW、PARTIALLY_FILLED、FILLED、CANCELED
	OrderID              int64  `json:"i"`   // 订单号
	LastFilledQty        string `json:"l"`   // 本次成交数量
	AccumulatedFilledQty string `json:"z"`   // 累计成交数量
	LastFilledPrice      string `json:"L"`   // 本次成交价格
	CommissionAsset      string `json:"N"`   // 手续费资产
	Commission           string `json:"n"`   // 手续费
	TradeTime            int64  `json:"T"`   // 成交时间（毫秒）
	TradeID              int64  `json:"t"`   // 成交 id
	IsMaker              bool   `json:"m"`   // 本次成交是否为 maker
	IsReduceOnly         bool   `json:"R"`   // 是否只减仓
	WorkingType          string `json:"wt"`  // 触发价类型（MARK_PRICE/CONTRACT_PRICE）
	OriginalType         string `json:"ot"`  // 原始订单类型
	PositionSide         string `json:"ps"`  // 持仓方向（BOTH/LONG/SHORT）
	IsClosingPosition    bool   `json:"cp"`  // 是否全平仓
	ActivationPrice      string `json:"AP"`  // 跟踪委托激活价
	CallbackRate         string `json:"cr"`  // 跟踪委托回调比例
	PriceProtect         bool   `json:"pP"`  // 是否启用触发价保护
	RealizedPnL          string `json:"rp"`  // 本次实现盈亏
	STP                  string `json:"V"`   // 自成交预防模式
	PriceMatchMode       string `json:"pm"`  // 价格匹配模式
	GTD                  int64  `json:"gtd"` // GTD 订单失效时间（毫秒）
}

type subEntry struct {
	f     func(o WsOrderTradeUpdate)
	timer *time.Timer
	done  chan struct{}
	once  sync.Once
}

func (e *subEntry) finish() {
	e.once.Do(func() { close(e.done) })
}

func (u *UserData) userHandler(event *WsUserDataEvent) {
	if event.Event == "ORDER_TRADE_UPDATE" {
		u.handleOrderTradeUpdate(event)
	}
}

// orderStatus 把币安订单状态映射为订阅使用的状态值：0=NEW、1=PARTIALLY_FILLED、2=FILLED。
// 其余状态（CANCELED、EXPIRED 等）返回 false，不做任何分发。
func orderStatus(status string) (int, bool) {
	switch status {
	case "NEW":
		return 0, true
	case "PARTIALLY_FILLED":
		return 1, true
	case "FILLED":
		return 2, true
	default:
		return 0, false
	}
}

func (u *UserData) handleOrderTradeUpdate(event *WsUserDataEvent) {
	o := event.OrderTradeUpdate

	st, ok := orderStatus(o.Status)
	if !ok {
		return
	}

	key := o.ClientOrderID + "-" + strconv.Itoa(st)
	u.subMu.Lock()
	e, ok := u.subs[key]
	if ok {
		delete(u.subs, key)
	}
	u.subMu.Unlock()
	if ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		go func() {
			defer e.finish()
			e.f(o)
		}()
	}
}

// clearSubs 取消全部订阅：停止计时器并关闭各自的 done channel，避免 Close 后残留回调。
func (u *UserData) clearSubs() {
	u.subMu.Lock()
	subs := u.subs
	u.subs = nil
	u.subMu.Unlock()
	for _, e := range subs {
		if e.timer != nil {
			e.timer.Stop()
		}
		e.finish()
	}
}

// Subscribe 注册一个订单事件回调，key 由客户端订单号与状态组成。
// status 取 0=NEW、1=PARTIALLY_FILLED、2=FILLED，与订单事件到达时的状态匹配。
// 事件到达时会移除该订阅，并异步执行 f，因此 f 返回前返回的 channel 不会关闭。
// timeout > 0 且 onTimeout 非空时启动计时器，超时未收到事件则移除订阅并异步调用 onTimeout。
// 同一订单与状态的旧订阅会被覆盖（旧计时器停止）。
// 返回的 channel 在订阅被触发、取消或超时后关闭；f 为 nil 或 UserData 已关闭时返回 nil。
func (u *UserData) Subscribe(id string, status int, f func(o WsOrderTradeUpdate), timeout time.Duration, onTimeout func()) <-chan struct{} {
	if f == nil || u.isClosed() {
		return nil
	}
	key := id + "-" + strconv.Itoa(status)
	e := &subEntry{f: f, done: make(chan struct{})}

	u.subMu.Lock()
	if u.subs == nil {
		u.subs = make(map[string]*subEntry)
	}

	if old, ok := u.subs[key]; ok && old.timer != nil {
		old.timer.Stop()
	}
	u.subs[key] = e
	if timeout > 0 && onTimeout != nil {
		e.timer = time.AfterFunc(timeout, func() {
			u.subMu.Lock()
			cur, ok := u.subs[key]
			if ok && cur == e {
				delete(u.subs, key)
			}
			u.subMu.Unlock()
			if ok && cur == e {
				go func() {
					defer e.finish()
					onTimeout()
				}()
			}
		})
	}
	u.subMu.Unlock()
	return e.done
}

// Unsubscribe 取消由 id 与 status 指定且尚未触发的订阅，停止其计时器并关闭返回的 channel。
// 订阅不存在时为空操作。
func (u *UserData) Unsubscribe(id string, status int) {
	key := id + "-" + strconv.Itoa(status)
	u.subMu.Lock()
	defer u.subMu.Unlock()
	if e, ok := u.subs[key]; ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(u.subs, key)
		e.finish()
	}
}
