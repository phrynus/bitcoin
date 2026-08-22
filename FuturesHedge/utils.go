package main

import (
	"errors"
	"fmt"
	"time"

	"main/exchange"
	"main/logger"

	"github.com/shopspring/decimal"
)

// getSymbolInfo 从共享的交易所元数据中获取交易对信息(含过滤器)。
func getSymbolInfo(symbol string) (*exchange.SymbolInfo, error) {
	if Exc == nil {
		return nil, errors.New("exchange not initialized")
	}
	si, ok := Exc.GetSymbol(symbol)
	if !ok {
		return nil, fmt.Errorf("symbol %s not found in exchange info", symbol)
	}
	return si, nil
}

// formatQuantityPrice 按交易所精度规则把目标金额换算成合法价格和数量(限价单)。
// 价格四舍五入到 tickSize 整数倍, 数量向上取整到 stepSize 整数倍并校验最小名义价值。
func formatQuantityPrice(symbol string, price, usdt decimal.Decimal) (string, string, error) {
	si, err := getSymbolInfo(symbol)
	if err != nil {
		return "", "", err
	}
	if si.PriceFilter == nil || si.LotSizeFilter == nil {
		return "", "", fmt.Errorf("symbol %s 缺少价格/数量过滤器", symbol)
	}

	pf := si.PriceFilter
	lf := si.LotSizeFilter
	minPrice := parseDecimal(pf.MinPrice)
	maxPrice := parseDecimal(pf.MaxPrice)
	tickSize := parseDecimal(pf.TickSize)
	stepSize := parseDecimal(lf.StepSize)
	if !tickSize.IsPositive() || !stepSize.IsPositive() {
		return "", "", errors.New("invalid exchange precision")
	}
	if !price.IsPositive() {
		return "", "", errors.New("price must be positive")
	}
	if minPrice.IsPositive() && price.LessThan(minPrice) {
		return "", "", fmt.Errorf("price %s 小于 MinPrice %s", price, pf.MinPrice)
	}
	if maxPrice.IsPositive() && price.GreaterThan(maxPrice) {
		return "", "", fmt.Errorf("price %s 大于 MaxPrice %s", price, pf.MaxPrice)
	}

	priceDecimals := decimalPlaces(tickSize)
	priceTicks := price.Div(tickSize).Round(0)
	priceValue := priceTicks.Mul(tickSize)
	if !priceValue.IsPositive() {
		return "", "", errors.New("price error")
	}

	quantityDecimals := decimalPlaces(stepSize)
	quantityTicks := usdt.Div(priceValue).Div(stepSize).Ceil()
	quantityValue := quantityTicks.Mul(stepSize)

	// 最小名义价值校验: 不足时提高数量(向上取整到 step 的整数倍)
	if si.MinNotionalFilter != nil {
		if notional := parseDecimal(si.MinNotionalFilter.Notional); notional.IsPositive() &&
			priceValue.Mul(quantityValue).LessThan(notional) {
			quantityValue = notional.Div(priceValue).Div(stepSize).Ceil().Mul(stepSize)
		}
	}

	minQuantity := parseDecimal(lf.MinQuantity)
	if minQuantity.IsPositive() && quantityValue.LessThan(minQuantity) {
		quantityValue = minQuantity.Div(stepSize).Ceil().Mul(stepSize)
	}
	if maxQuantity := parseDecimal(lf.MaxQuantity); maxQuantity.IsPositive() && quantityValue.GreaterThan(maxQuantity) {
		return "", "", fmt.Errorf("quantity %s 超过 MaxQuantity %s", quantityValue, lf.MaxQuantity)
	}

	priceStr := trimDecimalString(priceValue.StringFixed(priceDecimals))
	quantityStr := trimDecimalString(quantityValue.StringFixed(quantityDecimals))

	logger.Debugf("数量价格格式化: %s tick=%s step=%s price=%s quantity=%s", symbol, formatDecimal(tickSize), formatDecimal(stepSize), priceStr, quantityStr)
	return priceStr, quantityStr, nil
}

// formatQuantity 按交易所步长把数量调整为可下单值(向上取整到 stepSize 整数倍)。
func formatQuantity(symbol string, quantity decimal.Decimal) (string, error) {
	si, err := getSymbolInfo(symbol)
	if err != nil {
		return "", err
	}
	if si.LotSizeFilter == nil {
		return "", fmt.Errorf("symbol %s 缺少数量过滤器", symbol)
	}

	lf := si.LotSizeFilter
	stepSize := parseDecimal(lf.StepSize)
	if !stepSize.IsPositive() {
		return "", errors.New("invalid exchange precision")
	}
	if quantity.IsNegative() {
		quantity = quantity.Neg()
	}

	quantityDecimals := decimalPlaces(stepSize)
	quantityTicks := quantity.Div(stepSize).Ceil()
	quantityValue := quantityTicks.Mul(stepSize)
	if minQuantity := parseDecimal(lf.MinQuantity); minQuantity.IsPositive() && quantityValue.LessThan(minQuantity) {
		quantityValue = minQuantity.Div(stepSize).Ceil().Mul(stepSize)
	}
	if maxQuantity := parseDecimal(lf.MaxQuantity); maxQuantity.IsPositive() && quantityValue.GreaterThan(maxQuantity) {
		return "", fmt.Errorf("quantity %s 超过 MaxQuantity %s", quantityValue, lf.MaxQuantity)
	}

	quantityStr := trimDecimalString(quantityValue.StringFixed(quantityDecimals))
	logger.Debugf("数量格式化: %s step=%s quantity=%s", symbol, formatDecimal(stepSize), quantityStr)
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
		logger.Warnf("第 %d/%d 次重试失败: %v", i+1, maxRetries+1, lastErr)
	}
	return fmt.Errorf("order failed after %d retries: %w", maxRetries+1, lastErr)
}

func (a *Account) InitPositions() map[string]TCPosition {
	positions := make(map[string]TCPosition)
	for _, symbol := range GetEnv().Symbols {
		positions[symbol.Symbol] = TCPosition{}
	}
	return positions
}

// FormatSymbol 把账户持仓整理成按币种聚合后的 USDT/USDC 双边结构。
func (a *Account) FormatSymbol(positions []PositionMarginDetail) {
	a.Log.Infof("开始整理 %d 条持仓数据…", len(positions))
	formatted := a.InitPositions()

	for _, pos := range positions {
		quantity := pos.PositionAmt
		usd := pos.PositionAmt.Mul(pos.MarkPrice)
		a.Log.Debugf("[仓位整理] symbol=%s position=%s mark=%s profit=%s usd=%s",
			pos.Symbol,
			formatDecimalFixed(pos.PositionAmt, 4),
			formatDecimalFixed(pos.MarkPrice, 6),
			formatDecimalFixed(pos.UnrealizedProfit, 2),
			formatDecimalFixed(usd, 2),
		)

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

	a.TCPositions = formatted
	a.Log.Info("持仓整理完成")
}

// BalancePositions 检查同一币种的 USDT/USDC 双边仓位是否平衡，并提交修正单。
func (a *Account) BalancePositions() bool {
	a.Log.Info("检查双边持仓是否平衡…")
	didLiquidate := false
	env := GetEnv()

	for symbol, pos := range a.TCPositions {
		symbolConfig := env.GetSymbol(symbol)
		if symbolConfig == nil {
			a.Log.Warnf("没有 %s 的配置，跳过平衡检查", symbol)
			continue
		}

		diff := pos.USDC.Quantity.Sub(pos.USDT.Quantity)
		a.Log.Debugf("[仓位平衡] %s usdc=%s usdt=%s diff=%s",
			symbol,
			formatDecimalFixed(pos.USDC.Quantity, 4),
			formatDecimalFixed(pos.USDT.Quantity, 4),
			formatDecimalFixed(diff, 6),
		)

		if diff.Abs().LessThan(balanceEqualThreshold) {
			currentValue := pos.USDC.Quantity.Mul(pos.USDC.Price)
			targetValue := symbolConfig.Price.Mul(env.HoldingRatio)
			a.Log.Debugf("[仓位平衡] %s current=%s target=%s holding_ratio=%s",
				symbol,
				formatDecimalFixed(currentValue, 2),
				formatDecimalFixed(targetValue, 2),
				formatDecimal(env.HoldingRatio),
			)
			if currentValue.GreaterThan(targetValue) {
				closeValue := currentValue.Sub(targetValue)
				a.Log.Infof("%s 持仓价值偏高，需要减仓 %s", symbol, formatDecimalFixed(closeValue, 2))
				a.CreateTC(symbol, closeValue, decimal.Zero)
				didLiquidate = true
			}
			continue
		}

		if pos.USDC.Quantity.GreaterThan(pos.USDT.Quantity) {
			quantity, err := formatQuantity(symbol+"USDC", diff)
			if err != nil {
				a.Log.Errorf("格式化 %s USDC 数量失败: %v", symbol, err)
				continue
			}
			a.Log.Infof("平掉 %s 多余的 USDC 空仓，数量 %s", symbol, quantity)
			a.CreateUSDC(symbol, quantity)
			didLiquidate = true
			continue
		}

		quantity, err := formatQuantity(symbol+"USDT", diff.Neg())
		if err != nil {
			a.Log.Errorf("格式化 %s USDT 数量失败: %v", symbol, err)
			continue
		}
		a.Log.Infof("平掉 %s 多余的 USDT 多仓，数量 %s", symbol, quantity)
		a.CloseUSDT(symbol, quantity)
		didLiquidate = true
	}

	if didLiquidate {
		a.Log.Info("再平衡订单已提交")
	} else {
		a.Log.Info("当前双边持仓已平衡")
	}
	return didLiquidate
}

func (a *Account) GetMinValueSymbol() string {
	minSymbol := ""
	minValue := decimal.Zero
	initialized := false

	for symbol, pos := range a.TCPositions {
		totalValue := pos.USDC.Quantity.Mul(pos.USDC.Price).Add(pos.USDT.Quantity.Mul(pos.USDT.Price))
		a.Log.Debugf("[最小价值] %s totalValue=%s", symbol, formatDecimalFixed(totalValue, 2))
		if !initialized || totalValue.LessThan(minValue) {
			minValue = totalValue
			minSymbol = symbol
			initialized = true
		}
	}

	return minSymbol
}

func (a *Account) GetMaxProfitSymbol() string {
	maxSymbol := ""
	maxProfit := decimal.Zero
	initialized := false

	for symbol, pos := range a.TCPositions {
		totalProfit := pos.USDC.Profit.Add(pos.USDT.Profit)
		a.Log.Debugf("[最大盈利] %s totalProfit=%s", symbol, formatDecimalFixed(totalProfit, 2))
		if !initialized || totalProfit.GreaterThan(maxProfit) {
			maxProfit = totalProfit
			maxSymbol = symbol
			initialized = true
		}
	}

	return maxSymbol
}
