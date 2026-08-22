package exchange

import (
	"strconv"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

type subEntry struct {
	f     func(o futures.WsOrderTradeUpdate)
	timer *time.Timer
	done  chan struct{}
	once  sync.Once
}

func (e *subEntry) finish() {
	e.once.Do(func() { close(e.done) })
}

var (
	subOrderTrade   map[string]*subEntry
	subOrderTradeMu sync.Mutex
)

func (u *UserData) userHandler(event *futures.WsUserDataEvent) {

	if event.Event == "ORDER_TRADE_UPDATE" {
		u.handleOrderTradeUpdate(event)
	}

}

func (u *UserData) handleOrderTradeUpdate(event *futures.WsUserDataEvent) {
	o := event.WsUserDataOrderTradeUpdate.OrderTradeUpdate

	var st int
	if o.Status == "FILLED" {
		st = 2
	} else {
		switch o.Status {
		case "NEW":
			st = 0
		case "PARTIALLY_FILLED":
			st = 1
		}
	}

	id := o.ClientOrderID + "-" + strconv.Itoa(st)
	subOrderTradeMu.Lock()
	e, ok := subOrderTrade[id]
	if ok {
		delete(subOrderTrade, id)
	}
	subOrderTradeMu.Unlock()
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

func (u *UserData) Subscribe(id string, status int, f func(o futures.WsOrderTradeUpdate), timeout time.Duration, onTimeout func()) <-chan struct{} {
	if f == nil {
		return nil
	}
	key := id + "-" + strconv.Itoa(status)
	e := &subEntry{f: f, done: make(chan struct{})}

	subOrderTradeMu.Lock()
	if subOrderTrade == nil {
		subOrderTrade = make(map[string]*subEntry)
	}

	if old, ok := subOrderTrade[key]; ok && old.timer != nil {
		old.timer.Stop()
	}
	subOrderTrade[key] = e
	if timeout > 0 && onTimeout != nil {
		e.timer = time.AfterFunc(timeout, func() {
			subOrderTradeMu.Lock()
			cur, ok := subOrderTrade[key]
			if ok && cur == e {
				delete(subOrderTrade, key)
			}
			subOrderTradeMu.Unlock()
			if ok && cur == e {
				go func() {
					defer e.finish()
					onTimeout()
				}()
			}
		})
	}
	subOrderTradeMu.Unlock()
	return e.done
}

func (u *UserData) Unsubscribe(id string, status int) {
	key := id + "-" + strconv.Itoa(status)
	subOrderTradeMu.Lock()
	defer subOrderTradeMu.Unlock()
	if e, ok := subOrderTrade[key]; ok {
		if e.timer != nil {
			e.timer.Stop()
		}
		delete(subOrderTrade, key)
		e.finish()
	}
}
