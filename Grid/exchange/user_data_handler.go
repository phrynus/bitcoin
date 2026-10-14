package exchange

import (
	"sync"
	"time"

	"github.com/phrynus/plog"
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

// SubOrderFunc 为订单事件回调：status 为币安订单状态（NEW、PARTIALLY_FILLED、FILLED、CANCELED、EXPIRED 等），
// o 为该订单完整的订单/成交更新载荷。订单每次状态推送都会回调一次，是否重复由调用方自行处理。
type SubOrderFunc func(status string, o WsOrderTradeUpdate)

// SubOption 为 Subscribe 的可选配置项。
type SubOption func(*subEntry)

// WithTimeout 设置超时：在 timeout 内未收到该订单的任何事件时自动取消订阅，
// 并在 onTimeout 非空时异步调用它。timeout <= 0 时视为不超时（不做任何设置）。
func WithTimeout(timeout time.Duration, onTimeout func()) SubOption {
	return func(e *subEntry) {
		if timeout > 0 {
			e.timeout = timeout
			e.onTimeout = onTimeout
		}
	}
}

type subEntry struct {
	f         SubOrderFunc
	timer     *time.Timer
	timeout   time.Duration
	onTimeout func()
	done      chan struct{}
	once      sync.Once
}

func (e *subEntry) finish() {
	e.once.Do(func() { close(e.done) })
}

func (u *UserData) userHandler(event *WsUserDataEvent) {
	if event.Event == "ORDER_TRADE_UPDATE" {
		u.handleOrderTradeUpdate(event)
		return
	}
	plog.Debugf("[数据流] 忽略非订单事件 类型=%s", event.Event)
}

// handleOrderTradeUpdate 把订单事件按客户端订单号分发给对应的订阅。
// 不做状态过滤：NEW、PARTIALLY_FILLED、FILLED、CANCELED、EXPIRED 等所有状态都会触发回调，
// 由回调根据 status 自行判断；订阅不会因收到事件而移除，重复推送会重复回调。
// 首次收到事件后会停止超时计时器（之后不再触发超时）。
func (u *UserData) handleOrderTradeUpdate(event *WsUserDataEvent) {
	o := event.OrderTradeUpdate

	u.subMu.Lock()
	e, ok := u.subs[o.ClientOrderID]
	if ok && e.timer != nil {
		e.timer.Stop()
	}
	u.subMu.Unlock()
	if !ok {
		plog.Debugf("[数据流] 订单事件无匹配订阅, 忽略 订单=%s 状态=%s", o.ClientOrderID, o.Status)
		return
	}

	plog.Debugf("[数据流] 分发订单事件 订单=%s 状态=%s 累计成交=%s", o.ClientOrderID, o.Status, o.AccumulatedFilledQty)
	go e.f(o.Status, o)
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
	plog.Debugf("[数据流] 已清空全部订单订阅 数量=%d", len(subs))
}

// Subscribe 按客户端订单号注册订单事件回调。
// 该订单的任意状态变化（NEW、PARTIALLY_FILLED、FILLED、CANCELED、EXPIRED 等）都会触发 f，
// f 收到 status（订单状态）与 o（订单/成交数据）两个参数；部分成交与完全成交都会触发，且会重复回调。
// 订阅不会自动移除，去重与常用取消由调用方自行处理（可用 Unsubscribe）。
// 可选配置见 WithTimeout：不传则不超时；同一订单的旧订阅会被新订阅覆盖（旧计时器停止）。
// 返回的 channel 在订阅被取消、超时或 UserData 关闭后关闭；f 为 nil 或 UserData 已关闭时返回 nil。
func (u *UserData) Subscribe(id string, f SubOrderFunc, opts ...SubOption) <-chan struct{} {
	if f == nil || u.isClosed() {
		return nil
	}
	e := &subEntry{f: f, done: make(chan struct{})}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}

	u.subMu.Lock()
	if u.subs == nil {
		u.subs = make(map[string]*subEntry)
	}

	if old, ok := u.subs[id]; ok && old.timer != nil {
		old.timer.Stop()
	}
	u.subs[id] = e
	if e.timeout > 0 {
		e.timer = time.AfterFunc(e.timeout, func() {
			u.subMu.Lock()
			cur, ok := u.subs[id]
			if ok && cur == e {
				delete(u.subs, id)
			}
			u.subMu.Unlock()
			if ok && cur == e {
				go func() {
					defer e.finish()
					if e.onTimeout != nil {
						e.onTimeout()
					}
				}()
			}
		})
	}
	u.subMu.Unlock()
	plog.Debugf("[数据流] 建立订单订阅 订单=%s", id)
	return e.done
}

// Unsubscribe 取消由客户端订单号指定的订阅，停止其计时器并关闭返回的 channel。
// 订阅不存在时为空操作。
func (u *UserData) Unsubscribe(id string) {
	u.subMu.Lock()
	e, ok := u.subs[id]
	if ok {
		delete(u.subs, id)
	}
	u.subMu.Unlock()
	if ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		e.finish()
		plog.Debugf("[数据流] 取消订单订阅 订单=%s", id)
	}
}
