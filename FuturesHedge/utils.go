package main

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/shopspring/decimal"
)

func getFilterDecimalValue(symbol, filterType, key string) (decimal.Decimal, error) {
	s := ExchangeInfo.Get(symbol)
	if s == nil {
		return decimal.Zero, errors.New("symbol not found")
	}

	for _, raw := range s.FiltersRaw {
		if raw["filterType"] != filterType {
			continue
		}
		value, ok := raw[key]
		if !ok || value == "" {
			return decimal.Zero, fmt.Errorf("missing %s for %s", key, symbol)
		}
		return parseDecimal(value), nil
	}

	return decimal.Zero, fmt.Errorf("missing filter %s for %s", filterType, symbol)
}

// formatQuantityPrice 按交易所精度规则把目标金额换算成合法价格和数量。
func formatQuantityPrice(symbol string, price, usdt decimal.Decimal) (string, string, error) {
	s := ExchangeInfo.Get(symbol)
	if s == nil {
		return "", "", errors.New("symbol not found")
	}

	minPrice, err := getFilterDecimalValue(symbol, "PRICE_FILTER", "minPrice")
	if err != nil {
		return "", "", err
	}
	maxPrice, err := getFilterDecimalValue(symbol, "PRICE_FILTER", "maxPrice")
	if err != nil {
		return "", "", err
	}
	if price.LessThan(minPrice) || price.GreaterThan(maxPrice) {
		return "", "", errors.New("price error")
	}

	tickSize, err := getFilterDecimalValue(symbol, "PRICE_FILTER", "tickSize")
	if err != nil {
		return "", "", err
	}
	stepSize, err := getFilterDecimalValue(symbol, "LOT_SIZE", "stepSize")
	if err != nil {
		return "", "", err
	}
	if tickSize.IsZero() || stepSize.IsZero() {
		return "", "", errors.New("invalid exchange precision")
	}

	priceDecimals := decimalPlaces(tickSize)
	priceTicks := price.Div(tickSize).Round(0)
	priceValue := priceTicks.Mul(tickSize)
	if priceValue.IsZero() {
		return "", "", errors.New("price error")
	}

	quantityDecimals := decimalPlaces(stepSize)
	quantityTicks := usdt.Div(priceValue).Div(stepSize).Ceil()
	quantityValue := quantityTicks.Mul(stepSize)

	priceStr := trimDecimalString(priceValue.StringFixed(priceDecimals))
	quantityStr := trimDecimalString(quantityValue.StringFixed(quantityDecimals))

	log.Printf("数量价格格式化: %s tick=%s step=%s price=%s quantity=%s", symbol, formatDecimal(tickSize), formatDecimal(stepSize), priceStr, quantityStr)
	return priceStr, quantityStr, nil
}

// formatQuantity 按交易所步长把数量调整为可下单值。
func formatQuantity(symbol string, quantity decimal.Decimal) (string, error) {
	s := ExchangeInfo.Get(symbol)
	if s == nil {
		return "", errors.New("symbol not found")
	}

	stepSize, err := getFilterDecimalValue(symbol, "LOT_SIZE", "stepSize")
	if err != nil {
		return "", err
	}
	if stepSize.IsZero() {
		return "", errors.New("invalid exchange precision")
	}

	quantityDecimals := decimalPlaces(stepSize)
	quantityTicks := quantity.Div(stepSize).Ceil()
	quantityValue := quantityTicks.Mul(stepSize)
	quantityStr := trimDecimalString(quantityValue.StringFixed(quantityDecimals))
	log.Printf("数量格式化: %s step=%s quantity=%s", symbol, formatDecimal(stepSize), quantityStr)
	return quantityStr, nil
}

// RetryFunc 对下单或撤单操作做有限次重试，降低瞬时接口失败的影响。
func RetryFunc(maxRetries int, orderFunc func() error) error {
	var lastErr error
	for i := 0; i <= maxRetries; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * 20 * time.Millisecond)
		}
		lastErr = orderFunc()
		if lastErr == nil {
			return nil
		}
		log.Printf("第 %d/%d 次重试失败: %v", i+1, maxRetries+1, lastErr)
	}
	return fmt.Errorf("order failed after %d retries: %w", maxRetries+1, lastErr)
}

func InitPositions() map[string]TCPosition {
	positions := make(map[string]TCPosition)
	for _, symbol := range Env.Symbols {
		positions[symbol.Symbol] = TCPosition{}
	}
	return positions
}

// FormatSymbol 把账户持仓整理成按币种聚合后的 USDT/USDC 双边结构。
func FormatSymbol(positions []PositionMarginDetail) {
	log.Printf("开始整理 %d 条持仓数据…", len(positions))
	formatted := InitPositions()

	for _, pos := range positions {
		quantity := pos.PositionAmt
		usd := pos.PositionAmt.Mul(pos.MarkPrice)
		// log.Printf("[仓位整理] symbol=%s，position=%s，mark=%s，profit=%s，usd=%s",
		// 	pos.Symbol,
		// 	formatDecimalFixed(pos.PositionAmt, 4),
		// 	formatDecimalFixed(pos.MarkPrice, 6),
		// 	formatDecimalFixed(pos.UnrealizedProfit, 2),
		// 	formatDecimalFixed(usd, 2),
		// )

		symbolKey := pos.Symbol[len(pos.Symbol)-4:]
		symbolValue := pos.Symbol[:len(pos.Symbol)-4]
		p, exists := formatted[symbolValue]
		if !exists {
			p = TCPosition{}
		}

		switch symbolKey {
		case "USDC":
			quantity = pos.PositionAmt.Abs()
			p.USDC.Quantity = quantity
			p.USDC.Price = pos.MarkPrice
			p.USDC.USD = quantity.Mul(pos.MarkPrice)
			p.USDC.Profit = pos.UnrealizedProfit
		case "USDT":
			p.USDT.Quantity = pos.PositionAmt
			p.USDT.Price = pos.MarkPrice
			p.USDT.USD = usd
			p.USDT.Profit = pos.UnrealizedProfit
		}

		formatted[symbolValue] = p
	}

	TCPositions = formatted
	log.Println("持仓整理完成")
}

// BalancePositions 检查同一币种的 USDT/USDC 双边仓位是否平衡，并提交修正单。
func BalancePositions() bool {
	log.Println("检查双边持仓是否平衡…")
	didLiquidate := false

	for symbol, pos := range TCPositions {
		symbolConfig := Env.GetSymbol(symbol)
		if symbolConfig == nil {
			log.Printf("没有 %s 的配置，跳过平衡检查", symbol)
			continue
		}

		diff := pos.USDC.Quantity.Sub(pos.USDT.Quantity)
		// log.Printf("[仓位平衡] %s，usdc=%s，usdt=%s，diff=%s",
		// 	symbol,
		// 	formatDecimalFixed(pos.USDC.Quantity, 4),
		// 	formatDecimalFixed(pos.USDT.Quantity, 4),
		// 	formatDecimalFixed(diff, 6),
		// )

		if diff.Abs().LessThan(balanceEqualThreshold) {
			currentValue := pos.USDC.Quantity.Mul(pos.USDC.Price)
			targetValue := symbolConfig.Price.Mul(Env.HoldingRatio)
			// log.Printf("[仓位平衡] %s，current=%s，target=%s，holding_ratio=%s",
			// 	symbol,
			// 	formatDecimalFixed(currentValue, 2),
			// 	formatDecimalFixed(targetValue, 2),
			// 	formatDecimal(Env.HoldingRatio),
			// )
			if currentValue.GreaterThan(targetValue) {
				closeValue := currentValue.Sub(targetValue)
				log.Printf("%s 持仓价值偏高，需要减仓 %s", symbol, formatDecimalFixed(closeValue, 2))
				CreateTC(symbol, closeValue, decimal.Zero)
				didLiquidate = true
			}
			continue
		}

		if pos.USDC.Quantity.GreaterThan(pos.USDT.Quantity) {
			quantity, err := formatQuantity(symbol+"USDC", diff)
			if err != nil {
				log.Printf("格式化 %s USDC 数量失败: %v", symbol, err)
				continue
			}
			log.Printf("平掉 %s 多余的 USDC 空仓，数量 %s", symbol, quantity)
			CreateUSDC(symbol, quantity)
			didLiquidate = true
			continue
		}

		quantity, err := formatQuantity(symbol+"USDT", diff.Neg())
		if err != nil {
			log.Printf("格式化 %s USDT 数量失败: %v", symbol, err)
			continue
		}
		log.Printf("平掉 %s 多余的 USDT 多仓，数量 %s", symbol, quantity)
		CloseUSDT(symbol, quantity)
		didLiquidate = true
	}

	if didLiquidate {
		log.Println("再平衡订单已提交")
	} else {
		log.Println("当前双边持仓已平衡")
	}
	return didLiquidate
}

func GetMinValueSymbol() string {
	minSymbol := ""
	minValue := decimal.Zero
	initialized := false

	for symbol, pos := range TCPositions {
		totalValue := pos.USDC.Quantity.Mul(pos.USDC.Price).Add(pos.USDT.Quantity.Mul(pos.USDT.Price))
		if !initialized || totalValue.LessThan(minValue) {
			minValue = totalValue
			minSymbol = symbol
			initialized = true
		}
	}

	return minSymbol
}

func GetMaxProfitSymbol() string {
	maxSymbol := ""
	maxProfit := decimal.Zero
	initialized := false

	for symbol, pos := range TCPositions {
		totalProfit := pos.USDC.Profit.Add(pos.USDT.Profit)
		if !initialized || totalProfit.GreaterThan(maxProfit) {
			maxProfit = totalProfit
			maxSymbol = symbol
			initialized = true
		}
	}

	return maxSymbol
}
