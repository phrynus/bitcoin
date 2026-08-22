package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// OrphanExit 对不在交易范围内的币种逐轮退出：
// 第一步挂限价买单减仓 USDC 空仓, 成交后再市价卖出对应的 USDT 多仓。
type OrphanExit struct {
	Symbol string
	Usdt   decimal.Decimal // 本轮减仓 USDC 的目标金额(USDT)
	ID     string
	acc    *Account
	Handle func(data *futures.WsUserDataOrderTradeUpdate)
	doneC  chan struct{}
	errCh  chan error
	timer  *time.Timer

	errOnce  sync.Once // 保证 errCh 只发送并关闭一次
	doneOnce sync.Once // 保证 doneC 只关闭一次
}

// NewOrphanExit 创建一组退出不在范围内币种的组合单。
func (a *Account) NewOrphanExit(symbol string, usdt decimal.Decimal) *OrphanExit {
	o := &OrphanExit{
		Symbol: symbol,
		Usdt:   usdt,
		ID:     uuid.New().String(),
		acc:    a,
		doneC:  make(chan struct{}),
		errCh:  make(chan error, 1),
	}
	o.Handle = o.HandleFilled
	return o
}

// fail 向错误通道发送错误并关闭(并发安全, 只生效一次)。
func (o *OrphanExit) fail(err error) {
	o.errOnce.Do(func() {
		o.errCh <- err
		close(o.errCh)
	})
}

// finish 关闭完成通道(并发安全, 只生效一次)。
func (o *OrphanExit) finish() {
	o.doneOnce.Do(func() {
		close(o.doneC)
	})
}

// Start 第一步: 挂限价买单减仓 USDC 空仓(数量按金额换算, 不超过剩余仓位)。
func (o *OrphanExit) Start() (chan struct{}, chan error) {
	o.acc.Log.Infof("开始退出不在范围内的币种 %s，挂单减仓 USDC 金额 %s，ID: %s",
		o.Symbol, formatDecimalFixed(o.Usdt, 2), o.ID)

	book, err := o.acc.WsApi.BookTicker(context.Background(), o.Symbol+"USDC")
	if err != nil {
		o.acc.Log.Errorf("获取 %sUSDC 盘口失败: %v", o.Symbol, err)
		o.fail(fmt.Errorf("get book ticker failed: %w", err))
		return o.doneC, o.errCh
	}
	if book == nil || book.AskPrice == "" {
		o.fail(errors.New("book ticker not found"))
		return o.doneC, o.errCh
	}
	askPrice := parseDecimal(book.AskPrice)
	if !askPrice.IsPositive() {
		o.fail(fmt.Errorf("invalid ask price %q", book.AskPrice))
		return o.doneC, o.errCh
	}

	price, quantity, err := formatQuantityPrice(o.Symbol+"USDC", askPrice, o.Usdt)
	if err != nil {
		o.acc.Log.Errorf("计算下单价格和数量失败: %v", err)
		o.fail(err)
		return o.doneC, o.errCh
	}

	// 数量不得超过剩余 USDC 空仓, 避免减仓超出仓位
	if remaining := o.acc.TCPositions[o.Symbol].USDC.Quantity; remaining.IsPositive() {
		if parseDecimal(quantity).GreaterThan(remaining) {
			quantity, err = formatQuantity(o.Symbol+"USDC", remaining)
			if err != nil {
				o.acc.Log.Errorf("格式化 %s USDC 剩余数量失败: %v", o.Symbol, err)
				o.fail(fmt.Errorf("format USDC quantity failed: %w", err))
				return o.doneC, o.errCh
			}
		}
	} else {
		o.fail(errors.New("USDC 无剩余仓位"))
		return o.doneC, o.errCh
	}

	o.acc.Log.Infof("提交 %sUSDC 限价买单减仓，价格 %s 数量 %s", o.Symbol, price, quantity)
	err = RetryFunc(GetEnv().RetryCount, func() error {
		order, err := o.acc.WsApi.NewOrder().
			NewClientOrderID(o.ID).
			Symbol(o.Symbol + "USDC").
			Side("BUY").
			PositionSide("SHORT").
			Type("LIMIT").
			TimeInForce("GTC").
			Quantity(quantity).
			Price(price).
			DoPlace(context.Background())
		if err != nil {
			return err
		}
		if order == nil || order.OrderID == 0 {
			return errors.New("place limit order returned empty result")
		}
		o.acc.Log.Debugf("退出组合单 %s: USDC 减仓买单已提交 orderId=%d status=%s", o.ID, order.OrderID, order.Status)
		return nil
	})
	if err != nil {
		o.acc.Log.Errorf("提交 %sUSDC 减仓买单失败: %v", o.Symbol, err)
		o.fail(fmt.Errorf("place USDC order failed: %w", err))
		return o.doneC, o.errCh
	}

	o.timer = time.AfterFunc(GetEnv().FillTimeoutDuration, func() {
		o.acc.Log.Warnf("退出 %s 的 USDC 减仓单超时未成交，取消订单 ID: %s", o.Symbol, o.ID)
		if err := o.cancelOrder(); err != nil {
			o.acc.Log.Errorf("取消订单失败: %v", err)
		}
		o.fail(errors.New("order timed out before fill"))
	})

	return o.doneC, o.errCh
}

// HandleFilled USDC 减仓买单成交后, 市价卖出对应的 USDT 数量完成本轮退出。
func (o *OrphanExit) HandleFilled(data *futures.WsUserDataOrderTradeUpdate) {
	o.acc.Log.Infof("%s USDC 减仓单已成交，数量 %s", o.Symbol, data.OrderTradeUpdate.OriginalQty)
	o.acc.Log.Debugf("退出组合单 %s: 成交价=%s 成交数量=%s",
		o.ID, data.OrderTradeUpdate.AveragePrice, data.OrderTradeUpdate.OriginalQty)
	if o.timer != nil {
		o.timer.Stop()
	}

	filledQty := parseDecimal(data.OrderTradeUpdate.OriginalQty)
	if !filledQty.IsPositive() {
		o.fail(errors.New("USDC 成交数量无效"))
		return
	}

	// 市价卖出对应数量的 USDT, 不超过剩余 USDT 仓位
	remaining := o.acc.TCPositions[o.Symbol].USDT.Quantity
	if !remaining.IsPositive() {
		o.acc.Log.Warnf("%s 无剩余 USDT 仓位, 跳过市价平仓", o.Symbol)
		o.finish()
		return
	}
	if si, err := getSymbolInfo(o.Symbol + "USDT"); err == nil && si.LotSizeFilter != nil {
		if minQty := parseDecimal(si.LotSizeFilter.MinQuantity); minQty.IsPositive() && remaining.LessThan(minQty) {
			o.acc.Log.Warnf("%s USDT 剩余仓位低于最小下单数量 %s, 跳过市价平仓(仅剩粉尘)",
				o.Symbol, si.LotSizeFilter.MinQuantity)
			o.finish()
			return
		}
	}

	quantity, err := formatQuantity(o.Symbol+"USDT", filledQty)
	if err != nil {
		o.acc.Log.Errorf("格式化 %s USDT 下单数量失败: %v", o.Symbol, err)
		o.fail(fmt.Errorf("format USDT quantity failed: %w", err))
		return
	}
	if parseDecimal(quantity).GreaterThan(remaining) {
		quantity, err = formatQuantity(o.Symbol+"USDT", remaining)
		if err != nil {
			o.acc.Log.Errorf("格式化 %s USDT 剩余数量失败: %v", o.Symbol, err)
			o.fail(fmt.Errorf("format USDT quantity failed: %w", err))
			return
		}
	}

	err = RetryFunc(GetEnv().RetryCount, func() error {
		order, err := o.acc.WsApi.NewOrder().
			Symbol(o.Symbol + "USDT").
			Side("SELL").
			PositionSide("LONG").
			Type("MARKET").
			Quantity(quantity).
			DoPlace(context.Background())
		if err != nil {
			return err
		}
		if order == nil || order.OrderID == 0 {
			return errors.New("place market order returned empty result")
		}
		return nil
	})
	if err != nil {
		o.acc.Log.Errorf("%s 卖出 USDT 失败: %v", o.Symbol, err)
		o.fail(fmt.Errorf("sell USDT failed: %w", err))
		return
	}

	o.acc.Log.Infof("%s 本轮退出完成", o.Symbol)
	o.finish()
}

// cancelOrder 撤销未成交的 USDC 减仓买单。
func (o *OrphanExit) cancelOrder() error {
	o.acc.Log.Debugf("退出组合单 %s: 开始取消 USDC 减仓买单", o.ID)
	return RetryFunc(GetEnv().RetryCount, func() error {
		order, err := o.acc.WsApi.OrderCancel(context.Background(), o.Symbol+"USDC", 0, o.ID)
		if err != nil {
			return err
		}
		if order == nil || order.OrderID == 0 {
			return errors.New("cancel order returned empty result")
		}
		return nil
	})
}
