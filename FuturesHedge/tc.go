package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/adshao/go-binance/v2/futures"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type TC struct {
	Symbol string
	Usdt   decimal.Decimal
	ID     string
	Handle func(data *futures.WsUserDataOrderTradeUpdate)
	doneC  chan struct{}
	errCh  chan error
	timer  *time.Timer
}

// NewTC 创建一组先挂 USDC 空单、成交后再买入 USDT 多单的组合订单。
func NewTC(symbol string, usdt decimal.Decimal) *TC {
	t := &TC{
		Symbol: symbol,
		Usdt:   usdt,
		ID:     uuid.New().String(),
		doneC:  make(chan struct{}),
		errCh:  make(chan error),
	}
	t.Handle = t.HandleFilled
	return t
}

// Start 发起组合单的第一步：先在 USDC 合约挂空单等待成交。
func (t *TC) Start() (chan struct{}, chan error) {
	log.Printf("开始执行组合单，%s 金额 %s，ID: %s", t.Symbol, formatDecimalFixed(t.Usdt, 2), t.ID)

	bookTicker, err := Client.NewListBookTickersService().Symbol(t.Symbol + "USDC").Do(context.Background())
	if err != nil {
		log.Printf("获取 %sUSDC 盘口失败: %v", t.Symbol, err)
	}
	if len(bookTicker) == 0 {
		err := errors.New("book ticker not found")
		t.errCh <- err
		close(t.errCh)
		return t.doneC, t.errCh
	}

	askPrice := parseDecimal(bookTicker[0].AskPrice)
	price, quantity, err := formatQuantityPrice(t.Symbol+"USDC", askPrice, t.Usdt)
	if err != nil {
		log.Printf("计算下单价格和数量失败: %v", err)
		t.errCh <- err
		close(t.errCh)
		return t.doneC, t.errCh
	}

	log.Printf("提交 USDC 卖单，%sUSDC 价格 %s 数量 %s", t.Symbol, price, quantity)
	err = RetryFunc(Env.RetryCount, func() error {
		_, err := Client.NewCreateOrderService().
			NewClientOrderID(t.ID).
			Symbol(t.Symbol + "USDC").
			Side(futures.SideTypeSell).
			Type(futures.OrderTypeLimit).
			PositionSide(futures.PositionSideTypeShort).
			TimeInForce(futures.TimeInForceTypeGTC).
			Quantity(quantity).
			Price(price).
			Do(context.Background())
		return err
	})
	if err != nil {
		log.Printf("提交 USDC 卖单失败: %v", err)
		t.errCh <- fmt.Errorf("place USDC order failed: %w", err)
		close(t.errCh)
		return t.doneC, t.errCh
	}

	t.timer = time.AfterFunc(Env.FillTimeoutDuration, func() {
		log.Printf("订单超时未成交，ID: %s", t.ID)
		if err := t.cancelOrder(); err != nil {
			log.Printf("取消订单失败: %v", err)
		}
		t.errCh <- errors.New("order timed out before fill")
		close(t.errCh)
	})

	return t.doneC, t.errCh
}

// HandleFilled 在第一笔 USDC 空单成交后，补上对应的 USDT 多单完成对冲。
func (t *TC) HandleFilled(data *futures.WsUserDataOrderTradeUpdate) {
	log.Printf("首笔卖单已成交，%s 数量 %s", t.Symbol, data.OrderTradeUpdate.OriginalQty)
	if t.timer != nil {
		t.timer.Stop()
	}

	quantity, err := formatQuantity(t.Symbol+"USDT", parseDecimal(data.OrderTradeUpdate.OriginalQty))
	if err != nil {
		log.Printf("格式化 USDT 下单数量失败: %v", err)
		return
	}
	err = RetryFunc(Env.RetryCount, func() error {
		_, err := Client.NewCreateOrderService().
			Symbol(t.Symbol + "USDT").
			Side(futures.SideTypeBuy).
			Type(futures.OrderTypeMarket).
			PositionSide(futures.PositionSideTypeLong).
			Quantity(quantity).
			Do(context.Background())
		return err
	})
	if err != nil {
		log.Printf("买入 USDT 失败，回退：平掉 USDC 空单: %v", err)
		CreateUSDC(t.Symbol, data.OrderTradeUpdate.OriginalQty)
		t.errCh <- fmt.Errorf("buy USDT failed: %w", err)
		close(t.errCh)
		return
	}

	log.Println("对冲完成")
	if t.timer != nil {
		t.timer.Stop()
	}
	close(t.doneC)
}

func (t *TC) cancelOrder() error {
	return RetryFunc(Env.RetryCount, func() error {
		_, err := Client.NewCancelOrderService().
			Symbol(t.Symbol + "USDC").
			OrigClientOrderID(t.ID).
			Do(context.Background())
		return err
	})
}

func CreateUSDC(symbol string, quantity string) {
	// CreateUSDC 用市价单回补 USDC 空仓。
	log.Printf("回补 %s USDC 空仓，数量 %s", symbol, quantity)
	err := RetryFunc(Env.RetryCount, func() error {
		_, err := Client.NewCreateOrderService().
			Symbol(symbol + "USDC").
			Side(futures.SideTypeBuy).
			Type(futures.OrderTypeMarket).
			PositionSide(futures.PositionSideTypeShort).
			Quantity(quantity).
			Do(context.Background())
		return err
	})
	if err != nil {
		log.Printf("回补 USDC 空仓失败: %v", err)
	}
}

func CloseUSDT(symbol string, quantity string) {
	// CloseUSDT 用市价单卖出 USDT 多仓。
	log.Printf("平掉 %s USDT 多仓，数量 %s", symbol, quantity)
	err := RetryFunc(Env.RetryCount, func() error {
		_, err := Client.NewCreateOrderService().
			Symbol(symbol + "USDT").
			Side(futures.SideTypeSell).
			Type(futures.OrderTypeMarket).
			PositionSide(futures.PositionSideTypeLong).
			Quantity(quantity).
			Do(context.Background())
		return err
	})
	if err != nil {
		log.Printf("平掉 USDT 多仓失败: %v", err)
	}
}

// CreateTC 按给定数量或给定金额执行一组减仓对冲单。
func CreateTC(symbol string, usdt, q decimal.Decimal) {
	log.Printf("开始减仓对冲，%s 金额 %s 数量 %s", symbol, formatDecimalFixed(usdt, 2), formatDecimalFixed(q, 4))

	bookTicker, err := Client.NewListBookTickersService().Symbol(symbol + "USDC").Do(context.Background())
	if err != nil {
		log.Printf("获取 %sUSDC 盘口失败: %v", symbol, err)
	}

	quantity := ""
	if q.GreaterThan(decimal.Zero) {
		quantity, err = formatQuantity(symbol+"USDC", q)
		if err != nil {
			log.Printf("格式化下单数量失败: %v", err)
			return
		}
	} else {
		if len(bookTicker) == 0 {
			log.Printf("缺少 %sUSDC 盘口数据", symbol)
			return
		}
		bidPrice := parseDecimal(bookTicker[0].BidPrice)
		_, quantity, err = formatQuantityPrice(symbol+"USDC", bidPrice, usdt)
		if err != nil {
			log.Printf("按金额换算数量失败: %v", err)
			return
		}
	}

	err = RetryFunc(Env.RetryCount, func() error {
		_, err := Client.NewCreateOrderService().
			Symbol(symbol + "USDC").
			Side(futures.SideTypeBuy).
			Type(futures.OrderTypeMarket).
			PositionSide(futures.PositionSideTypeShort).
			Quantity(quantity).
			Do(context.Background())
		return err
	})
	if err != nil {
		log.Printf("平掉 USDC 空仓失败: %v", err)
	}

	quantityUsdt, err := formatQuantity(symbol+"USDT", parseDecimal(quantity))
	if err != nil {
		log.Printf("格式化 USDT 下单数量失败: %v", err)
		return
	}

	err = RetryFunc(Env.RetryCount, func() error {
		_, err := Client.NewCreateOrderService().
			Symbol(symbol + "USDT").
			Side(futures.SideTypeSell).
			Type(futures.OrderTypeMarket).
			PositionSide(futures.PositionSideTypeLong).
			Quantity(quantityUsdt).
			Do(context.Background())
		return err
	})
	if err != nil {
		log.Printf("平掉 USDT 多仓失败: %v", err)
	}
}
