package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type TC struct {
	Symbol string
	Usdt   decimal.Decimal
	ID     string
	acc    *Account
	Handle func(data *futures.WsUserDataOrderTradeUpdate)
	doneC  chan struct{}
	errCh  chan error
	timer  *time.Timer
}

// NewTC 创建一组先挂 USDC 空单、成交后再买入 USDT 多单的组合订单。
func (a *Account) NewTC(symbol string, usdt decimal.Decimal) *TC {
	t := &TC{
		Symbol: symbol,
		Usdt:   usdt,
		ID:     uuid.New().String(),
		acc:    a,
		doneC:  make(chan struct{}),
		errCh:  make(chan error),
	}
	t.Handle = t.HandleFilled
	return t
}

// Start 发起组合单的第一步：先在 USDC 合约挂空单等待成交。
func (t *TC) Start() (chan struct{}, chan error) {
	t.acc.Log.Infof("开始执行组合单，%s 金额 %s，ID: %s", t.Symbol, formatDecimalFixed(t.Usdt, 2), t.ID)

	book, err := t.acc.WsApi.BookTicker(context.Background(), t.Symbol+"USDC")
	if err != nil {
		t.acc.Log.Errorf("获取 %sUSDC 盘口失败: %v", t.Symbol, err)
		t.errCh <- fmt.Errorf("get book ticker failed: %w", err)
		close(t.errCh)
		return t.doneC, t.errCh
	}
	if book == nil || book.AskPrice == "" {
		err := errors.New("book ticker not found")
		t.acc.Log.Errorf("获取 %sUSDC 盘口失败: %v", t.Symbol, err)
		t.errCh <- err
		close(t.errCh)
		return t.doneC, t.errCh
	}

	askPrice := parseDecimal(book.AskPrice)
	if !askPrice.IsPositive() {
		err := fmt.Errorf("invalid ask price %q", book.AskPrice)
		t.acc.Log.Errorf("获取 %sUSDC 盘口失败: %v", t.Symbol, err)
		t.errCh <- err
		close(t.errCh)
		return t.doneC, t.errCh
	}

	price, quantity, err := formatQuantityPrice(t.Symbol+"USDC", askPrice, t.Usdt)
	if err != nil {
		t.acc.Log.Errorf("计算下单价格和数量失败: %v", err)
		t.errCh <- err
		close(t.errCh)
		return t.doneC, t.errCh
	}

	t.acc.Log.Infof("提交 USDC 卖单，%sUSDC 价格 %s 数量 %s", t.Symbol, price, quantity)
	err = RetryFunc(GetEnv().RetryCount, func() error {
		order, err := t.acc.WsApi.NewOrder().
			NewClientOrderID(t.ID).
			Symbol(t.Symbol + "USDC").
			Side("SELL").
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
		t.acc.Log.Debugf("组合单 %s: USDC 限价卖单已提交 orderId=%d status=%s", t.ID, order.OrderID, order.Status)
		return nil
	})
	if err != nil {
		t.acc.Log.Errorf("提交 USDC 卖单失败: %v", err)
		t.errCh <- fmt.Errorf("place USDC order failed: %w", err)
		close(t.errCh)
		return t.doneC, t.errCh
	}

	t.timer = time.AfterFunc(GetEnv().FillTimeoutDuration, func() {
		t.acc.Log.Warnf("订单超时未成交，ID: %s", t.ID)
		if err := t.cancelOrder(); err != nil {
			t.acc.Log.Errorf("取消订单失败: %v", err)
		}
		t.errCh <- errors.New("order timed out before fill")
		close(t.errCh)
	})

	return t.doneC, t.errCh
}

// HandleFilled 在第一笔 USDC 空单成交后，补上对应的 USDT 多单完成对冲。
func (t *TC) HandleFilled(data *futures.WsUserDataOrderTradeUpdate) {
	t.acc.Log.Infof("首笔卖单已成交，%s 数量 %s", t.Symbol, data.OrderTradeUpdate.OriginalQty)
	t.acc.Log.Debugf("组合单 %s: 成交价=%s 成交数量=%s", t.ID, data.OrderTradeUpdate.AveragePrice, data.OrderTradeUpdate.OriginalQty)
	if t.timer != nil {
		t.timer.Stop()
	}

	quantity, err := formatQuantity(t.Symbol+"USDT", parseDecimal(data.OrderTradeUpdate.OriginalQty))
	if err != nil {
		t.acc.Log.Errorf("格式化 USDT 下单数量失败: %v", err)
		return
	}
	err = RetryFunc(GetEnv().RetryCount, func() error {
		order, err := t.acc.WsApi.NewOrder().
			Symbol(t.Symbol + "USDT").
			Side("BUY").
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
		t.acc.Log.Errorf("买入 USDT 失败，回退：平掉 USDC 空单: %v", err)
		t.acc.Log.Debugf("组合单 %s: 回退平仓 USDC 数量=%s", t.ID, data.OrderTradeUpdate.OriginalQty)
		t.acc.CreateUSDC(t.Symbol, data.OrderTradeUpdate.OriginalQty)
		t.errCh <- fmt.Errorf("buy USDT failed: %w", err)
		close(t.errCh)
		return
	}

	t.acc.Log.Info("对冲完成")
	t.acc.Log.Debugf("组合单 %s: 全部完成，关闭完成通道", t.ID)
	if t.timer != nil {
		t.timer.Stop()
	}
	close(t.doneC)
}

func (t *TC) cancelOrder() error {
	t.acc.Log.Debugf("组合单 %s: 开始取消 USDC 挂单", t.ID)
	return RetryFunc(GetEnv().RetryCount, func() error {
		order, err := t.acc.WsApi.OrderCancel(context.Background(), t.Symbol+"USDC", 0, t.ID)
		if err != nil {
			return err
		}
		if order == nil || order.OrderID == 0 {
			return errors.New("cancel order returned empty result")
		}
		return nil
	})
}

func (a *Account) CreateUSDC(symbol string, quantity string) {
	// CreateUSDC 用市价单回补 USDC 空仓。
	a.Log.Infof("回补 %s USDC 空仓，数量 %s", symbol, quantity)
	formatted, err := formatQuantity(symbol+"USDC", parseDecimal(quantity))
	if err != nil {
		a.Log.Errorf("格式化 %s USDC 数量失败: %v", symbol, err)
		return
	}
	err = RetryFunc(GetEnv().RetryCount, func() error {
		order, err := a.WsApi.NewOrder().
			Symbol(symbol + "USDC").
			Side("BUY").
			PositionSide("SHORT").
			Type("MARKET").
			Quantity(formatted).
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
		a.Log.Errorf("回补 USDC 空仓失败: %v", err)
	}
}

func (a *Account) CloseUSDT(symbol string, quantity string) {
	// CloseUSDT 用市价单卖出 USDT 多仓。
	a.Log.Infof("平掉 %s USDT 多仓，数量 %s", symbol, quantity)
	formatted, err := formatQuantity(symbol+"USDT", parseDecimal(quantity))
	if err != nil {
		a.Log.Errorf("格式化 %s USDT 数量失败: %v", symbol, err)
		return
	}
	err = RetryFunc(GetEnv().RetryCount, func() error {
		order, err := a.WsApi.NewOrder().
			Symbol(symbol + "USDT").
			Side("SELL").
			PositionSide("LONG").
			Type("MARKET").
			Quantity(formatted).
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
		a.Log.Errorf("平掉 USDT 多仓失败: %v", err)
	}
}

// CreateTC 按给定数量或给定金额执行一组减仓对冲单。
func (a *Account) CreateTC(symbol string, usdt, q decimal.Decimal) {
	a.Log.Infof("开始减仓对冲，%s 金额 %s 数量 %s", symbol, formatDecimalFixed(usdt, 2), formatDecimalFixed(q, 4))

	book, err := a.WsApi.BookTicker(context.Background(), symbol+"USDC")
	if err != nil {
		a.Log.Errorf("获取 %sUSDC 盘口失败: %v", symbol, err)
	}

	quantity := ""
	if q.GreaterThan(decimal.Zero) {
		a.Log.Debugf("减仓对冲 %s: 按指定数量 mode, q=%s", symbol, formatDecimalFixed(q, 4))
		quantity, err = formatQuantity(symbol+"USDC", q)
		if err != nil {
			a.Log.Errorf("格式化下单数量失败: %v", err)
			return
		}
	} else {
		a.Log.Debugf("减仓对冲 %s: 按金额换算 mode, usdt=%s", symbol, formatDecimalFixed(usdt, 2))
		if book == nil || book.BidPrice == "" {
			a.Log.Warnf("缺少 %sUSDC 盘口数据", symbol)
			return
		}
		bidPrice := parseDecimal(book.BidPrice)
		if !bidPrice.IsPositive() {
			a.Log.Errorf("%sUSDC 买一价无效: %s", symbol, book.BidPrice)
			return
		}
		_, quantity, err = formatQuantityPrice(symbol+"USDC", bidPrice, usdt)
		if err != nil {
			a.Log.Errorf("按金额换算数量失败: %v", err)
			return
		}
	}
	if quantity == "" {
		a.Log.Errorf("%s USDC 数量计算为空", symbol)
		return
	}

	err = RetryFunc(GetEnv().RetryCount, func() error {
		order, err := a.WsApi.NewOrder().
			Symbol(symbol + "USDC").
			Side("BUY").
			PositionSide("SHORT").
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
		a.Log.Errorf("平掉 USDC 空仓失败，跳过 USDT 腿避免单边风险: %v", err)
		return
	}

	quantityUsdt, err := formatQuantity(symbol+"USDT", parseDecimal(quantity))
	if err != nil {
		a.Log.Errorf("格式化 USDT 下单数量失败: %v", err)
		return
	}
	a.Log.Debugf("减仓对冲 %s: USDC平仓=%s USDT平仓=%s", symbol, quantity, quantityUsdt)

	err = RetryFunc(GetEnv().RetryCount, func() error {
		order, err := a.WsApi.NewOrder().
			Symbol(symbol + "USDT").
			Side("SELL").
			PositionSide("LONG").
			Type("MARKET").
			Quantity(quantityUsdt).
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
		a.Log.Errorf("平掉 USDT 多仓失败: %v", err)
	}
}
